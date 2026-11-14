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
	"slices"
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
	client.guard = &recordingGuard{}
	return client
}

// recordingGuard stands in for the kernel firewall in tests of the Docker contract.
type recordingGuard struct {
	ensured, removed []string
	err              error
}

func (g *recordingGuard) Ensure(bridge string) error {
	g.ensured = append(g.ensured, bridge)
	return g.err
}

func (g *recordingGuard) Remove(bridge string) error {
	g.removed = append(g.removed, bridge)
	return g.err
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

func TestOwnedNetworkIsGuardedBeforeUseAndUnguardedAfterRemoval(t *testing.T) {
	node := "00000000-0000-0000-0000-000000000001"
	networkID := strings.Repeat("b", 64)
	container := strings.Repeat("c", 64)
	exists, attached := false, false
	client := testDocker(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == engineAPI+"/networks/create":
			exists = true
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"Id":%q}`, networkID)
		case r.Method == "DELETE" && r.URL.Path == engineAPI+"/networks/"+networkID:
			exists = false
			w.WriteHeader(204)
		case strings.HasPrefix(r.URL.Path, engineAPI+"/networks/"):
			if !exists {
				w.WriteHeader(404)
				w.Write([]byte(`{"message":"not found"}`))
				return
			}
			containers := map[string]json.RawMessage{}
			if attached {
				containers[container] = json.RawMessage(`{}`)
			}
			json.NewEncoder(w).Encode(Network{ID: networkID, Name: "projectone-inference-" + node, Driver: "bridge", Options: networkOptions(), Labels: map[string]string{"projectone.owner": "node-agent", "projectone.node_id": node}, Containers: containers})
		default:
			w.WriteHeader(500)
		}
	})
	guard := client.guard.(*recordingGuard)
	bridge := "br-" + strings.Repeat("b", 12)
	ctx := context.Background()

	guard.err = fail(egressGuardUnavailable)
	if id, e := client.EnsureNetwork(ctx, node); e == nil || e.Error() != egressGuardUnavailable || id != "" {
		t.Fatalf("network handed out without its guard: %q %v", id, e)
	}
	guard.err = nil
	if id, e := client.EnsureNetwork(ctx, node); e != nil || id != networkID {
		t.Fatal("existing owned network not guarded and reused", id, e)
	}
	attached = true
	if e := client.VerifyNetworkEndpoint(ctx, node, networkID, container); e != nil {
		t.Fatal(e)
	}
	if want := []string{bridge, bridge, bridge}; !slices.Equal(guard.ensured, want) {
		t.Fatalf("guard ensured for %v, want %v", guard.ensured, want)
	}
	guard.err = fail(egressGuardUnavailable)
	if e := client.VerifyNetworkEndpoint(ctx, node, networkID, container); e == nil || e.Error() != egressGuardUnavailable {
		t.Fatal("endpoint accepted without its guard", e)
	}
	guard.err = nil
	attached = false
	if e := client.RemoveUnusedNetwork(ctx, node, networkID); e != nil || exists {
		t.Fatal("unused network not removed", e)
	}
	if e := client.RemoveUnusedNetwork(ctx, node, networkID); e != nil {
		t.Fatal("retried removal failed", e)
	}
	if want := []string{bridge, bridge}; !slices.Equal(guard.removed, want) {
		t.Fatalf("guard removed for %v, want %v", guard.removed, want)
	}
}

func TestBridgeInterfaceIsDockersDerivedName(t *testing.T) {
	id := "0123456789ab" + strings.Repeat("f", 52)
	if name := bridgeInterface(id); name != "br-0123456789ab" || !bridgeName.MatchString(name) {
		t.Fatal(name)
	}
}
