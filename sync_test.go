package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSyncReconciliation(t *testing.T) {
	const a = "02:00:00:00:00:01"
	const b = "02:00:00:00:00:02"
	const c = "02:00:00:00:00:03"
	desired := []reservation{{Name: "a", MAC: a, IP: "192.0.2.2"}, {Name: "b", MAC: b, IP: "192.0.2.1"}}
	for _, scenario := range []string{"preview", "apply", "no-op", "write failure", "readback mismatch"} {
		t.Run(scenario, func(t *testing.T) {
			current := []lease{{a, "192.0.2.1"}, {b, "192.0.2.2"}, {c, "192.0.2.3"}}
			if scenario == "no-op" {
				current = []lease{{strings.ToUpper(b), "192.0.2.1"}, {a, "192.0.2.2"}}
			}
			before := append([]lease{}, current...)
			var writes []string
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var call struct {
					Service    string
					Method     string
					Parameters map[string]string
				}
				if err := json.NewDecoder(req.Body).Decode(&call); err != nil {
					t.Error(err)
				}
				if call.Service != pool {
					t.Errorf("unexpected service %s", call.Service)
				}
				mac, ip := call.Parameters["MACAddress"], call.Parameters["IPAddress"]
				switch call.Method {
				case "getStaticLeases":
					reads++
					json.NewEncoder(w).Encode(map[string]any{"status": current})
					return
				case "deleteStaticLease":
					writes = append(writes, "remove "+mac)
					if scenario == "write failure" && len(writes) == 2 {
						fmt.Fprint(w, `{"status":null,"errors":[{"error":1}]}`)
						return
					}
					for i, l := range current {
						if l.MACAddress == mac {
							current = append(current[:i], current[i+1:]...)
							break
						}
					}
				case "addStaticLease":
					writes = append(writes, "add "+mac)
					for _, l := range current {
						if l.MACAddress == mac || l.IPAddress == ip {
							t.Error("add attempted before conflicting reservation was removed")
						}
					}
					if scenario != "readback mismatch" {
						current = append(current, lease{mac, ip})
					}
				default:
					t.Errorf("unexpected method %s", call.Method)
				}
				fmt.Fprint(w, `{"status":null}`)
			}))
			defer server.Close()
			r := &router{base: server.URL, client: server.Client()}
			result, err := r.sync(context.Background(), desired, scenario != "preview")
			if scenario == "write failure" || scenario == "readback mismatch" {
				if err == nil || result.Verified {
					t.Fatalf("expected failure, got %+v, %v", result, err)
				}
				if scenario == "write failure" && len(writes) != 2 {
					t.Fatal("did not stop on write failure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "preview" || scenario == "no-op" {
				if len(writes) != 0 || !reflect.DeepEqual(current, before) {
					t.Fatal("preview or no-op mutated router")
				}
			} else {
				want := []string{"remove " + a, "remove " + b, "remove " + c, "add " + a, "add " + b}
				if !reflect.DeepEqual(writes, want) {
					t.Fatalf("writes %v, want %v", writes, want)
				}
			}
			if scenario == "preview" {
				if reads != 1 || result.Applied || result.Verified || len(result.Changes) != 5 {
					t.Fatalf("incorrect preview %+v, reads %d", result, reads)
				}
			} else if reads != 2 || !result.Verified {
				t.Fatalf("missing verification %+v, reads %d", result, reads)
			}
		})
	}
}

func TestLoadReservations(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		valid        bool
	}{
		{"empty explicit", `{"reservations":[]}`, true},
		{"normalize", `{"reservations":[{"name":"test","mac":"02:AB:00:00:00:01","ip":"192.0.2.1"}]}`, true},
		{"missing", `{}`, false},
		{"null", `{"reservations":null}`, false},
		{"typo", `{"reservatons":[]}`, false},
		{"unknown field", `{"reservations":[{"mac":"02:00:00:00:00:01","ip":"192.0.2.1","extra":1}]}`, false},
		{"invalid MAC", `{"reservations":[{"mac":"bad","ip":"192.0.2.1"}]}`, false},
		{"invalid IP", `{"reservations":[{"mac":"02:00:00:00:00:01","ip":"::1"}]}`, false},
		{"duplicate MAC", `{"reservations":[{"mac":"02:00:00:00:00:01","ip":"192.0.2.1"},{"mac":"02:00:00:00:00:01","ip":"192.0.2.2"}]}`, false},
		{"duplicate IP", `{"reservations":[{"mac":"02:00:00:00:00:01","ip":"192.0.2.1"},{"mac":"02:00:00:00:00:02","ip":"192.0.2.1"}]}`, false},
		{"trailing JSON", `{"reservations":[]} {}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.config), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := loadReservations(path)
			if (err == nil) != tc.valid {
				t.Fatalf("loadReservations = %v, %v", got, err)
			}
			if tc.name == "normalize" && got[0].MAC != "02:ab:00:00:00:01" {
				t.Fatal("MAC not normalized")
			}
		})
	}
}
