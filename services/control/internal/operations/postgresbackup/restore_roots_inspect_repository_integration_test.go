//go:build repository_integration && postgres_restore_roots_inspect_integration

package postgresbackup

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type rootsInspectionFixture struct {
	configuration, request string
	runtime                config.Config
	input                  release.PostgreSQLRestoreRootsInspectionRequest
}

func newRootsInspectionFixture(t *testing.T) rootsInspectionFixture {
	t.Helper()
	f := newRestoreInputFixture(t)
	profile := release.MVPPostgreSQLCPUApplicationProfile
	f.runtime.Server.ApplicationProfile = &profile
	raw, e := yaml.Marshal(f.runtime)
	if e != nil || os.WriteFile(f.request.RuntimeConfigurationFile, raw, 0600) != nil {
		t.Fatal("typed runtime fixture")
	}
	r := release.PostgreSQLRestoreRootsInspectionRequest{FormatVersion: 1, ApplicationProfile: profile, InspectionID: strings.Repeat("a", 32), TargetScopeID: strings.Repeat("b", 32), Binding: release.PostgreSQLRestoreTargetInitializationBinding{RestoreAttemptID: strings.Repeat("c", 32), ReservationPlanSHA256: strings.Repeat("d", 64), TargetDescriptorSHA256: strings.Repeat("e", 64), TargetRuntimeSHA256: strings.Repeat("f", 64), SourceCandidateSHA256: strings.Repeat("1", 64), NetworkID: strings.Repeat("2", 64), PostgresContainerID: strings.Repeat("3", 64)}, RuntimeConfigurationRoot: f.request.ConfigurationRoot, RuntimeConfigurationSHA256: digest(raw), ExpectedUID: uint32(os.Geteuid()), ExpectedGID: uint32(os.Getegid()), Policy: release.PostgreSQLRestoreRootsInspectionPolicy{OperationSeconds: 30, MaximumRecordBytes: 65536}}
	request := filepath.Join(filepath.Dir(f.operatorPath), "roots-inspection-request.json")
	writeRootsInspectionJSON(t, request, r)
	// The operation must not open this credential reference or contact SQL.
	if os.Remove(f.runtime.Database.DSNFile) != nil {
		t.Fatal("remove unused synthetic DSN")
	}
	return rootsInspectionFixture{f.request.RuntimeConfigurationFile, request, f.runtime, r}
}

func writeRootsInspectionJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, e := json.Marshal(value)
	if e != nil || os.WriteFile(path, append(raw, '\n'), 0600) != nil {
		t.Fatal("private fixture document")
	}
}

func updateRootsInspectionRuntime(t *testing.T, f *rootsInspectionFixture) {
	t.Helper()
	raw, e := yaml.Marshal(f.runtime)
	if e != nil || os.WriteFile(f.configuration, raw, 0600) != nil {
		t.Fatal("typed runtime update")
	}
	f.input.RuntimeConfigurationSHA256 = digest(raw)
	writeRootsInspectionJSON(t, f.request, f.input)
}

func runRootsInspectionFixture(f rootsInspectionFixture) (release.PostgreSQLRestoreRootsInspectionObservation, error) {
	return InspectRestoreRoots(context.Background(), f.configuration, "", f.request, "", f.input)
}

type rootsInspectionCloseFault struct{ *freshRoots }

func (r rootsInspectionCloseFault) Close() error {
	// A real closed FD makes the original unlock/close report failure. Remaining
	// original handles are still released; no successful observation may escape.
	_ = r.held[0].file.Close()
	return r.freshRoots.Close()
}

func TestActualPostgresRestoreRootsInspectionOriginalReadonlyOwner(t *testing.T) {
	if os.Geteuid() == 0 || os.Getegid() == 0 {
		t.Fatal("explicit nonroot container fixture required")
	}
	selector := os.Getenv("PROJECT_ONE_TEST_ROOTS_INSPECTION_INPUT_FILE")
	var input struct {
		Format       string `json:"format"`
		ReadonlyRoot string `json:"readonly_root"`
		ForeignRoot  string `json:"foreign_root"`
		CLIFile      string `json:"cli_file"`
		CLISHA256    string `json:"cli_sha256"`
		Receipt      string `json:"receipt"`
	}
	if _, e := readPrivateDocument(selector, 65536, &input); e != nil || input.Format != "projectone.postgres.roots-inspection-fixture.v1" || input.ReadonlyRoot != "/readonly-roots" || input.ForeignRoot != "/foreign-root" || input.CLIFile != "/cli-roots.test" || !release.ValidSHA256(input.CLISHA256) || input.Receipt != "/fixture-roots/receipt.json" {
		t.Fatal("exact private fixture selector")
	}
	var fs syscall.Statfs_t
	if syscall.Statfs("/runtime", &fs) != nil || uint64(fs.Type) != 0xef53 {
		t.Fatal("actual ext4 runtime required")
	}
	cliRaw, e := os.ReadFile(input.CLIFile)
	if e != nil || digest(cliRaw) != input.CLISHA256 {
		t.Fatal("same-source CLI test executable")
	}
	cliRaw = nil
	passed := []string{}
	var readonlyObservation release.PostgreSQLRestoreRootsInspectionObservation
	for _, name := range []string{"fresh-original", "policy-assertion", "nonempty", "symlink", "wrong-expected-owner", "foreign-actual-owner", "configuration-sha", "replaced-inode", "contended-lock", "release-failure", "kernel-readonly", "original-cli"} {
		t.Run(name, func(t *testing.T) {
			f := newRootsInspectionFixture(t)
			paths := storeRoots(f.runtime)
			if name == "fresh-original" {
				out, e := runRootsInspectionFixture(f)
				if e != nil || !out.ValidFor(f.input) {
					t.Fatal("original readonly producer", e)
				}
				lease, e := acquireFreshRoots(paths)
				if e != nil {
					t.Fatal("all original locks released", e)
				}
				if e = lease.Close(); e != nil {
					t.Fatal(e)
				}
			} else if name == "policy-assertion" {
				path := filepath.Join(filepath.Dir(f.request), "inspection-policy.env")
				if os.WriteFile(path, []byte(restoreRootsInspectionPolicyPrefix+"OPERATION_SECONDS=30\n"), 0600) != nil {
					t.Fatal("policy assertion")
				}
				out, e := InspectRestoreRoots(context.Background(), f.configuration, "", f.request, path, f.input)
				if e != nil || !out.ValidFor(f.input) || out.RequestSHA256 != f.input.SHA256() {
					t.Fatal("equal policy assertion changed frozen request", e)
				}
				if os.WriteFile(path, []byte(restoreRootsInspectionPolicyPrefix+"OPERATION_SECONDS=31\n"), 0600) != nil {
					t.Fatal("policy contradiction")
				}
				out, e = InspectRestoreRoots(context.Background(), f.configuration, "", f.request, path, f.input)
				if e == nil || out.Format != "" {
					t.Fatal("policy rewrote frozen request")
				}
			} else if name == "replaced-inode" || name == "release-failure" {
				out, e := inspectRestoreRootsUsing(context.Background(), f.configuration, "", f.request, "", f.input, func(paths []string) (restoreRootsInspectionLease, error) {
					lease, e := acquireFreshRoots(paths)
					if e != nil {
						return nil, e
					}
					if name == "release-failure" {
						return rootsInspectionCloseFault{lease}, nil
					}
					if e = os.Rename(paths[0], paths[0]+"-retained"); e != nil {
						lease.Close()
						return nil, e
					}
					if e = os.Mkdir(paths[0], 0700); e != nil {
						lease.Close()
						return nil, e
					}
					return lease, nil
				})
				if e == nil || out.Format != "" {
					t.Fatal("inode or release failure published success")
				}
			} else if name == "contended-lock" {
				lease, e := acquireFreshRoots(paths)
				if e != nil {
					t.Fatal(e)
				}
				out, e := runRootsInspectionFixture(f)
				ce := lease.Close()
				if e == nil || out.Format != "" || ce != nil {
					t.Fatal("held lock admitted", e, ce)
				}
			} else if name == "kernel-readonly" {
				for i, name := range []string{"models", "datasets", "training", "preparations"} {
					paths[i] = filepath.Join(input.ReadonlyRoot, name)
				}
				f.runtime.Models.ArtifactRoot = paths[0]
				f.runtime.Datasets.ArtifactRoot = paths[1]
				f.runtime.Training.ArtifactRoot = paths[2]
				f.runtime.ModelPreparation.ArtifactRoot = paths[3]
				updateRootsInspectionRuntime(t, &f)
				if e := os.WriteFile(filepath.Join(paths[0], "forbidden-write"), []byte("never written"), 0600); !os.IsPermission(e) && e != syscall.EROFS {
					if pe, ok := e.(*os.PathError); !ok || pe.Err != syscall.EROFS {
						t.Fatal("kernel readonly mount required", e)
					}
				}
				out, e := runRootsInspectionFixture(f)
				if e != nil || !out.ValidFor(f.input) {
					t.Fatal("readonly directory handles", e)
				}
				readonlyObservation = out
			} else if name == "original-cli" {
				selector := filepath.Join(filepath.Dir(f.request), "cli-input.json")
				receipt := filepath.Join(filepath.Dir(f.request), "cli-observation.json")
				writeRootsInspectionJSON(t, selector, map[string]string{"configuration": f.configuration, "request": f.request, "receipt": receipt})
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, input.CLIFile, "-test.v", "-test.run=^TestActualPostgresRestoreRootsInspectionCLI$", "-test.count=1", "-test.timeout=40s")
				command.Env = []string{"TMPDIR=/runtime", "PROJECT_ONE_TEST_ROOTS_INSPECTION_CLI_INPUT=" + selector}
				result, e := command.CombinedOutput()
				if e != nil {
					t.Fatal("same-source original CLI handler failed", string(result))
				}
				var out release.PostgreSQLRestoreRootsInspectionObservation
				if _, e = readPrivateDocument(receipt, f.input.Policy.MaximumRecordBytes, &out); e != nil || !out.ValidFor(f.input) {
					t.Fatal("original CLI response", e)
				}
			} else {
				switch name {
				case "nonempty":
					if os.WriteFile(filepath.Join(paths[0], "guard"), []byte("synthetic guard"), 0600) != nil {
						t.Fatal("guard")
					}
				case "symlink":
					if os.Rename(paths[0], paths[0]+"-retained") != nil || os.Symlink(paths[0]+"-retained", paths[0]) != nil {
						t.Fatal("symlink fixture")
					}
				case "wrong-expected-owner":
					f.input.ExpectedUID++
					writeRootsInspectionJSON(t, f.request, f.input)
				case "foreign-actual-owner":
					f.runtime.Models.ArtifactRoot = input.ForeignRoot
					updateRootsInspectionRuntime(t, &f)
				case "configuration-sha":
					f.input.RuntimeConfigurationSHA256 = strings.Repeat("9", 64)
					writeRootsInspectionJSON(t, f.request, f.input)
				}
				out, e := runRootsInspectionFixture(f)
				if e == nil || out.Format != "" {
					t.Fatal("foreign/nonfresh input admitted")
				}
			}
			passed = append(passed, name)
		})
	}
	if t.Failed() {
		return
	}
	for _, name := range []string{"models", "datasets", "training", "preparations"} {
		entries, e := os.ReadDir(filepath.Join(input.ReadonlyRoot, name))
		if e != nil || len(entries) != 0 {
			t.Fatal("readonly roots changed")
		}
	}
	writeRootsInspectionJSON(t, input.Receipt, map[string]any{"format": "projectone.postgres.roots-inspection-fixture-receipt.v1", "cases": passed, "readonly_mount_observation": readonlyObservation, "qualification": "Original readonly helper plus dedicated CLI handler, real ext4/readonly directory locks and controlled refusals. Context bindings are synthetic; no full main, SQL, Scope physical custody or image qualification."})
}
