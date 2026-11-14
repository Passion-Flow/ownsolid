//go:build linux && model_lifecycle_container_integration

package modelpreparations

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

func TestActualLinuxProtectedManagerAndPrivateWorkerInputOwnership(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("PROJECT_ONE_PREPARATION_CONTAINER_FIXTURE") != "isolated-test-only" {
		t.Fatal("explicit isolated Linux root scope required")
	}
	root, e := os.MkdirTemp("/verification", "ownership-")
	if e != nil {
		t.Fatal(e)
	}
	config := fixtureConfig()
	config.RootDirectory = filepath.Join(root, "preparation")
	state := filepath.Join(root, "state")
	for _, path := range []string{config.RootDirectory, state} {
		if e = os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	config.DockerSocket = "/run/docker.sock"
	config.ImageReference = os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_REFERENCE")
	config.ImageID = os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_ID")
	config.WorkerSourceSHA256 = os.Getenv("PROJECT_ONE_PREPARATION_WORKER_SOURCE")
	manager, e := New(config, state, settings.Default())
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	if _, e = manager.Lifecycle.Engine.InspectImage(context.Background(), config); e != nil {
		t.Fatal("actual manufactured image identity", e)
	}
	command := fixtureCommand()
	command.Recipe.ImageReference = config.ImageReference
	command.Recipe.Identity.ImageID = &config.ImageID
	command.Recipe.Identity.WorkerSourceSHA256 = config.WorkerSourceSHA256
	// Synthetic bounded source bytes exercise filesystem handoff only, not dataset/model computation.
	raw := []byte("synthetic private input")
	command.Inputs.Heldout.SizeBytes = int64(len(raw))
	command.Inputs.Heldout.SHA256 = digest(raw)
	for i := range command.Inputs.AdapterFiles {
		command.Inputs.AdapterFiles[i].SizeBytes = int64(len(raw))
		command.Inputs.AdapterFiles[i].SHA256 = digest(raw)
	}
	j, e := manager.Lifecycle.Store.Begin(fixtureID, fixtureID, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", command)
	if e != nil {
		t.Fatal(e)
	}
	download := func(_ context.Context, _ string, _ datawire.Asset, path string) error {
		return os.WriteFile(path, raw, 0600)
	}
	if e = prepareAttempt(context.Background(), config, j, download); e != nil {
		t.Fatal(e)
	}
	for _, relative := range []string{"", "inputs", "inputs/adapter", "inputs/heldout.jsonl", "inputs/adapter/README.md", "request.json", "projectone-preparation.json", "output"} {
		info, e := os.Lstat(filepath.Join(attemptPath(config, j), relative))
		if e != nil {
			t.Fatal(e)
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 10001 || owner.Gid != 10001 || info.Mode().Perm()&0077 != 0 {
			t.Fatal("worker UID/private permission handoff", relative)
		}
	}
	t.Log("Actual Linux amd64 root constructor, manufactured image identity, scoped private inputs and UID/GID10001 handoff. Synthetic inputs; no worker/GPU launch.")
}
