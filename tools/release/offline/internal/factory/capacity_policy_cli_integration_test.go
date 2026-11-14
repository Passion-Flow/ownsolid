//go:build factory_capacity_cli_integration

package factory

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualFixedReserveNativeFactoryCLIOnSelectedMount(t *testing.T) {
	binary, sha := os.Getenv("PROJECT_ONE_TEST_OFFLINE_FACTORY"), os.Getenv("PROJECT_ONE_TEST_OFFLINE_FACTORY_SHA256")
	raw, e := os.ReadFile(binary)
	if e != nil || !filepath.IsAbs(binary) || !wire.ValidSHA256(sha) || wire.Digest(raw) != sha {
		t.Fatal("current source-bound factory binary", e)
	}
	c, _, _ := setup(t)
	// The owning runner admits and inventories this external TMPDIR separately.
	// This test never chooses a volume or falls back to a different filesystem.
	if c.MountRoot != os.Getenv("PROJECT_ONE_TEST_FACTORY_MOUNT_ROOT") {
		t.Fatal("selected actual mount required")
	}
	c.MinimumFreeBytes = 1 << 50
	root := filepath.Dir(c.ManifestFile)
	config, env := filepath.Join(root, "factory.json"), filepath.Join(root, "factory.env")
	put(t, config, Encode(c))
	put(t, env, []byte("PROJECT_ONE_OFFLINE_FACTORY_MINIMUM_FREE_BYTES=34359738368\n"))
	invoke := func(action string, useEnv bool) (Result, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		args := []string{action, "--config", config}
		if useEnv {
			args = append(args, "--env-file", env)
		}
		cmd := exec.CommandContext(ctx, binary, args...)
		out, err := cmd.CombinedOutput()
		if bytes.Contains(out, []byte("synthetic-private-key")) || bytes.Contains(out, []byte(root)) {
			t.Fatal("private factory references exposed")
		}
		var result Result
		if err == nil {
			err = json.Unmarshal(out, &result)
		}
		return result, err
	}
	if _, e = invoke("build", false); e == nil {
		t.Fatal("JSON fixed reserve ignored")
	}
	if _, e = os.Stat(c.OutputFile); !os.IsNotExist(e) {
		t.Fatal("refused native build published output", e)
	}
	built, e := invoke("build", true)
	if e != nil || !wire.ValidSHA256(built.ArchiveSHA256) || !wire.ValidSHA256(built.ManifestSHA256) {
		t.Fatal("actual fixed-reserve native build", e)
	}
	inspected, e := invoke("inspect", true)
	if e != nil || inspected.ArchiveSHA256 != built.ArchiveSHA256 || inspected.ManifestSHA256 != built.ManifestSHA256 {
		t.Fatal("independent native CLI all-byte inspect", e)
	}
	if _, e = invoke("build", true); e == nil {
		t.Fatal("fixed reserve overwrote published archive")
	}
}
