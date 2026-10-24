package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const pool = "DHCPv4.Server.Pool.default"

type router struct {
	base    string
	client  *http.Client
	context string
	cookies map[string]string
}

type lease struct {
	MACAddress string
	IPAddress  string
}

// Never include request URLs or response bodies in errors: authentication URLs
// contain the password, and responses can contain session tokens.
func (r *router) request(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, body)
	if err != nil {
		return errors.New("cannot construct router request")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if r.context != "" {
		req.Header.Set("X-Context", r.context)
	}
	// Funbox uses a non-RFC cookie name such as "abcd1234/sessid".
	// net/http's cookie jar drops it. Keep raw cookie pairs for this one
	// router origin only; redirects are disabled on the HTTP client.
	var pairs []string
	for name, value := range r.cookies {
		pairs = append(pairs, name+"="+value)
	}
	if len(pairs) != 0 {
		req.Header.Set("Cookie", strings.Join(pairs, "; "))
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return errors.New("router request failed (check address, connectivity and timeout)")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("router returned HTTP %d", resp.StatusCode)
	}
	for _, header := range resp.Header.Values("Set-Cookie") {
		pair, _, _ := strings.Cut(header, ";")
		name, value, ok := strings.Cut(pair, "=")
		if ok && strings.TrimSpace(name) != "" {
			if r.cookies == nil {
				r.cookies = make(map[string]string)
			}
			r.cookies[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	if out == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		return err
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(out); err != nil {
		return errors.New("router returned an invalid JSON response")
	}
	return nil
}

func (r *router) login(ctx context.Context, username, password string) error {
	form := url.Values{"username": {username}, "password": {password}}.Encode()
	var result struct {
		Data struct{ ContextID string } `json:"data"`
	}
	err := r.request(ctx, "POST", "/authenticate?"+form, "application/x-www-form-urlencoded", strings.NewReader(form), &result)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if result.Data.ContextID == "" {
		return errors.New("login: no session context returned")
	}
	r.context = result.Data.ContextID
	return nil
}

func (r *router) rpc(ctx context.Context, service, method string, parameters any, out any) error {
	if parameters == nil {
		parameters = map[string]any{}
	}
	body, err := json.Marshal(map[string]any{"service": service, "method": method, "parameters": parameters})
	if err != nil {
		return err
	}
	var result struct {
		Status json.RawMessage `json:"status"`
		Errors json.RawMessage `json:"errors"`
		Error  json.RawMessage `json:"error"`
	}
	if err := r.request(ctx, "POST", "/ws", "application/x-sah-ws-4-call+json", strings.NewReader(string(body)), &result); err != nil {
		return err
	}
	for _, field := range []json.RawMessage{result.Errors, result.Error} {
		if len(field) > 0 && string(field) != "null" && string(field) != "[]" && string(field) != "{}" {
			return fmt.Errorf("router rejected %s:%s", service, method)
		}
	}
	if len(result.Status) == 0 || string(result.Status) == "false" {
		return fmt.Errorf("router did not acknowledge %s:%s", service, method)
	}
	if out != nil {
		if string(result.Status) == "null" {
			return fmt.Errorf("router returned no data for %s:%s", service, method)
		}
		return json.Unmarshal(result.Status, out)
	}
	return nil
}

func (r *router) leases(ctx context.Context) ([]lease, error) {
	var leases []lease
	err := r.rpc(ctx, pool, "getStaticLeases", nil, &leases)
	return leases, err
}

// reserve is idempotent and refuses to silently move an existing reservation.
func (r *router) reserve(ctx context.Context, mac, ip string) (string, error) {
	before, err := r.leases(ctx)
	if err != nil {
		return "", err
	}
	for _, l := range before {
		if strings.EqualFold(l.MACAddress, mac) {
			if l.IPAddress == ip {
				return "already reserved", nil
			}
			return "", fmt.Errorf("MAC %s already reserved at %s", mac, l.IPAddress)
		}
		if l.IPAddress == ip {
			return "", fmt.Errorf("IP %s already reserved for %s", ip, l.MACAddress)
		}
	}
	if err := r.rpc(ctx, pool, "addStaticLease", map[string]string{"MACAddress": mac, "IPAddress": ip}, nil); err != nil {
		return "", err
	}
	after, err := r.leases(ctx)
	if err != nil {
		return "", fmt.Errorf("reservation submitted but readback failed: %w", err)
	}
	for _, l := range after {
		if strings.EqualFold(l.MACAddress, mac) && l.IPAddress == ip {
			return "reserved and verified", nil
		}
	}
	return "", errors.New("reservation submitted but missing from readback")
}

func password(file string) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil
	}
	if value := os.Getenv("NJU_PASSWORD"); value != "" {
		return value, nil
	}
	return "", errors.New("set NJU_PASSWORD or --password-file to the router admin password")
}

func run(args []string, stdout, stderr io.Writer) (err error) {
	flags := flag.NewFlagSet("njuctl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	address := flags.String("router", "", "Funbox base URL (required)")
	username := flags.String("username", "admin", "router login")
	file := flags.String("password-file", "", "file containing the router admin password")
	timeout := flags.Duration("timeout", 20*time.Second, "timeout per HTTP request")
	asJSON := flags.Bool("json", false, "JSON output")
	configFile := flags.String("config", "", "desired reservation JSON file (required for sync)")
	apply := flags.Bool("apply", false, "apply the sync plan and verify readback")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: njuctl [flags] leases | devices | reserve MAC IPv4 | sync\nFlags must precede the command.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	args = flags.Args()
	if len(args) == 0 {
		flags.Usage()
		return errors.New("command required")
	}
	command := args[0]
	var mac, ip string
	var desired []reservation
	if command != "sync" && *apply {
		return errors.New("--apply is only supported with sync")
	}
	switch command {
	case "leases", "devices":
		if len(args) != 1 {
			return errors.New("unexpected command arguments")
		}
	case "reserve":
		if len(args) != 3 {
			return errors.New("usage: njuctl [flags] reserve MAC IPv4")
		}
		var err error
		mac, ip, err = normalize(args[1], args[2])
		if err != nil {
			return err
		}
	case "sync":
		if len(args) != 1 || *configFile == "" {
			return errors.New("usage: njuctl --router URL --config FILE [--apply] sync")
		}
		var err error
		desired, err = loadReservations(*configFile)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown command %q", command)
	}
	if *address == "" {
		return errors.New("--router is required")
	}
	u, err := url.Parse(*address)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("--router must be an HTTP(S) base URL without credentials, path or query")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	ctx := context.Background()
	secret, err := password(*file)
	if err != nil {
		return err
	}
	if secret == "" {
		return errors.New("empty router password")
	}
	r := &router{base: strings.TrimRight(*address, "/"), client: &http.Client{
		Timeout:       *timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
	if err := r.login(ctx, *username, secret); err != nil {
		return err
	}
	defer func() {
		if logoutErr := r.request(context.Background(), "GET", "/logout", "", nil, nil); logoutErr != nil {
			err = errors.Join(err, fmt.Errorf("logout: %w", logoutErr))
		}
	}()
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	switch command {
	case "sync":
		result, err := r.sync(ctx, desired, *apply)
		if err != nil {
			return err
		}
		if *asJSON {
			return encoder.Encode(result)
		}
		return printSync(stdout, result)
	case "leases":
		leases, err := r.leases(ctx)
		if err != nil {
			return err
		}
		if *asJSON {
			return encoder.Encode(leases)
		}
		for _, l := range leases {
			if _, err := fmt.Fprintf(stdout, "%s  %s\n", l.MACAddress, l.IPAddress); err != nil {
				return err
			}
		}
	case "devices":
		var devices json.RawMessage
		if err := r.rpc(ctx, "Devices", "get", map[string]string{"expression": ""}, &devices); err != nil {
			return err
		}
		return encoder.Encode(devices)
	case "reserve":
		status, err := r.reserve(ctx, mac, ip)
		if err != nil {
			return err
		}
		if *asJSON {
			return encoder.Encode(map[string]string{"MACAddress": mac, "IPAddress": ip, "status": status})
		}
		_, err = fmt.Fprintf(stdout, "%s → %s: %s\n", mac, ip, status)
		return err
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "njuctl:", err)
		os.Exit(1)
	}
}
