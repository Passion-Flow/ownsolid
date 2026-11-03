package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// The Unix HTTP response source below is a contract test double, never a production engine.
func testDocker(t *testing.T, handler http.HandlerFunc) *Docker {
	t.Helper()
	directory, e := os.MkdirTemp(os.Getenv("TMPDIR"), "i-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	socket := filepath.Join(directory, "d.sock")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	client, e := NewDocker(socket)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	return client
}
func TestFixedDockerContractRejectsUnknownNetworksImagesAndOversizeResponses(t *testing.T) {
	node := "00000000-0000-0000-0000-000000000001"
	networkID := strings.Repeat("a", 64)
	created := false
	unowned := false
	mutations := 0
	client := testDocker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, engineAPI+"/images/"):
			recipe := wire.ApprovedInferenceRecipe()
			json.NewEncoder(w).Encode(map[string]any{"Id": recipe.ImageConfigDigest, "Architecture": "amd64", "Os": "linux", "Config": map[string]any{"Env": []string{"CUDA_VERSION=" + recipe.CUDAVersion}}})
		case r.URL.Path == engineAPI+"/networks/create":
			var input struct {
				Internal bool
				Options  map[string]string
				Name     string
				Driver   string
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Internal || !maps.Equal(input.Options, networkOptions()) || input.Driver != "bridge" || input.Name != "projectone-inference-"+node {
				t.Error("unapproved network specification")
			}
			created = true
			mutations++
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"Id":%q}`, networkID)
		case strings.HasPrefix(r.URL.Path, engineAPI+"/networks/"):
			if !created {
				w.WriteHeader(404)
				w.Write([]byte(`{"message":"not found"}`))
				return
			}
			owner := "node-agent"
			if unowned {
				owner = "unowned"
			}
			json.NewEncoder(w).Encode(Network{ID: networkID, Name: "projectone-inference-" + node, Driver: "bridge", Internal: false, Options: networkOptions(), Labels: map[string]string{"projectone.owner": owner, "projectone.node_id": node}, Containers: map[string]json.RawMessage{}})
		default:
			w.WriteHeader(500)
		}
	})
	if _, e := client.InspectApprovedImage(context.Background()); e != nil {
		t.Fatal(e)
	}
	if value, e := client.EnsureNetwork(context.Background(), node); e != nil || value != networkID {
		t.Fatal(e)
	}
	unowned = true
	if _, e := client.EnsureNetwork(context.Background(), node); e == nil || e.Error() != "INFERENCE_NETWORK_CONFLICT" || mutations != 1 {
		t.Fatal("unowned network adopted or changed", e)
	}
	oversized := testDocker(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(strings.Repeat("X", 262145)))
	})
	if _, e := oversized.InspectApprovedImage(context.Background()); e == nil {
		t.Fatal("unbounded image metadata accepted")
	}
}

func TestDockerStopAllowsTheActualFifteenSecondGraceWindow(t *testing.T) {
	client := testDocker(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != engineAPI+"/containers/"+strings.Repeat("a", 64)+"/stop" || r.URL.Query().Get("t") != "15" {
			t.Error("unbounded stop endpoint")
			w.WriteHeader(400)
			return
		}
		timer := time.NewTimer(16 * time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			w.WriteHeader(204)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if e := client.StopContainer(ctx, strings.Repeat("a", 64)); e != nil {
		t.Fatal("client header deadline cut off the owned stop grace window", e)
	}
}

func TestNetworkOptionsAllowOnlyKnownIPv4DefaultAndLoopbackIsolation(t *testing.T) {
	for _, option := range []string{"com.docker.network.bridge.host_binding_ipv4", "com.docker.network.bridge.enable_icc", "com.docker.network.enable_ipv4", "unknown"} {
		altered := networkOptions()
		altered[option] = "unsafe"
		if approvedNetworkOptions(altered) {
			t.Fatal("changed network option adopted", option)
		}
	}
	current := networkOptions()
	current["com.docker.network.enable_ipv4"] = "true"
	if !approvedNetworkOptions(current) || !approvedNetworkOptions(networkOptions()) {
		t.Fatal("versioned Engine IPv4 defaults rejected")
	}
}
