package config

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/registryconfig"
)

// All inputs are disposable synthetic configuration. No SQL, provider, live
// credential, mounted Registry or runtime service is opened by these controls.
func probePrivateConfiguration(t *testing.T, raw []byte) string {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private probe configuration root")
	}
	path := filepath.Join(root, "synthetic-configuration")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal("private probe configuration fixture")
	}
	return path
}

type probeUnsafeFileCase struct {
	name   string
	change func(*testing.T, string) string
}

func probeUnsafeFileCases(maximum int64) []probeUnsafeFileCase {
	return []probeUnsafeFileCase{
		{"relative", func(_ *testing.T, _ string) string { return "synthetic-sensitive-relative" }},
		{"unclean", func(_ *testing.T, path string) string {
			return filepath.Dir(path) + "/../synthetic-sensitive-configuration"
		}},
		{"missing", func(_ *testing.T, path string) string { return path + "-missing" }},
		{"directory", func(_ *testing.T, path string) string { return filepath.Dir(path) }},
		{"empty", func(t *testing.T, path string) string {
			if os.WriteFile(path, nil, 0600) != nil {
				t.Fatal("empty fixture")
			}
			return path
		}},
		{"oversize", func(t *testing.T, path string) string {
			if os.WriteFile(path, bytes.Repeat([]byte(" "), int(maximum)+1), 0600) != nil {
				t.Fatal("oversized fixture")
			}
			return path
		}},
		{"public_file", func(t *testing.T, path string) string {
			if os.Chmod(path, 0644) != nil {
				t.Fatal("public file fixture")
			}
			return path
		}},
		{"read_only_mode", func(t *testing.T, path string) string {
			if os.Chmod(path, 0400) != nil {
				t.Fatal("read-only mode fixture")
			}
			return path
		}},
		{"public_parent", func(t *testing.T, path string) string {
			parent := filepath.Dir(path)
			if os.Chmod(parent, 0755) != nil {
				t.Fatal("public parent fixture")
			}
			t.Cleanup(func() {
				if os.Chmod(parent, 0700) != nil {
					t.Error("restore owned parent mode")
				}
			})
			return path
		}},
		{"symlink_file", func(t *testing.T, path string) string {
			link := path + "-symlink"
			if os.Symlink(path, link) != nil {
				t.Fatal("file symlink fixture")
			}
			return link
		}},
		{"symlink_parent", func(t *testing.T, path string) string {
			parent := filepath.Dir(path)
			link := filepath.Join(parent, "parent-link")
			if os.Symlink(parent, link) != nil {
				t.Fatal("parent symlink fixture")
			}
			return filepath.Join(link, filepath.Base(path))
		}},
		{"hardlink", func(t *testing.T, path string) string {
			if os.Link(path, path+"-hardlink") != nil {
				t.Fatal("hardlink fixture")
			}
			return path
		}},
		{"fifo", func(t *testing.T, path string) string {
			fifo := path + "-fifo"
			if syscall.Mkfifo(fifo, 0600) != nil {
				t.Fatal("FIFO fixture")
			}
			return fifo
		}},
	}
}

func TestProbeConfigurationProtectedSnapshotAndPolicyMapping(t *testing.T) {
	base := validConfig()
	base.ModelCache = DefaultModelCacheConfiguration()
	base.ModelCache.Enabled = true
	base.ModelCache.Address, base.ModelCache.Username = "cache.example.invalid:16379", "synthetic"
	base.ModelCache.PasswordFile = "/synthetic-unavailable-probe/password"
	policy := DefaultRuntimePolicy()
	policy.Workspaces.MaximumCount = 7
	base.Runtime = &policy
	raw, e := yaml.Marshal(base)
	if e != nil {
		t.Fatal("synthetic configuration encoding")
	}
	t.Run("valid_snapshot", func(t *testing.T) {
		path := probePrivateConfiguration(t, raw)
		actual, e := LoadProtectedWithEnvironment(path, "")
		if e != nil || !actual.ModelCache.Enabled || !actual.ModelCache.TLS || actual.Policy().Workspaces.MaximumCount != 7 || actual.Server.PortalOrigin != base.Server.PortalOrigin {
			t.Fatal("protected typed snapshot changed or opened unavailable runtime credentials", e)
		}
	})
	t.Run("nondefault_and_false_environment", func(t *testing.T) {
		path := probePrivateConfiguration(t, raw)
		t.Setenv(workspaceCountEnvironment, "9999")
		environment := workspaceEnvironmentFixture(t, workspaceCountEnvironment+"=9\nPROJECT_ONE_MODEL_CACHE_ENABLED=false\nPROJECT_ONE_MODEL_CACHE_TLS=false\n")
		actual, e := LoadProtectedWithEnvironment(path, environment)
		if e != nil || actual.Policy().Workspaces.MaximumCount != 9 || actual.ModelCache.Enabled || actual.ModelCache.TLS || actual.ModelCache.PasswordFile != base.ModelCache.PasswordFile {
			t.Fatal("explicit typed nondefault/false override or no-ambient authority changed", e)
		}
	})
	for _, test := range probeUnsafeFileCases(MaximumProtectedConfigurationBytes) {
		t.Run(test.name, func(t *testing.T) {
			path := test.change(t, probePrivateConfiguration(t, raw))
			started := time.Now()
			if _, e := LoadProtectedWithEnvironment(path, ""); e == nil || e.Error() != "CONFIG_PRIVATE_SNAPSHOT_REQUIRED" || strings.Contains(e.Error(), path) {
				t.Fatal("unsafe main snapshot passed or leaked its reference", e)
			}
			if test.name == "fifo" && time.Since(started) > time.Second {
				t.Fatal("FIFO refusal was not prompt")
			}
		})
	}
	t.Run("environment_fifo", func(t *testing.T) {
		path := probePrivateConfiguration(t, raw)
		fifo := path + "-policy-fifo"
		if syscall.Mkfifo(fifo, 0600) != nil {
			t.Fatal("environment FIFO fixture")
		}
		if _, e := LoadProtectedWithEnvironment(path, fifo); e == nil || e.Error() != "CONFIG_POLICY_PRIVATE_FILE_INVALID" {
			t.Fatal("unsafe reachable policy file passed", e)
		}
	})
	t.Run("invalid_environment", func(t *testing.T) {
		path := probePrivateConfiguration(t, raw)
		for _, body := range []string{"PROJECT_ONE_MODEL_CACHE_ENABLED=truthy\n", workspaceCountEnvironment + "=9\n" + workspaceCountEnvironment + "=10\n", "UNKNOWN_PROBE_POLICY=synthetic-sensitive-value\n"} {
			if _, e := LoadProtectedWithEnvironment(path, workspaceEnvironmentFixture(t, body)); e == nil || strings.Contains(e.Error(), "synthetic-sensitive-value") {
				t.Fatal("invalid policy passed or disclosed input", e)
			}
		}
	})
	t.Run("invalid_yaml_before_override", func(t *testing.T) {
		path := probePrivateConfiguration(t, append(append([]byte{}, raw...), []byte("unowned_probe_field: synthetic-sensitive-value\n")...))
		if _, e := LoadProtectedWithEnvironment(path, workspaceEnvironmentFixture(t, workspaceCountEnvironment+"=9\n")); e == nil || strings.Contains(e.Error(), "synthetic-sensitive-value") {
			t.Fatal("invalid YAML concealed or disclosed", e)
		}
	})
}

func TestProbeRegistryReferenceUsesPinnedPrivateSnapshot(t *testing.T) {
	t.Run("valid_nondefault_false_snapshot", func(t *testing.T) {
		registry, expected := registryConfigurationFixture(t)
		main := probePrivateConfiguration(t, registryParentYAML(t, registry))
		actual, e := LoadProtectedWithEnvironment(main, "")
		if e != nil || actual.ImageRegistry == nil || actual.ImageRegistry.OperationSeconds != expected.OperationSeconds || actual.ImageRegistry.RequestSeconds != 31 || actual.ImageRegistry.MaximumConcurrentImports != 2 || actual.ImageRegistry.Verification.AllowTestFixture {
			t.Fatal("protected Registry snapshot lost nondefault/false policy", e)
		}
	})
	t.Run("explicit_selected_reference", func(t *testing.T) {
		first, _ := registryConfigurationFixture(t)
		second, next := registryConfigurationFixture(t)
		next.RequestSeconds = 32
		writeRegistryConfiguration(t, second, next)
		main := probePrivateConfiguration(t, registryParentYAML(t, first))
		actual, e := LoadProtectedWithEnvironment(main, workspaceEnvironmentFixture(t, registryConfigEnvironment+"="+second+"\n"))
		if e != nil || actual.ImageRegistryConfigFile != second || actual.ImageRegistry == nil || actual.ImageRegistry.RequestSeconds != 32 {
			t.Fatal("selected protected reference override changed", e)
		}
	})
	for _, test := range probeUnsafeFileCases(registryconfig.MaximumConfigurationBytes) {
		t.Run(test.name, func(t *testing.T) {
			path, _ := registryConfigurationFixture(t)
			path = test.change(t, path)
			// Invalid path spellings can fail at YAML admission; all safe spellings
			// with unsafe file types must fail at the reachable private reader.
			main := probePrivateConfiguration(t, registryParentYAML(t, path))
			started := time.Now()
			if _, e := LoadProtectedWithEnvironment(main, ""); e == nil || e.Error() != "CONFIG_IMAGE_REGISTRY_INVALID" || strings.Contains(e.Error(), "sensitive-private-marker") || strings.Contains(e.Error(), path) {
				t.Fatal("unsafe Registry snapshot passed or disclosed reference", e)
			}
			if test.name == "fifo" && time.Since(started) > time.Second {
				t.Fatal("reachable Registry FIFO refusal was not prompt")
			}
		})
	}
	t.Run("invalid_private_body", func(t *testing.T) {
		path, value := registryConfigurationFixture(t)
		good, e := json.Marshal(value)
		if e != nil {
			t.Fatal("synthetic Registry encoding")
		}
		for _, raw := range [][]byte{append([]byte(`{"unknown_sensitive":"synthetic-sensitive-value",`), good[1:]...), append(append([]byte{}, good...), []byte(` {}`)...), bytes.Replace(good, []byte(`"request_seconds":31`), []byte(`"request_seconds":null`), 1)} {
			if os.WriteFile(path, raw, 0600) != nil {
				t.Fatal("invalid Registry body fixture")
			}
			main := probePrivateConfiguration(t, registryParentYAML(t, path))
			if _, e := LoadProtectedWithEnvironment(main, ""); e == nil || e.Error() != "CONFIG_IMAGE_REGISTRY_INVALID" || strings.Contains(e.Error(), "synthetic-sensitive-value") {
				t.Fatal("invalid Registry JSON passed or leaked", e)
			}
		}
	})
}

func TestProbePrivateSnapshotRejectsSameSizeRewriteWithRestoredMtime(t *testing.T) {
	path := probePrivateConfiguration(t, []byte(`{"value":11}`))
	file, e := os.OpenFile(path, os.O_RDWR, 0)
	if e != nil {
		t.Fatal("owned mutation fixture open")
	}
	defer file.Close()
	before, e := file.Stat()
	if e != nil || !unchangedPrivateFile(before, before) {
		t.Fatal("unchanged actual private snapshot refused")
	}
	// Move past the native metadata clock tick, then mutate the held inode.
	// No concurrent writer, timing hook or synthetic stat object is involved.
	time.Sleep(10 * time.Millisecond)
	if _, e = file.WriteAt([]byte(`{"value":22}`), 0); e != nil || file.Sync() != nil {
		t.Fatal("owned same-size rewrite")
	}
	if e = os.Chtimes(path, before.ModTime(), before.ModTime()); e != nil {
		t.Fatal("owned mtime restoration")
	}
	after, e := file.Stat()
	current, pe := os.Lstat(path)
	if e != nil || pe != nil {
		t.Fatal("actual rewritten file observation")
	}
	for _, observed := range []os.FileInfo{after, current} {
		// This is the exact old guard. The actual mutation would pass it.
		if !privateFileOwned(observed, false) || !os.SameFile(before, observed) || before.Size() != observed.Size() || !before.ModTime().Equal(observed.ModTime()) {
			t.Fatal("fixture did not reproduce the old same-inode/size/mtime acceptance")
		}
		if samePrivateChangeTime(before, observed) || unchangedPrivateFile(before, observed) {
			t.Fatal("restored mtime concealed a real file rewrite")
		}
	}
	if unchangedPrivateFile(nil, after) || unchangedPrivateFile(before, nil) {
		t.Fatal("missing snapshot metadata accepted")
	}
	if _, e = readPrivateFile(path, MaximumProtectedConfigurationBytes, "SYNTHETIC_PRIVATE_INPUT_INVALID"); e != nil {
		t.Fatal("fresh stable private snapshot refused after explicit mutation completed", e)
	}
}

func TestProbeParentDirectoryOpenRefusesPostcheckFIFO(t *testing.T) {
	const childInput = "PROJECT_ONE_TEST_PROBE_PARENT_FIFO_CHILD"
	const fixtureMarker = "projectone-owned-parent-fifo-fixture-v1\n"
	const fixtureName = "checked-private-parent"
	if parent := os.Getenv(childInput); parent != "" {
		// Refuse a child-mode environment pointing at arbitrary existing data.
		// The parent owns the marker and every mutated entry in one temp root.
		marker, e := readPrivateFile(filepath.Join(filepath.Dir(parent), "parent-fifo-fixture"), int64(len(fixtureMarker)), "SYNTHETIC_PARENT_FIXTURE_INVALID")
		if e != nil || !bytes.Equal(marker, []byte(fixtureMarker)) || filepath.Base(parent) != fixtureName {
			t.Fatal("exclusive disposable parent fixture required")
		}
		// Observe the actual owned directory first, just as the production
		// readers do. Replace it before the same directory-open boundary,
		// without a mutable production hook or a race-dependent timing loop.
		before, e := os.Lstat(parent)
		if e != nil || !privateFileOwned(before, true) {
			t.Fatal("owned checked directory fixture")
		}
		if e = os.Rename(parent, parent+"-original"); e != nil {
			t.Fatal("owned directory replacement")
		}
		if e = syscall.Mkfifo(parent, 0600); e != nil {
			t.Fatal("owned replacement FIFO")
		}
		started := time.Now()
		opened, e := openPrivateDirectory(parent)
		if opened != nil {
			_ = opened.Close()
			t.Fatal("postcheck FIFO unexpectedly produced a directory root")
		}
		if !errors.Is(e, syscall.ENOTDIR) || time.Since(started) > time.Second {
			t.Fatal("postcheck FIFO did not promptly fail kernel directory traversal", e)
		}
		return
	}
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(base, 0700) != nil {
		t.Fatal("canonical owned parent fixture")
	}
	if e = os.WriteFile(filepath.Join(base, "parent-fifo-fixture"), []byte(fixtureMarker), 0600); e != nil {
		t.Fatal("owned parent fixture marker")
	}
	parent := filepath.Join(base, fixtureName)
	if e = os.Mkdir(parent, 0700); e != nil {
		t.Fatal("private parent fixture")
	}
	// A stable actual directory remains usable. Close it before the child
	// replaces only inventoried paths under this one disposable temp root.
	stable, e := openPrivateDirectory(parent)
	if e != nil {
		t.Fatal("stable directory open", e)
	}
	info, statError := stable.Stat(".")
	closeError := stable.Close()
	if statError != nil || closeError != nil || !privateFileOwned(info, true) {
		t.Fatal("stable directory identity or close")
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal("owned test executable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, executable, "-test.run=^TestProbeParentDirectoryOpenRefusesPostcheckFIFO$", "-test.timeout=2s")
	child.Env = append(os.Environ(), childInput+"="+parent)
	output, runError := child.CombinedOutput()
	if ctx.Err() != nil || runError != nil {
		t.Fatalf("postcheck parent FIFO refusal failed or hung; child joined: %v, %s", runError, output)
	}
}
