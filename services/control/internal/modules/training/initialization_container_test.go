//go:build linux && training_container_integration

package training_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

// This test runs only inside an isolated owned container, never by changing host account state.
func TestTrainingContainerRootInitializationForDifferentRuntimeUID(t *testing.T) {
	if os.Getenv("PROJECT_ONE_INITIALIZATION_CONTAINER") != "isolated-test-only" || os.Geteuid() != 0 {
		t.Fatal("isolated root container required")
	}
	parent, e := os.MkdirTemp("", "projectone-initialization-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(parent) })
	if e := os.Chmod(parent, 0755); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(parent, "training")
	if e := os.Mkdir(root, 0700); e != nil {
		t.Fatal(e)
	}
	tenant := uuid.NewString()
	if e := training.InitializeStore(root, tenant, 10001, 10001); e != nil {
		t.Fatal(e)
	}
	id, e := training.VerifyInitializedStore(root, tenant, 10001, 10001)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = training.OpenStore(root, tenant, 4096, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles); e == nil {
		t.Fatal("root opened another runtime's store")
	}
	program, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	child := exec.Command(program, "-test.run=^TestTrainingContainerNonrootStoreHelper$", "-test.v")
	child.Env = append(os.Environ(), "PROJECT_ONE_INITIALIZED_ROOT="+root, "PROJECT_ONE_INITIALIZED_TENANT="+tenant, "PROJECT_ONE_INITIALIZED_ID="+id)
	child.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 10001, Gid: 10001, Groups: []uint32{}}}
	raw, e := child.CombinedOutput()
	if e != nil {
		t.Fatalf("runtime child: %v %s", e, raw)
	}
	t.Log("Actual isolated Linux root initialization, expected owner verification and nonroot runtime open/restart/private lock passed; no host account modification.")
}

func TestTrainingContainerNonrootStoreHelper(t *testing.T) {
	if os.Getenv("PROJECT_ONE_INITIALIZED_ROOT") == "" {
		t.Fatal("only an explicit nonroot child may run this helper")
	}
	if os.Geteuid() != 10001 || os.Getegid() != 10001 {
		t.Fatal("runtime identity mismatch")
	}
	root, tenant, id := os.Getenv("PROJECT_ONE_INITIALIZED_ROOT"), os.Getenv("PROJECT_ONE_INITIALIZED_TENANT"), os.Getenv("PROJECT_ONE_INITIALIZED_ID")
	for i := 0; i < 2; i++ {
		store, e := training.OpenStore(root, tenant, 4096, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles)
		if e != nil {
			t.Fatal(e)
		}
		if store.Namespace() != id {
			t.Fatal("namespace changed")
		}
		if _, e = training.OpenStore(root, tenant, 4096, wire.MaximumCheckpointBytes, wire.MaximumCheckpointFiles); e == nil {
			t.Fatal("store lock bypass")
		}
		if e = store.Close(); e != nil {
			t.Fatal(e)
		}
	}
}
