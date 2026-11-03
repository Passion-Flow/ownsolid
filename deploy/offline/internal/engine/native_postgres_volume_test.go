package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNativePostgreSQLArchiveCleanupRejectsLiveOrForeignCatalog(t *testing.T) {
	var c *Client
	for _, role := range []string{"models", "datasets", "training", "preparations", NativePostgreSQLRegistryRole, "operator-config", NativePostgreSQLPreparedRole} {
		o := Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), role}
		if e := c.RemoveNativePostgreSQLVolume(context.Background(), o); e == nil {
			t.Fatal("live/unbound volume accepted for removal")
		}
	}
}

func TestNativePostgreSQLArchiveCleanupRequiresExactOwnerAndNoConsumers(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "foreign-consumer"}[foreign], func(t *testing.T) {
			o := Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), NativePostgreSQLArtifactRole + "-" + strings.Repeat("b", 32)}
			var removed atomic.Bool
			var deletes atomic.Int32
			c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				write := func(v any) { json.NewEncoder(w).Encode(v) }
				switch {
				case r.Method == "GET" && strings.Contains(r.URL.Path, "/volumes/"):
					if removed.Load() {
						w.WriteHeader(404)
						return
					}
					write(Volume{Name: o.VolumeName(), Driver: "local", Scope: "local", Labels: o.labels()})
				case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/containers/json"):
					if foreign {
						write([]RuntimeObject{{ID: strings.Repeat("f", 64)}})
					} else {
						write([]RuntimeObject{})
					}
				case r.Method == "DELETE":
					if len(r.URL.Query()) != 0 {
						t.Error("forced volume removal")
					}
					deletes.Add(1)
					removed.Store(true)
					w.WriteHeader(204)
				default:
					t.Error("unexpected cleanup route")
					w.WriteHeader(500)
				}
			})
			e := c.RemoveNativePostgreSQLVolume(context.Background(), o)
			if foreign {
				if e == nil || deletes.Load() != 0 {
					t.Fatal("foreign consumers removed")
				}
			} else if e != nil || deletes.Load() != 1 || !removed.Load() {
				t.Fatal("exact disposable volume not removed", e)
			}
		})
	}
}
