package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBackupVolumeRemovalCannotDeleteInstallationDataOrForeignConsumer(t *testing.T) {
	for _, role := range []string{"models", "datasets", "training", "preparations", "postgres", "operator-config", "control-config", "backup-models", "backup-models-foreign"} {
		requests := 0
		c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) })
		owner := Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), role}
		if e := c.RemoveBackupVolume(context.Background(), owner); e == nil || requests != 0 {
			t.Fatal("source data crossed removal boundary", role, e)
		}
	}
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "exclusive", true: "foreign-consumer"}[foreign], func(t *testing.T) {
			owner := Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), "backup-models-" + strings.Repeat("b", 32)}
			found := true
			deletes := 0
			c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/volumes/"):
					if !found {
						w.WriteHeader(404)
						return
					}
					json.NewEncoder(w).Encode(Volume{Name: owner.VolumeName(), Driver: "local", Scope: "local", Labels: owner.labels()})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/json"):
					if foreign {
						json.NewEncoder(w).Encode([]RuntimeObject{{ID: strings.Repeat("f", 64)}})
					} else {
						json.NewEncoder(w).Encode([]RuntimeObject{})
					}
				case r.Method == http.MethodDelete:
					deletes++
					found = false
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Error("unexpected request")
				}
			})
			e := c.RemoveBackupVolume(context.Background(), owner)
			if foreign {
				if e == nil || deletes != 0 || !found {
					t.Fatal("foreign volume consumer lost", e)
				}
			} else if e != nil || deletes != 1 || found {
				t.Fatal("exact removal failed", e)
			}
		})
	}
}
