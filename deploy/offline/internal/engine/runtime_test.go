package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestRuntimeReadonlyAPIValidatesNamesInventoryAndReplies(t *testing.T) {
	scope := strings.Repeat("a", 32)
	id := strings.Repeat("b", 64)
	for _, fault := range []string{"none", "absent", "foreign-name", "bad-id", "duplicate-json", "malformed", "null-inventory", "changed-inventory", "server-error"} {
		t.Run(fault, func(t *testing.T) {
			calls := 0
			c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Error("readonly inspection mutated Engine")
				}
				if fault == "server-error" {
					w.WriteHeader(500)
					return
				}
				if fault == "malformed" {
					w.Write([]byte("{"))
					return
				}
				if fault == "duplicate-json" {
					w.Write([]byte(`{"Id":"first","Id":"second"}`))
					return
				}
				if fault == "absent" && r.URL.Path != "/v1.49/containers/json" && r.URL.Path != "/v1.49/networks" {
					w.WriteHeader(404)
					return
				}
				switch r.URL.Path {
				case "/v1.49/containers/" + RuntimeContainerName(scope, "control") + "/json":
					name := "/" + RuntimeContainerName(scope, "control")
					objectID := id
					if fault == "foreign-name" {
						name = "/foreign"
					}
					if fault == "bad-id" {
						objectID = "bad"
					}
					json.NewEncoder(w).Encode(map[string]any{"Id": objectID, "Name": name})
				case "/v1.49/networks/" + RuntimeNetworkName(scope):
					name := RuntimeNetworkName(scope)
					objectID := id
					if fault == "foreign-name" {
						name = "foreign"
					}
					if fault == "bad-id" {
						objectID = "bad"
					}
					json.NewEncoder(w).Encode(map[string]any{"Id": objectID, "Name": name})
				case "/v1.49/containers/json", "/v1.49/networks":
					calls++
					var filters map[string][]string
					if json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters) != nil || len(filters["label"]) != 1 {
						t.Error("missing independent scope/project filter")
					}
					if r.URL.Path == "/v1.49/containers/json" && r.URL.Query().Get("all") != "1" {
						t.Error("stopped objects omitted")
					}
					if fault == "null-inventory" {
						w.Write([]byte("null"))
						return
					}
					object := RuntimeObject{ID: id, Name: "fixed"}
					if fault == "bad-id" {
						object.ID = "bad"
					}
					if fault == "changed-inventory" && calls%2 == 0 {
						object.Name = "changed"
					}
					json.NewEncoder(w).Encode([]RuntimeObject{object})
				default:
					t.Error("unexpected path", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			_, found, e := c.InspectRuntime(context.Background(), scope, "control")
			wantError := fault == "foreign-name" || fault == "bad-id" || fault == "duplicate-json" || fault == "malformed" || fault == "server-error"
			if (e != nil) != wantError || !wantError && found != (fault != "absent") {
				t.Fatal("container response", found, e)
			}
			_, found, e = c.InspectRuntimeNetwork(context.Background(), scope)
			if (e != nil) != wantError || !wantError && found != (fault != "absent") {
				t.Fatal("network response", found, e)
			}
			for _, networks := range []bool{false, true} {
				_, e := c.RuntimeCandidates(context.Background(), scope, networks)
				bad := fault == "bad-id" || fault == "duplicate-json" || fault == "malformed" || fault == "null-inventory" || fault == "changed-inventory" || fault == "server-error"
				if (e != nil) != bad {
					t.Fatal("inventory response", e)
				}
			}
			if _, _, e := c.InspectRuntime(context.Background(), "../foreign", "control"); e == nil {
				t.Fatal("unsafe scope")
			}
			if _, _, e := c.InspectRuntime(context.Background(), scope, "foreign"); e == nil {
				t.Fatal("unknown service")
			}
		})
	}
}

func TestRuntimeVolumeConsumerChecksAdmitOnlyRecordedIDs(t *testing.T) {
	for _, fault := range []string{"none", "consumer"} {
		t.Run(fault, func(t *testing.T) {
			c, owner, _, state := volumeClient(t, fault)
			state.volume.Store(true)
			e := c.VerifyVolumeConsumers(context.Background(), owner, nil)
			if (e != nil) != (fault == "consumer") {
				t.Fatal(e)
			}
			if e = c.VerifyVolumeConsumers(context.Background(), owner, []string{"bad"}); e == nil {
				t.Fatal("invalid admitted identity")
			}
		})
	}
}
