package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestNativePostgreSQLStorageObservationNeverSerializesHostPaths(t *testing.T) {
	o := NativePostgreSQLStorageObservation{AllocationRoot: "/private/actual-engine-root", SourceOwners: []NativePostgreSQLStorageOwner{{Mountpoint: "/private/owned-source-volume"}}}
	raw, e := json.Marshal(o)
	if e != nil || strings.Contains(string(raw), "/private/") {
		t.Fatal("host paths exposed", e)
	}
}

func TestNativePostgreSQLStorageContextRequiresOriginalNativeBackup(t *testing.T) {
	var c *Client
	if _, e := c.ObserveNativePostgreSQLStorage(context.Background(), DatabaseTask{}); e == nil {
		t.Fatal("nil client accepted")
	}
	c = &Client{}
	if _, e := c.ObserveNativePostgreSQLStorage(context.Background(), DatabaseTask{}); e == nil {
		t.Fatal("unbound native task accepted")
	}
	task := nativeRecoveryFixture(NativePostgreSQLBackup)
	if len(nativePostgreSQLStorageSourceOwners(task)) != 0 {
		t.Fatal("missing registry silently omitted")
	}
	p := task.NativePostgreSQLRecovery
	p.Registry = &NativePostgreSQLRegistryMount{Owner: Ownership{ScopeID: task.Owner.ScopeID, OperationID: task.Owner.OperationID, Role: NativePostgreSQLRegistryRole}, Target: "/fixture/cas"}
	owners := nativePostgreSQLStorageSourceOwners(task)
	if len(owners) != 5 || owners[0] != p.Stores[0].Owner || owners[4] != p.Registry.Owner {
		t.Fatal("original source owners substituted")
	}
}

func TestNativePostgreSQLStorageVolumeMappingKeepsOriginalCustody(t *testing.T) {
	for _, fault := range []string{"none", "foreign-label", "driver", "option", "path", "changed-after"} {
		t.Run(fault, func(t *testing.T) {
			owner := Ownership{ScopeID: strings.Repeat("a", 32), OperationID: strings.Repeat("b", 32), Role: "models"}
			gets := 0
			c, calls := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/v1.49/volumes/"+owner.VolumeName() {
					t.Error("mutation or wrong owner")
					w.WriteHeader(400)
					return
				}
				gets++
				labels := owner.labels()
				driver := "local"
				options := map[string]string{}
				mount := "/actual/docker/volumes/" + owner.VolumeName() + "/_data"
				switch fault {
				case "foreign-label":
					labels["io.projectone.operation"] = "foreign"
				case "driver":
					driver = "foreign"
				case "option":
					options["device"] = "/foreign"
				case "path":
					mount = "/coincident/foreign/_data"
				case "changed-after":
					if gets == 3 {
						labels["io.projectone.operation"] = "foreign"
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"Name": owner.VolumeName(), "Driver": driver, "Scope": "local", "Options": options, "Labels": labels, "Mountpoint": mount})
			})
			mount, e := c.nativePostgreSQLStorageVolume(context.Background(), owner, "/actual/docker/volumes")
			if fault == "none" {
				if e != nil || mount == "" || calls.Load() != 3 {
					t.Fatal("original readonly projection failed", e, calls.Load())
				}
			} else if e == nil {
				t.Fatal("changed/foreign storage mapped", fault)
			}
		})
	}
}
