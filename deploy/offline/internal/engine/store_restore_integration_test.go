//go:build offlineintegration

package engine

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type actualStoreRestoreFixture struct {
	Context                                                         context.Context
	CleanupContext                                                  func() context.Context
	QualifyAuthority                                                bool
	Client                                                          *Client
	BackupRoot, ScopeID, NetworkID, Image, Database, User, Password string
	SourceFiles                                                     []BackupArchiveFile
	UID, GID                                                        int
	Peers                                                           map[string]string
	Nonce                                                           func() string
	Put                                                             func(string, any)
	Query                                                           func(string) (string, error)
	Track                                                           func(Ownership)
}

func runActualSelectedStoreImports(t *testing.T, f actualStoreRestoreFixture) []map[string]any {
	t.Helper()
	c, ctx := f.Client, f.Context
	filesByName := map[string]BackupArchiveFile{}
	for _, file := range f.SourceFiles {
		filesByName[file.Name] = file
	}
	read := func(name string) []byte {
		file, ok := filesByName[name]
		if !ok {
			t.Fatal("source file absent from complete manifest")
		}
		raw, e := bundle.ReadProtectedConfiguration(filepath.Join(f.BackupRoot, filepath.FromSlash(name)))
		if e != nil || int64(len(raw)) != file.Bytes || wire.Digest(raw) != file.SHA256 {
			t.Fatal("protected complete source file changed", e)
		}
		return raw
	}
	configuration := map[string][]byte{}
	for _, role := range []string{"control-config", "operator-config"} {
		prefix := "configuration/" + role + "/"
		for _, file := range f.SourceFiles {
			if strings.HasPrefix(file.Name, prefix) {
				name := strings.TrimPrefix(file.Name, prefix)
				if !wire.SafePath(name) || filepath.Base(name) != name {
					t.Fatal("fixed source configuration basename")
				}
				if name != "bootstrap-request.json" {
					configuration[name] = read(file.Name)
				}
			}
		}
	}
	connection := url.URL{Scheme: "postgresql", Host: net.JoinHostPort(postgresPeerAlias, "5432"), Path: "/" + f.Database, User: url.UserPassword(f.User, f.Password), RawQuery: "sslmode=disable"}
	configuration["operator-dsn"] = []byte(connection.String() + "\n")
	if f.QualifyAuthority {
		runtime := connection
		runtimeSecret, e := hex.DecodeString(f.Nonce() + f.Nonce())
		if e != nil {
			t.Fatal("synthetic random credential bytes")
		}
		runtime.User = url.UserPassword("restore_runtime_"+f.ScopeID[:8], base64.RawURLEncoding.EncodeToString(runtimeSecret))
		configuration["runtime-dsn"] = []byte(runtime.String() + "\n")
		raw, e := bundle.ReadProtectedConfiguration(filepath.Join(f.BackupRoot, "backup.json"))
		if e != nil {
			t.Fatal(e)
		}
		var source struct {
			ScopeID string               `json:"scope_id"`
			Source  PostgresBackupSource `json:"source"`
		}
		if json.Unmarshal(raw, &source) != nil {
			t.Fatal("bound complete source metadata")
		}
		restored := wire.RestoredRuntimeContext{FormatVersion: 1, RestoreID: f.Nonce(), SourceScopeID: source.ScopeID, TargetScopeID: f.ScopeID, BackupManifestSHA256: wire.Digest(raw), SQLArchiveSHA256: filesByName["postgres.tar"].SHA256, TenantID: source.Source.TenantID, SchemaVersion: source.Source.SchemaVersion, TargetDatabase: f.Database, TargetOperator: f.User, Reason: "M17 isolated current restored runtime role"}
		if !restored.Valid() {
			t.Fatal("new-target protected restore context")
		}
		configuration["restored-runtime.json"], e = json.Marshal(restored)
		if e != nil {
			t.Fatal(e)
		}
	}
	if len(configuration["control.yaml"]) == 0 {
		t.Fatal("existing protected operator configuration required")
	}
	names := []string{}
	for name := range configuration {
		names = append(names, name)
	}
	sort.Strings(names)
	payload := []ConfigurationFile{}
	for _, name := range names {
		payload = append(payload, ConfigurationFile{Name: name, Contents: configuration[name]})
	}
	prepare := func(owner Ownership, files []ConfigurationFile, keepUtility bool) {
		f.Track(owner)
		if _, e := c.CreateVolume(ctx, owner); e != nil {
			t.Fatal(e)
		}
		if _, e := c.CreateUtility(ctx, owner, f.Image, f.UID, f.GID); e != nil {
			t.Fatal(e)
		}
		if files != nil {
			if e := c.PutConfiguration(ctx, owner, f.Image, f.UID, f.GID, files); e != nil {
				t.Fatal(e)
			}
		}
		if !keepUtility {
			if e := c.RemoveUtility(ctx, owner, f.Image, f.UID, f.GID); e != nil {
				t.Fatal(e)
			}
		}
	}
	var active *DatabaseTask
	activeID := ""
	t.Cleanup(func() {
		if active == nil {
			return
		}
		bounded := f.CleanupContext()
		v, found, e := c.InspectDatabaseTask(bounded, *active)
		if e != nil {
			t.Error(e)
			return
		}
		if !found {
			return
		}
		if v.ID != activeID {
			t.Error("actual store task changed")
			return
		}
		if v.State.Running {
			if e = c.StopDatabaseTask(bounded, *active, v.ID, 2); e != nil {
				t.Error(e)
				return
			}
		}
		if e = c.RemoveDatabaseTask(bounded, *active, v.ID); e != nil {
			t.Error(e)
		}
	})
	results := []map[string]any{}
	for _, role := range []string{"models", "datasets"} {
		original, ok := filesByName[role+".tar"]
		if !ok {
			t.Fatal("declared source archive missing")
		}
		input, e := os.Open(filepath.Join(f.BackupRoot, original.Name))
		if e != nil {
			t.Fatal(e)
		}
		descriptor, e := DescribeStoreRestoreArchive(ctx, input, StoreRestoreArtifact{Role: role, UID: f.UID, GID: f.GID, Bytes: original.Bytes, SHA256: original.SHA256, MaximumEntries: 100000})
		input.Close()
		if e != nil {
			t.Fatal("actual source description", e)
		}
		f.Put(role+"-source-descriptor.json", descriptor)
		data := Ownership{f.ScopeID, f.Nonce(), role}
		prepare(data, []ConfigurationFile{}, false)
		kinds := []string{ModelFilesImport, ModelFilesVerify}
		auditAction := "model.files_imported"
		if role == "datasets" {
			kinds = []string{DatasetFilesImport, DatasetFilesVerify}
			auditAction = "dataset.files_imported"
		}
		before, e := f.Query("SELECT count(*) FROM public.audit_events WHERE audience='operator' AND action='" + auditAction + "'")
		if e != nil {
			t.Fatal(e)
		}
		beforeCount, e := strconv.Atoi(before)
		if e != nil {
			t.Fatal(e)
		}
		for _, kind := range kinds {
			op := f.Nonce()
			profile, _ := storeRestoreTask(kind)
			task := DatabaseTask{Owner: Ownership{f.ScopeID, op, "database-" + kind}, VolumeOwner: Ownership{f.ScopeID, op, StoreRestoreConfigRole + "-" + op}, OutputOwner: &data, Kind: kind, Image: f.Image, UID: f.UID, GID: f.GID, NetworkID: f.NetworkID, Peers: f.Peers}
			if profile.reason {
				task.Reason = "M17 isolated actual restored store acceptance"
			}
			prepare(task.VolumeOwner, payload, false)
			if profile.importFiles {
				task.StoreRestore = &descriptor
				task.SourceOwner = &Ownership{f.ScopeID, op, StoreRestoreArchiveRole + "-" + role + "-" + op}
				prepare(*task.SourceOwner, nil, true)
				f.Put(kind+"-intent.json", task)
				input, e := os.Open(filepath.Join(f.BackupRoot, original.Name))
				if e != nil {
					t.Fatal(e)
				}
				transport := c.http.Transport
				loss := &restoreResultLoss{base: transport, method: http.MethodPut, suffix: "/archive"}
				c.http.Transport = loss
				_, copyError := c.PutStoreRestoreArchive(ctx, task, f.Image, input)
				c.http.Transport = transport
				input.Close()
				if copyError == nil || loss.hits != 1 {
					t.Fatal("actual store successful copy result loss not exercised", copyError)
				}
				if _, e = c.InspectStoreRestoreArchiveInput(ctx, task, f.Image); e != nil {
					t.Fatal("actual store copy reconciliation", e)
				}
				if e = c.RemoveUtility(ctx, *task.SourceOwner, f.Image, f.UID, f.GID); e != nil {
					t.Fatal(e)
				}
			} else {
				f.Put(kind+"-intent.json", task)
			}
			active, activeID = &task, ""
			v, e := c.CreateDatabaseTask(ctx, task)
			activeID = v.ID
			if e != nil {
				t.Fatal("actual store task create", e)
			}
			f.Put(kind+"-created.json", map[string]string{"id": v.ID})
			transport := c.http.Transport
			loss := &restoreResultLoss{base: transport, method: http.MethodPost, suffix: "/start"}
			if profile.importFiles {
				c.http.Transport = loss
			}
			_, e = c.StartDatabaseTask(ctx, task, activeID, payload, 30)
			c.http.Transport = transport
			if profile.importFiles {
				if e == nil || loss.hits != 1 {
					t.Fatal("actual store import successful start result loss not exercised", e)
				}
			} else if e != nil {
				t.Fatal("actual backend verification start", e)
			}
			for deadline := time.Now().Add(50 * time.Second); ; {
				var found bool
				v, found, e = c.InspectDatabaseTask(ctx, task)
				if e != nil || !found {
					t.Fatal(e)
				}
				if v.State.Status == "exited" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("actual store task deadline")
				}
				time.Sleep(100 * time.Millisecond)
			}
			f.Put(kind+"-outcome.json", map[string]any{"id": activeID, "exit_code": v.State.ExitCode, "oom_killed": v.State.OOMKilled})
			if v.State.ExitCode != 0 || v.State.OOMKilled {
				t.Fatal("actual existing backend store operation failed", kind, v.State.ExitCode)
			}
			if _, e = c.StartDatabaseTask(ctx, task, activeID, payload, 30); e == nil {
				t.Fatal("terminal import/verify replay")
			}
			if e = c.RemoveDatabaseTask(ctx, task, activeID); e != nil {
				t.Fatal(e)
			}
			active, activeID = nil, ""
			results = append(results, map[string]any{"kind": kind, "exit_code": 0, "source_archive_sha256": original.SHA256, "source_bytes": original.Bytes, "backend_authority": "existing_control_command", "real_copy_and_start_result_losses_reconciled_without_resend": profile.importFiles})
		}
		after, e := f.Query("SELECT count(*) FROM public.audit_events WHERE audience='operator' AND action='" + auditAction + "'")
		if e != nil || after != strconv.Itoa(beforeCount+1) {
			t.Fatal("exact restored import audit", e)
		}
		if _, e = DescribeStoreRestoreArchive(ctx, mustOpenActualRestore(t, filepath.Join(f.BackupRoot, original.Name)), descriptor.Artifact); e != nil {
			t.Fatal("original snapshot changed", e)
		}
	}
	if f.QualifyAuthority {
		results = append(results, runActualRestoredQualification(t, f, payload, prepare, func(task *DatabaseTask, id string) { active, activeID = task, id })...)
	}
	counts, e := f.Query("SELECT (SELECT count(*) FROM public.model_versions)||'|'||(SELECT count(*) FROM public.datasets)||'|'||(SELECT count(*) FROM public.dataset_versions)||'|'||(SELECT max(version) FROM public.schema_migrations)")
	if e != nil || counts != "0|1|2|16" {
		t.Fatal("selected restored business history", counts, e)
	}
	f.Put("store-results.json", map[string]any{"results": results, "counts_model_dataset_versions_schema": counts, "model_catalog_qualification": "empty_existing_model_catalog_only_no_model_weight_restore_claim", "no_source_export_bootstrap_or_user_regeneration": true, "qualification": "real_selected_backend_store_import_verify_and_audit_only_not_runtime_access_auth_browser_full_restore_or_native_target"})
	return results
}

func mustOpenActualRestore(t *testing.T, name string) *os.File {
	t.Helper()
	f, e := os.Open(name)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
