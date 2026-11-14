package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func kubernetesLease(operation string) wire.Lease {
	command := wire.KubernetesCommand{ClusterID: "6f1c2a8e-4b3d-4c5e-9f7a-1b2c3d4e5f60", Version: wire.ApprovedKubernetesRelease().Version, Role: wire.KubernetesServerRole, NodeName: "0d9e8f7a-6b5c-4d3e-8f2a-1b0c9d8e7f6a", NodeAddress: "10.40.0.1"}
	if operation == wire.KubernetesTokenOperation {
		command.TokenTTLSeconds = 3600
	}
	return wire.Lease{TaskID: "1d9e8f7a-6b5c-4d3e-8f2a-1b0c9d8e7f6a", Operation: operation, LeaseToken: strings.Repeat("A", 43), Attempt: 1, Kubernetes: &command}
}

func TestKubernetesLeasesRunOnlyOnKubernetesNodes(t *testing.T) {
	for _, operation := range []string{wire.KubernetesJoinOperation, wire.KubernetesTokenOperation, wire.KubernetesObserveOperation} {
		lease := kubernetesLease(operation)
		for _, profile := range []string{wire.ReadOnlyProfile, wire.SystemdComponentsProfile, wire.SystemdMaintenanceProfile, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile, wire.KubernetesNodeProfile} {
			if e := validateExecutionLease(profile, lease); (e == nil) != (profile == wire.KubernetesNodeProfile) {
				t.Errorf("%s on %s: %v", operation, profile, e)
			}
		}
		missing := lease
		missing.Kubernetes = nil
		if validateExecutionLease(wire.KubernetesNodeProfile, missing) == nil {
			t.Error("a Kubernetes lease without its command was accepted")
		}
		mixed := lease
		mixed.Component = &wire.ComponentCommand{}
		if validateExecutionLease(wire.KubernetesNodeProfile, mixed) == nil {
			t.Error("a Kubernetes lease carrying a component command was accepted")
		}
	}
	join := kubernetesLease(wire.KubernetesJoinOperation)
	join.Kubernetes.TokenTTLSeconds = 3600
	if validateExecutionLease(wire.KubernetesNodeProfile, join) == nil {
		t.Error("a join shaped like a token request was accepted")
	}
	collect := wire.Lease{TaskID: join.TaskID, Operation: wire.CollectFacts, LeaseToken: join.LeaseToken, Attempt: 1, Kubernetes: kubernetesLease(wire.KubernetesObserveOperation).Kubernetes}
	if validateExecutionLease(wire.KubernetesNodeProfile, collect) == nil {
		t.Error("discovery smuggled a Kubernetes command")
	}
}

func TestKubernetesConfigurationBelongsToItsProfile(t *testing.T) {
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(root, 0o700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "agent.json")
	load := func(input map[string]any) error {
		raw, e := json.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, raw, 0o600); e != nil {
			t.Fatal(e)
		}
		_, e = LoadConfig(path)
		return e
	}
	base := func(profile string, kubernetes map[string]any) map[string]any {
		input := map[string]any{"control_origin": "https://admin.example.invalid", "state_directory": root, "disk_path": root, "execution_profile": profile}
		if kubernetes != nil {
			input["kubernetes"] = kubernetes
		}
		return input
	}
	if e := load(base(wire.KubernetesNodeProfile, nil)); e != nil {
		t.Fatal("defaults refused", e)
	}
	if e := load(base(wire.KubernetesNodeProfile, map[string]any{"ready_seconds": 300, "task_seconds": 900})); e != nil {
		t.Fatal(e)
	}
	for name, input := range map[string]map[string]any{
		"another profile with a Kubernetes section": base(wire.ReadOnlyProfile, map[string]any{"ready_seconds": 300}),
		"a task shorter than its readiness wait":    base(wire.KubernetesNodeProfile, map[string]any{"ready_seconds": 600, "task_seconds": 600}),
		"an unbounded task":                         base(wire.KubernetesNodeProfile, map[string]any{"task_seconds": 7201}),
		"an unknown field":                          base(wire.KubernetesNodeProfile, map[string]any{"kubeconfig": "/etc/x"}),
	} {
		if load(input) == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
