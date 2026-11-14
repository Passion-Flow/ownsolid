package inference

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestRegistryImagePresenceClassifiesOnlyAll404AsMissing(t *testing.T) {
	for _, sample := range []struct {
		name     string
		want     string
		requests int
	}{
		{"missing", wire.InferenceImageMissing, 4},
		{"unavailable", "INFERENCE_IMAGE_UNAVAILABLE", 1},
		{"mixed", "INFERENCE_IMAGE_UNAVAILABLE", 2},
		{"transport", "INFERENCE_IMAGE_UNAVAILABLE", 1},
		{"invalid_json", "INFERENCE_IMAGE_UNAVAILABLE", 1},
		{"oversized", "INFERENCE_IMAGE_UNAVAILABLE", 1},
		{"wrong_identity", "INFERENCE_IMAGE_INVALID", 1},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var requests, mutations atomic.Int64
			// Explicit actual Unix HTTP contract double, not a Docker daemon/image.
			docker := testDocker(t, func(w http.ResponseWriter, r *http.Request) {
				count := requests.Add(1)
				if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, engineAPI+"/images/") {
					mutations.Add(1)
					w.WriteHeader(500)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch sample.name {
				case "missing":
					w.WriteHeader(404)
				case "unavailable":
					w.WriteHeader(500)
				case "mixed":
					if count == 1 {
						w.WriteHeader(404)
					} else {
						w.WriteHeader(500)
					}
				case "transport":
					connection, _, e := w.(http.Hijacker).Hijack()
					if e != nil {
						t.Error(e)
						return
					}
					connection.Close()
				case "invalid_json":
					w.Write([]byte(`{`))
				case "oversized":
					w.Write([]byte(strings.Repeat("x", 262145)))
				case "wrong_identity":
					json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:" + strings.Repeat("0", 64), "Architecture": "amd64", "Os": "linux"})
				}
			})
			_, e := docker.InspectApprovedImage(context.Background())
			if e == nil || e.Error() != sample.want || requests.Load() != int64(sample.requests) || mutations.Load() != 0 {
				t.Fatal("image absence classification changed", e, requests.Load(), mutations.Load())
			}
		})
	}
}
