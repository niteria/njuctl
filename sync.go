package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
)

type reservation struct {
	Name string `json:"name,omitempty"`
	MAC  string `json:"mac"`
	IP   string `json:"ip"`
}

type change struct {
	Action string `json:"action"`
	reservation
}

type syncResult struct {
	Changes  []change `json:"changes"`
	Applied  bool     `json:"applied"`
	Verified bool     `json:"verified"`
}

func normalize(mac, ip string) (string, string, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 || hw[0]&1 != 0 || hw.String() == "00:00:00:00:00:00" {
		return "", "", errors.New("expected a unicast six-byte MAC address")
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is4() || !addr.IsGlobalUnicast() || addr.IsLoopback() {
		return "", "", errors.New("expected a unicast IPv4 address")
	}
	return hw.String(), addr.String(), nil
}

func loadReservations(path string) ([]reservation, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var config struct {
		Reservations *[]reservation `json:"reservations"`
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	if config.Reservations == nil {
		return nil, errors.New("config must contain a reservations array (use [] to explicitly remove all reservations)")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("config must contain exactly one JSON object")
	}
	reservations := *config.Reservations
	macs, ips := map[string]bool{}, map[string]bool{}
	for i := range reservations {
		r := &reservations[i]
		mac, ip, err := normalize(r.MAC, r.IP)
		if err != nil {
			return nil, fmt.Errorf("reservation %d (%s): %w", i+1, r.Name, err)
		}
		if macs[mac] || ips[ip] {
			return nil, fmt.Errorf("duplicate MAC or IP in reservation %d (%s)", i+1, r.Name)
		}
		macs[mac], ips[ip] = true, true
		r.MAC, r.IP = mac, ip
	}
	return reservations, nil
}

// Deletes run first so address moves, replacements and swaps can be reconciled.
// Only static leases are managed; dynamic leases and other settings are untouched.
func planSync(current []lease, desired []reservation) ([]change, error) {
	want := make(map[string]reservation, len(desired))
	for _, r := range desired {
		want[r.MAC] = r
	}
	seen := make(map[string]string, len(current))
	ips := map[string]bool{}
	deletes, adds := []change{}, []change{}
	for _, l := range current {
		mac, ip, err := normalize(l.MACAddress, l.IPAddress)
		if err != nil {
			return nil, fmt.Errorf("invalid router reservation: %w", err)
		}
		if _, duplicate := seen[mac]; duplicate || ips[ip] {
			return nil, errors.New("router returned duplicate MAC or IP reservations")
		}
		seen[mac], ips[ip] = ip, true
		if r, exists := want[mac]; !exists || r.IP != ip {
			deletes = append(deletes, change{Action: "remove", reservation: reservation{MAC: mac, IP: ip}})
		}
	}
	for _, r := range desired {
		if seen[r.MAC] != r.IP {
			adds = append(adds, change{Action: "add", reservation: r})
		}
	}
	less := func(changes []change) {
		sort.Slice(changes, func(i, j int) bool { return changes[i].MAC < changes[j].MAC })
	}
	less(deletes)
	less(adds)
	return append(deletes, adds...), nil
}

func (r *router) sync(ctx context.Context, desired []reservation, apply bool) (syncResult, error) {
	result := syncResult{Changes: []change{}}
	current, err := r.leases(ctx)
	if err != nil {
		return result, err
	}
	result.Changes, err = planSync(current, desired)
	if err != nil || !apply {
		return result, err
	}
	for i, c := range result.Changes {
		method := "deleteStaticLease"
		parameters := map[string]string{"MACAddress": c.MAC}
		if c.Action == "add" {
			method = "addStaticLease"
			parameters["IPAddress"] = c.IP
		}
		if err := r.rpc(ctx, pool, method, parameters, nil); err != nil {
			return result, fmt.Errorf("sync stopped at change %d/%d (%s %s → %s); router may be partially updated, rerun sync to inspect: %w", i+1, len(result.Changes), c.Action, c.MAC, c.IP, err)
		}
	}
	result.Applied = true
	after, err := r.leases(ctx)
	if err != nil {
		return result, fmt.Errorf("sync submitted but readback failed: %w", err)
	}
	remaining, err := planSync(after, desired)
	if err != nil {
		return result, fmt.Errorf("sync readback: %w", err)
	}
	if len(remaining) != 0 {
		return result, errors.New("sync readback differs from desired reservations; rerun sync to inspect")
	}
	result.Verified = true
	return result, nil
}

func printSync(w io.Writer, result syncResult) error {
	for _, c := range result.Changes {
		label := ""
		if c.Name != "" {
			label = " (" + c.Name + ")"
		}
		if _, err := fmt.Fprintf(w, "%s %s → %s%s\n", strings.ToUpper(c.Action), c.MAC, c.IP, label); err != nil {
			return err
		}
	}
	message := "Preview only; use --apply sync to reconcile the static reservation list."
	if result.Verified {
		message = "Static reservations match configuration (verified)."
	} else if len(result.Changes) == 0 {
		message = "Static reservations already match configuration."
	}
	_, err := fmt.Fprintln(w, message)
	return err
}
