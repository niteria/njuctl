package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the complete CLI against a fake Funbox, including its form login,
// session cookie, context header, RPC envelope, verification and logout.
func TestReservationSession(t *testing.T) {
	for _, scenario := range []string{"new", "existing", "mac conflict", "ip conflict", "missing readback", "rpc error"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("NJU_PASSWORD", "secret+&password")
			const mac = "02:00:00:00:00:22"
			const ip = "192.0.2.22"
			var leases []lease
			switch scenario {
			case "existing":
				leases = []lease{{mac, ip}}
			case "mac conflict":
				leases = []lease{{mac, "192.0.2.23"}}
			case "ip conflict":
				leases = []lease{{"00:11:22:33:44:55", ip}}
			}
			adds, reads, logouts := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/authenticate":
					if r.Method != "POST" || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
						t.Error("incorrect authentication request")
					}
					if err := r.ParseForm(); err != nil {
						t.Error(err)
					}
					if r.PostForm.Get("password") != "secret+&password" || r.URL.Query().Get("password") != "secret+&password" || r.PostForm.Get("username") != "admin" {
						t.Error("incorrect form encoding or credentials")
					}
					w.Header().Add("Set-Cookie", "abcd1234/sessid=cookie; path=/; SameSite=Strict")
					fmt.Fprint(w, `{"data":{"contextID":"test-context"}}`)
				case "/ws":
					if r.Header.Get("Cookie") != "abcd1234/sessid=cookie" || r.Header.Get("X-Context") != "test-context" {
						t.Error("missing session cookie or context")
					}
					if r.Header.Get("Content-Type") != "application/x-sah-ws-4-call+json" {
						t.Error("incorrect RPC content type")
					}
					var call struct {
						Service    string
						Method     string
						Parameters map[string]string
					}
					if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
						t.Error(err)
					}
					if call.Service != pool {
						t.Errorf("unexpected service %s", call.Service)
					}
					switch call.Method {
					case "getStaticLeases":
						reads++
						if leases == nil {
							fmt.Fprint(w, `{"status":[]}`)
						} else {
							json.NewEncoder(w).Encode(map[string]any{"status": leases})
						}
					case "addStaticLease":
						adds++
						if call.Parameters["MACAddress"] != mac || call.Parameters["IPAddress"] != ip {
							t.Error("incorrect reservation parameters")
						}
						if scenario == "rpc error" {
							fmt.Fprint(w, `{"status":null,"errors":[{"description":"rejected"}]}`)
							return
						}
						if scenario != "missing readback" {
							leases = append(leases, lease{mac, ip})
						}
						// The real router acknowledges writes with null.
						fmt.Fprint(w, `{"status":null}`)
					default:
						t.Errorf("unexpected method %s", call.Method)
					}
				case "/logout":
					logouts++
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer server.Close()
			var output, stderr bytes.Buffer
			err := run([]string{"--router", server.URL, "reserve", strings.ToUpper(mac), ip}, &output, &stderr)
			wantSuccess := scenario == "new" || scenario == "existing"
			if (err == nil) != wantSuccess {
				t.Fatalf("error = %v, output = %s", err, output.String())
			}
			wantAdds, wantReads := 1, 2
			if scenario == "existing" || strings.Contains(scenario, "conflict") {
				wantAdds, wantReads = 0, 1
			} else if scenario == "rpc error" {
				wantReads = 1
			}
			if adds != wantAdds || reads != wantReads || logouts != 1 {
				t.Errorf("adds/reads/logouts = %d/%d/%d, want %d/%d/1", adds, reads, logouts, wantAdds, wantReads)
			}
		})
	}
}

func TestCredentialsNotLeakedAndRedirectsRefused(t *testing.T) {
	for _, scenario := range []string{"unauthorized", "redirect", "broken connection", "invalid JSON"} {
		t.Run(scenario, func(t *testing.T) {
			const secret = "do-not-leak-this-password"
			t.Setenv("NJU_PASSWORD", secret)
			redirected := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/authenticate" {
					redirected = true
				}
				switch scenario {
				case "unauthorized":
					http.Error(w, secret, http.StatusUnauthorized)
				case "redirect":
					http.Redirect(w, r, "/unexpected", http.StatusFound)
				case "broken connection":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
				case "invalid JSON":
					fmt.Fprint(w, secret)
				}
			}))
			defer server.Close()
			var output, stderr bytes.Buffer
			err := run([]string{"--router", server.URL, "leases"}, &output, &stderr)
			if err == nil || strings.Contains(err.Error(), secret) || strings.Contains(output.String()+stderr.String(), secret) {
				t.Fatalf("expected sanitized error, got %v", err)
			}
			if redirected {
				t.Error("followed redirect")
			}
		})
	}
}
