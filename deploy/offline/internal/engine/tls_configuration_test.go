package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTLSGenerationRemovalRefusesOldDataAndForeignGenerationBeforeRequest(t *testing.T) {
	for _, role := range []string{"control-config", "operator-config", "postgres", "models", "datasets", "training", "preparations", TLSConfigurationRole, TLSConfigurationRole + "-" + strings.Repeat("c", 32)} {
		calls := 0
		c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
		if e := c.RemoveTLSGenerationVolume(context.Background(), Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), role}); e == nil || calls != 0 {
			t.Fatal("unsafe configuration removal admitted", role, e)
		}
	}
}
func TestTLSGenerationRemovalRequiresExactOwnerAndNoConsumers(t *testing.T) {
	for _, fault := range []string{"none", "foreign-consumer", "foreign-owner"} {
		t.Run(fault, func(t *testing.T) {
			owner := Ownership{strings.Repeat("a", 32), strings.Repeat("b", 32), TLSConfigurationRole + "-" + strings.Repeat("b", 32)}
			found, deletes := true, 0
			c, _ := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/volumes/"):
					if !found {
						w.WriteHeader(404)
						return
					}
					labels := owner.labels()
					if fault == "foreign-owner" {
						labels["io.projectone.operation"] = strings.Repeat("c", 32)
					}
					json.NewEncoder(w).Encode(Volume{Name: owner.VolumeName(), Driver: "local", Scope: "local", Labels: labels})
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/containers/json"):
					objects := []RuntimeObject{}
					if fault == "foreign-consumer" {
						objects = append(objects, RuntimeObject{ID: strings.Repeat("f", 64)})
					}
					json.NewEncoder(w).Encode(objects)
				case r.Method == http.MethodDelete:
					deletes++
					found = false
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Error("unexpected request")
				}
			})
			e := c.RemoveTLSGenerationVolume(context.Background(), owner)
			if fault == "none" {
				if e != nil || found || deletes != 1 {
					t.Fatal("exact generated removal", e)
				}
			} else if e == nil || !found || deletes != 0 {
				t.Fatal("foreign object/consumer removed", e)
			}
		})
	}
}
