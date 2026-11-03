//go:build offlineintegration

package scope

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type applicationImageReceipt struct {
	Status       string `json:"status"`
	Architecture string `json:"architecture"`
	Image        string `json:"platform_manifest_digest"`
	Config       string `json:"image_config_digest"`
	Archive      struct {
		Path  string `json:"path"`
		Bytes int64  `json:"size_bytes"`
		SHA   string `json:"sha256"`
	} `json:"archive"`
}

// Optional private acceptance input for a fresh target of the effective backup.
// This test-only envelope never configures product behavior or customer systems.
type actualRestoreExpectations struct {
	FormatVersion        int    `json:"format_version"`
	SourceScopeID        string `json:"source_scope_id"`
	SourceManifestSHA256 string `json:"source_manifest_sha256"`
	MaximumJournalBytes  int64  `json:"maximum_journal_bytes"`
	Users                int    `json:"users"`
	Datasets             int    `json:"datasets"`
	DatasetVersions      int    `json:"dataset_versions"`
	SchemaVersion        int    `json:"schema_version"`
}

func loadActualRestoreExpectations(raw []byte) (actualRestoreExpectations, error) {
	var value actualRestoreExpectations
	if len(raw) > 1<<20 || !utf8.Valid(raw) || wire.StrictJSON(raw, &value) != nil || value.FormatVersion != 1 || !identityPattern.MatchString(value.SourceScopeID) || !wire.ValidSHA256(value.SourceManifestSHA256) || value.MaximumJournalBytes < configuration.MinimumJournalBytes || value.MaximumJournalBytes > configuration.MaximumJournalBytes || value.Users < 1 || value.Users > 10000 || value.Datasets < 1 || value.Datasets > 10000 || value.DatasetVersions < value.Datasets || value.DatasetVersions > 10000 || value.SchemaVersion != 16 {
		return actualRestoreExpectations{}, failure("ACTUAL_RESTORE_EXPECTATIONS_INVALID")
	}
	return value, nil
}

func TestActualRestoreExpectationsBoundSourceInventoryAndBudget(t *testing.T) {
	value := actualRestoreExpectations{FormatVersion: 1, SourceScopeID: strings.Repeat("a", 32), SourceManifestSHA256: strings.Repeat("b", 64), MaximumJournalBytes: 8 << 20, Users: 3, Datasets: 1, DatasetVersions: 2, SchemaVersion: 16}
	raw := encode(value)
	if got, e := loadActualRestoreExpectations(raw); e != nil || got != value {
		t.Fatal("explicit fresh source inventory", e)
	}
	for _, change := range []func(*actualRestoreExpectations){func(v *actualRestoreExpectations) { v.SourceScopeID = "" }, func(v *actualRestoreExpectations) { v.SourceManifestSHA256 = "" }, func(v *actualRestoreExpectations) { v.MaximumJournalBytes = 0 }, func(v *actualRestoreExpectations) { v.MaximumJournalBytes = configuration.MaximumJournalBytes + 1 }, func(v *actualRestoreExpectations) { v.Users = 0 }, func(v *actualRestoreExpectations) { v.Users = 10001 }, func(v *actualRestoreExpectations) { v.DatasetVersions = 0 }, func(v *actualRestoreExpectations) { v.SchemaVersion = 15 }} {
		copy := value
		change(&copy)
		if _, e := loadActualRestoreExpectations(encode(copy)); e == nil {
			t.Fatal("invalid expectations admitted")
		}
	}
	for _, bad := range [][]byte{[]byte("null"), []byte(`{}`), bytes.Replace(raw, []byte(`"users":3`), []byte(`"users":null`), 1), bytes.Replace(raw, []byte(`"users":3`), []byte(`"users":false`), 1), bytes.Replace(raw, []byte(`"users":3`), []byte(`"users":3,"users":4`), 1), bytes.Replace(raw, []byte(`"users":3`), []byte(`"unknown":true,"users":3`), 1)} {
		if _, e := loadActualRestoreExpectations(bad); e == nil {
			t.Fatal("unknown/duplicate/null/false expectations admitted")
		}
	}
}

// This selected installation profile exercises the actual current complete
// image, SQL, models and dataset processor. Optional training/preparation workers,
// gateway, SMTP, Google locales and a complete production release stay separate.
func TestActualSignedCurrentApplicationAndRestart(t *testing.T) {
	runActualSignedApplication(t, false)
}

func TestActualReservedSQLRestoreRetainsNewTarget(t *testing.T) {
	runActualSignedApplication(t, true)
}

func runActualSignedApplication(t *testing.T, restoreSQL bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	readReceipt := func(variable string) applicationImageReceipt {
		raw, e := os.ReadFile(os.Getenv(variable))
		if e != nil || len(raw) > 4<<20 {
			t.Fatal("bounded image receipt required", e)
		}
		var value applicationImageReceipt
		if wire.UniqueJSON(raw, &value) != nil || value.Status != "passed_actual_declared_image_archive_and_full_blob_readback" || value.Architecture != "amd64" || !wire.ValidSHA256(value.Archive.SHA) || value.Archive.Bytes < 1 || value.Archive.Bytes > 1<<30 {
			t.Fatal("verified declared actual image receipt required")
		}
		return value
	}
	control := readReceipt("PROJECT_ONE_TEST_APPLICATION_CONTROL_RECEIPT")
	postgres := readReceipt("PROJECT_ONE_TEST_APPLICATION_POSTGRES_RECEIPT")
	// The original PG exporter calls the same platform manifest image_id.
	if postgres.Image == "" {
		raw, _ := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_APPLICATION_POSTGRES_RECEIPT"))
		var p struct {
			Image string `json:"image_id"`
		}
		if json.Unmarshal(raw, &p) != nil {
			t.Fatal("PG image receipt")
		}
		postgres.Image = p.Image
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_APPLICATION_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	profile, e := configuration.Load(raw, nil)
	if e != nil || profile.Engine == nil || profile.Compose == nil || profile.Compose.Process == nil {
		t.Fatal("explicit actual operator profile required", e)
	}
	var expected *actualRestoreExpectations
	if name := os.Getenv("PROJECT_ONE_TEST_SCOPE_RESTORE_EXPECTATIONS"); name != "" {
		if !restoreSQL {
			t.Fatal("fresh restore expectations unused outside SQL restore")
		}
		raw, e := bundle.ReadProtectedConfiguration(name)
		if e != nil {
			t.Fatal("protected fresh restore expectations required", e)
		}
		value, e := loadActualRestoreExpectations(raw)
		if e != nil {
			t.Fatal(e)
		}
		expected = &value
	}
	binary := os.Getenv("PROJECT_ONE_TEST_APPLICATION_OPERATOR")
	binaryFile, e := os.Open(binary)
	if e != nil {
		t.Fatal(e)
	}
	binaryHash := sha256.New()
	_, e = io.Copy(binaryHash, binaryFile)
	binaryFile.Close()
	if e != nil || hex.EncodeToString(binaryHash.Sum(nil)) != os.Getenv("PROJECT_ONE_TEST_APPLICATION_OPERATOR_SHA256") {
		t.Fatal("current operator binary required")
	}
	assets := []wire.Asset{}
	for i, r := range []applicationImageReceipt{control, postgres} {
		id := []string{"current-control", "postgres"}[i]
		assets = append(assets, wire.Asset{ID: id, Path: "images/" + id + ".tar", Kind: "image_archive", Size: r.Archive.Bytes, SHA256: r.Archive.SHA, ImageID: r.Image, ImageConfigDigest: r.Config, Architecture: "amd64", Licenses: []string{"license"}})
	}
	fixtureParent := os.Getenv("PROJECT_ONE_TEST_APPLICATION_SCOPE_PARENT")
	if fixtureParent == "" {
		t.Fatal("retained private fixture parent required for interrupted recovery")
	}
	c := fixtureWithFileAssetsAt(t, assets, []string{control.Archive.Path, postgres.Archive.Path}, fixtureParent)
	if expected != nil {
		if !mounted(profile.Verification.MountRoot) || !contained(profile.Verification.MountRoot, fixtureParent) {
			t.Fatal("declared external fixture mount required")
		}
		c.Verification.MountRoot = profile.Verification.MountRoot
	}
	c.Engine, c.Compose = profile.Engine, profile.Compose
	c.Engine.MaximumExpandedImageBytes = 2 << 30
	c.Engine.Utility = &wire.UtilityResources{MemoryBytes: 256 << 20, MilliCPU: 100, PIDs: 32}
	c.MaximumJournalBytes = 2 << 20
	if expected != nil {
		c.MaximumJournalBytes = expected.MaximumJournalBytes
	}
	c.TimeoutSeconds = 600
	parent := filepath.Dir(c.ScopeRoot)
	c.Compose.ModelFile, c.Compose.InputsFile = filepath.Join(parent, "model.json"), filepath.Join(parent, "sources.json")
	port := func() int {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		value := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		return value
	}
	portalPort, adminPort := port(), port()
	if portalPort == adminPort {
		t.Fatal("distinct loopback ports required")
	}
	portal, admin := fmt.Sprintf("https://127.0.0.1:%d", portalPort), fmt.Sprintf("https://127.0.0.1:%d", adminPort)
	const listenerPort = 18443
	databaseName := "projectone_m17_test"
	if restoreSQL {
		generation, e := nonce()
		if e != nil {
			t.Fatal(e)
		}
		databaseName = "projectone_restore_" + generation[:8]
	}
	secret := func() []byte {
		value := make([]byte, 32)
		if _, e := rand.Read(value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	operatorPassword, runtimePassword := base64.RawURLEncoding.EncodeToString(secret()), base64.RawURLEncoding.EncodeToString(secret())
	dsn := func(user, password string) []byte {
		return []byte((&url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: "postgres:5432", Path: "/" + databaseName, RawQuery: "sslmode=disable"}).String())
	}
	base := engine.DatabaseTaskDestination + "/private/"
	certificate, privateKey, roots, spki := applicationCertificate(t)
	runtimeConfiguration := map[string]any{
		"server":   map[string]any{"listen_address": fmt.Sprintf("0.0.0.0:%d", listenerPort), "portal_origin": portal, "admin_origin": admin, "tls_certificate_file": base + "certificate.pem", "tls_private_key_file": base + "private-key.pem", "portal_assets": "/opt/projectone/portal", "admin_assets": "/opt/projectone/admin"},
		"database": map[string]any{"dsn_file": base + "runtime-dsn", "max_open_connections": 4, "max_idle_connections": 1, "statement_timeout_seconds": 5},
		"security": map[string]any{"auth_hmac_key_file": base + "auth-hmac", "mail_keyring_file": base + "mail-keyring.json", "password_blocklist_file": "/opt/projectone/security/password-blocklist.txt"},
		"smtp":     map[string]any{"host": "smtp.example.invalid", "port": 587, "from_address": "platform@example.invalid", "tls_mode": "starttls_required", "connect_timeout_seconds": 5, "operation_timeout_seconds": 20},
		"models":   map[string]any{"artifact_root": "/var/lib/projectone/models/private"},
		"datasets": map[string]any{"artifact_root": "/var/lib/projectone/datasets/private", "python_executable": "/usr/local/bin/python3.12", "processor_file": "/opt/projectone/data-processing/process.py"},
	}
	runtimeRaw := encode(runtimeConfiguration)
	operatorConfiguration := map[string]any{}
	if json.Unmarshal(runtimeRaw, &operatorConfiguration) != nil {
		t.Fatal("operator configuration")
	}
	operatorConfiguration["database"].(map[string]any)["dsn_file"] = base + "operator-dsn"
	files := map[string][]engine.ConfigurationFile{
		"postgres-config": {{Name: "postgres-password", Contents: []byte(operatorPassword)}},
		"operator-config": {{Name: "control.yaml", Contents: encode(operatorConfiguration)}, {Name: "runtime.yaml", Contents: runtimeRaw}, {Name: "operator-dsn", Contents: dsn(databaseName, operatorPassword)}, {Name: "runtime-dsn", Contents: dsn(databaseName+"_runtime", runtimePassword)}, {Name: "bootstrap-request.json", Contents: []byte(`{"format_version":1,"administrator_email":"owner@example.invalid","display_name":"Owner","installation_name":"Application acceptance","workspace_name":"Workspace"}`)}, {Name: "store-request.json", Contents: encode(StoreRequest{FormatVersion: 1, Reason: "Synthetic current application dataset initialization"})}},
		"control-config":  {{Name: "runtime.yaml", Contents: runtimeRaw}, {Name: "runtime-dsn", Contents: dsn(databaseName+"_runtime", runtimePassword)}, {Name: "auth-hmac", Contents: secret()}, {Name: "mail-keyring.json", Contents: encode(map[string]any{"active": "test-key", "keys": map[string]string{"test-key": base64.StdEncoding.EncodeToString(secret())}})}, {Name: "certificate.pem", Contents: certificate}, {Name: "private-key.pem", Contents: privateKey}},
	}
	var restoreRequest configuration.RestoreRequest
	if restoreSQL {
		requestRaw, e := bundle.ReadProtectedConfiguration(os.Getenv("PROJECT_ONE_TEST_SCOPE_RESTORE_REQUEST"))
		if e != nil {
			t.Fatal(e)
		}
		restoreRequest, e = configuration.LoadRestoreRequest(requestRaw, nil)
		if e != nil {
			t.Fatal(e)
		}
		source, e := InspectRestoreSource(ctx, c, restoreRequest.RestoreSourceRequest)
		if e != nil {
			t.Fatal("independent actual source", e)
		}
		if expected != nil && (source.SourceScopeID != expected.SourceScopeID || source.ManifestSHA256 != expected.SourceManifestSHA256) {
			t.Fatal("fresh acceptance source lineage differs from declared current backup")
		}
		_, original, e := readRestoreSourceConfiguration(ctx, c, restoreRequest.RestoreSourceRequest, source.PlanSHA256)
		if e != nil {
			t.Fatal(e)
		}
		for _, name := range []string{"auth-hmac", "mail-keyring.json"} {
			contents, e := restoreNamedFile(original["control-config"], name)
			if e != nil {
				t.Fatal(e)
			}
			for i := range files["control-config"] {
				if files["control-config"][i].Name == name {
					files["control-config"][i].Contents = append([]byte{}, contents...)
				}
			}
		}
	}
	roles := []SourceRole{}
	volumeKeys := []string{"postgres-config", "postgres", "operator-config", engine.BootstrapStateRole, "control-config", "models", "datasets"}
	for _, key := range volumeKeys {
		role := SourceRole{Key: key}
		for _, file := range files[key] {
			path := filepath.Join(parent, key+"-"+file.Name)
			put(t, path, file.Contents)
			role.Files = append(role.Files, SourceFile{Name: file.Name, SourceFile: path})
		}
		roles = append(roles, role)
	}
	model := compose.Model{Volumes: volumeKeys, Services: map[string]compose.Service{
		"control":  {Image: control.Image, User: "10033:10034", Command: []string{"serve", "--config", base + "runtime.yaml"}, MemoryBytes: 768 << 20, MilliCPU: 1000, PIDs: 64, Restart: "no", ReadOnly: true, Mounts: []compose.Mount{{Type: "volume", Source: "control-config", Target: engine.DatabaseTaskDestination, ReadOnly: true}, {Type: "volume", Source: "models", Target: "/var/lib/projectone/models"}, {Type: "volume", Source: "datasets", Target: "/var/lib/projectone/datasets"}}, Ports: []compose.Port{{HostIP: "127.0.0.1", Published: portalPort, Target: listenerPort}, {HostIP: "127.0.0.1", Published: adminPort, Target: listenerPort}}, Tmpfs: []compose.Tmpfs{{Target: "/tmp", SizeBytes: 32 << 20, UID: 10033, GID: 10034}}},
		"postgres": {Image: postgres.Image, User: "999:999", MemoryBytes: 512 << 20, MilliCPU: 1000, PIDs: 64, Restart: "no", ReadOnly: true, Environment: map[string]string{"POSTGRES_USER": databaseName, "POSTGRES_DB": databaseName, "POSTGRES_PASSWORD_FILE": "/run/projectone/postgres/private/postgres-password", "PGDATA": "/var/lib/postgresql/private/data"}, Mounts: []compose.Mount{{Type: "volume", Source: "postgres-config", Target: "/run/projectone/postgres", ReadOnly: true}, {Type: "volume", Source: "postgres", Target: "/var/lib/postgresql"}}, Tmpfs: []compose.Tmpfs{{Target: "/var/run/postgresql", SizeBytes: 16 << 20, UID: 999, GID: 999}, {Target: "/tmp", SizeBytes: 16 << 20, UID: 999, GID: 999}}},
	}}
	put(t, c.Compose.ModelFile, encode(model))
	put(t, c.Compose.InputsFile, encode(SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: "current-control", Roles: roles}))
	configFile := filepath.Join(parent, "operator.json")
	put(t, configFile, encode(c))
	operationFile := filepath.Join(parent, "restore-request.json")
	sourceOperationFile := filepath.Join(parent, "restore-source-request.json")
	if restoreSQL {
		put(t, operationFile, encode(restoreRequest))
		put(t, sourceOperationFile, encode(restoreRequest.RestoreSourceRequest))
	}
	output := os.Getenv("PROJECT_ONE_TEST_APPLICATION_EVIDENCE")
	if output == "" {
		t.Fatal("private evidence path required")
	}
	steps := []string{}
	cli := func(command string) []byte {
		args := []string{command, "--config", configFile}
		if command == "prepare-restore" || command == "inspect-restore-target" || command == "restore-sql" || command == "reconcile-restore-sql" || command == "abort-restore-sql" {
			selectedOperation := operationFile
			if command == "inspect-restore-target" {
				selectedOperation = sourceOperationFile
			}
			args = append(args, "--operation-file", selectedOperation)
		}
		value, e := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if e != nil {
			t.Fatalf("actual application operator %s failed: %v %s", command, e, value)
		}
		steps = append(steps, command)
		return value
	}
	var initial Status
	if json.Unmarshal(cli("init-scope"), &initial) != nil {
		t.Fatal("scope identity")
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	render := compose.RenderContext{ScopeID: initial.ScopeID, ScopeRoot: c.ScopeRoot, AllowedImages: map[string]bool{control.Image: true, postgres.Image: true}}
	private := filepath.Join(parent, "docker-client")
	if os.Mkdir(private, 0700) != nil {
		t.Fatal("private client")
	}
	put(t, filepath.Join(private, "config.json"), []byte("{}\n"))
	docker := func(commandContext context.Context, args ...string) ([]byte, error) {
		command := exec.CommandContext(commandContext, c.Compose.DockerBinary, append([]string{"--config", private, "--host", "unix://" + c.Engine.SocketFile}, args...)...)
		command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
		return command.CombinedOutput()
	}
	cleaned := false
	applicationReady := false
	restoreStageReady := false
	t.Cleanup(func() {
		cleanupContext, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		l, e := open(c, false)
		if e != nil {
			t.Error("cleanup ownership unavailable", e)
			return
		}
		events := append([]Event(nil), l.events...)
		l.close()
		put(t, output+".journal.json", encodeJournal(appendJournal(events)))
		if restoreSQL {
			sql := restoreSQLState(events)
			if sql.record != nil && !sql.complete && !sql.aborted {
				value, e := exec.CommandContext(cleanupContext, binary, "abort-restore-sql", "--config", configFile, "--operation-file", operationFile).CombinedOutput()
				if e != nil {
					t.Error("exact SQL cleanup unresolved", e, string(value))
					return
				}
			}
		}
		if os.Getenv("PROJECT_ONE_TEST_APPLICATION_RETAIN_STOPPED") == "1" && (applicationReady || restoreStageReady) {
			value, e := exec.CommandContext(cleanupContext, binary, "stop-runtime", "--config", configFile).CombinedOutput()
			if e != nil {
				t.Error("qualified scope recorded stop failed", e, string(value))
				return
			}
			observed, e := worker.ObserveRuntime(cleanupContext, client, render, model)
			if e != nil {
				t.Error("retained scope observation", e)
				return
			}
			for _, service := range observed.Services {
				if service.State != "exited" && !(restoreStageReady && service.State == "created") {
					t.Error("retained scope still active")
					return
				}
			}
			l, e := open(c, false)
			if e != nil {
				t.Error(e)
				return
			}
			events = append([]Event(nil), l.events...)
			l.close()
			put(t, output+".journal.json", encodeJournal(appendJournal(events)))
			if restoreStageReady {
				put(t, output+".retention.json", encode(map[string]any{"status": "retained_exact_SQL_restored_scope_stopped_qualification_pending", "scope_id": initial.ScopeID, "operator_config": configFile, "restore_request": operationFile, "resources": observed, "SQL_stage_assertions_passed": !t.Failed(), "qualification": "SQL_stage_only_not_schema_access_auth_files_browser_or_target"}))
			} else {
				put(t, output+".retention.json", encode(map[string]any{"status": "retained_exact_qualified_application_scope_stopped", "scope_id": initial.ScopeID, "operator_config": configFile, "browser_config": browserConfigName(parent), "resources": observed, "application_assertions_passed": !t.Failed(), "bootstrap_handoff": "scope/" + bootstrapCopyName, "qualification": "selected_synthetic_core_profile_only_not_full_release_or_target"}))
			}
			return
		}
		if task := databaseTaskState(events).record; task != nil {
			if value, found, e := client.InspectDatabaseTask(cleanupContext, task.Task); e != nil {
				t.Error(e)
				return
			} else if found {
				if value.State.Running {
					if e = client.StopDatabaseTask(cleanupContext, task.Task, value.ID, 2); e != nil {
						t.Error(e)
						return
					}
				}
				if e = client.RemoveDatabaseTask(cleanupContext, task.Task, value.ID); e != nil {
					t.Error(e)
					return
				}
			}
		}
		if task := modelTaskState(events).record; task != nil {
			if value, found, e := client.InspectModelTask(cleanupContext, task.Task); e != nil {
				t.Error(e)
				return
			} else if found {
				if value.State.Running {
					if e = client.StopModelTask(cleanupContext, task.Task, value.ID, 2); e != nil {
						t.Error(e)
						return
					}
				}
				if e = client.RemoveModelTask(cleanupContext, task.Task, value.ID); e != nil {
					t.Error(e)
					return
				}
			}
		}
		observed, e := worker.ObserveRuntime(cleanupContext, client, render, model)
		if e != nil {
			t.Error(e)
			return
		}
		if len(observed.Services) > 0 {
			if _, e = worker.StopRuntime(cleanupContext, client, render, model, observed); e != nil {
				t.Error(e)
				return
			}
			for _, service := range observed.Services {
				if _, e = docker(cleanupContext, "container", "rm", service.ID); e != nil {
					t.Error(e)
					return
				}
			}
		}
		for _, id := range []string{observed.NetworkID, observed.IngressNetworkID} {
			if id != "" {
				if _, e = docker(cleanupContext, "network", "rm", id); e != nil {
					t.Error(e)
					return
				}
			}
		}
		for _, r := range volumeState(events).roles {
			owner := engine.Ownership{ScopeID: initial.ScopeID, OperationID: r.ResourceOperationID, Role: r.Key}
			if _, found, e := client.InspectVolume(cleanupContext, owner); e != nil || !found {
				t.Error("volume cleanup identity", e)
				return
			}
			if e = client.VerifyVolumeConsumers(cleanupContext, owner, nil); e != nil {
				t.Error(e)
				return
			}
			if _, e = docker(cleanupContext, "volume", "rm", owner.VolumeName()); e != nil {
				t.Error(e)
				return
			}
		}
		cleaned = true
		put(t, output+".cleanup.json", encode(map[string]any{"exact_scope_objects_removed": cleaned, "shared_actual_images_retained": true, "scope_id": initial.ScopeID}))
	})
	commands := []string{"import-images", "prepare-storage-inputs", "initialize-storage", "create-model-store-task", "start-model-store-task", "remove-model-store-task", "create-runtime", "start-database"}
	if restoreSQL {
		commands = []string{"import-images", "prepare-storage-inputs", "initialize-storage", "create-runtime", "start-database"}
	}
	for _, command := range commands {
		cli(command)
	}
	var running ProcessResult
	if json.Unmarshal(cli("reconcile-process"), &running) != nil {
		t.Fatal("database process observation")
	}
	pgID := running.Resources.Services["postgres"].ID
	query := func(statement string) (string, error) {
		value, e := docker(ctx, "exec", "--user", "999:999", pgID, "psql", "-X", "--set", "ON_ERROR_STOP=1", "-U", databaseName, "-d", databaseName, "-At", "-c", statement)
		return strings.TrimSpace(string(value)), e
	}
	for deadline := time.Now().Add(time.Minute); ; {
		if value, e := query("SELECT 1"); e == nil && value == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("database readiness timeout")
		}
		time.Sleep(200 * time.Millisecond)
	}
	if restoreSQL {
		var target RestoreTargetResult
		if json.Unmarshal(cli("inspect-restore-target"), &target) != nil || target.Status != "verified_restore_target_lineage_not_restored" {
			t.Fatal("actual independent target admission")
		}
		var reserved RestoreReservationResult
		if json.Unmarshal(cli("prepare-restore"), &reserved) != nil || reserved.Status != "restore_reserved_execution_pending" {
			t.Fatal("actual durable restore reservation")
		}
		var result RestoreSQLResult
		SQLCommand := "restore-sql"
		for deadline := time.Now().Add(90 * time.Second); ; {
			value, e := exec.CommandContext(ctx, binary, SQLCommand, "--config", configFile, "--operation-file", operationFile).CombinedOutput()
			steps = append(steps, SQLCommand)
			if e == nil {
				if json.Unmarshal(value, &result) != nil {
					t.Fatal("actual SQL result encoding")
				}
				break
			}
			if string(value) != "OFFLINE_SCOPE_RESTORE_SQL_TASK_DEADLINE\n" || time.Now().After(deadline) {
				t.Fatal("actual SQL stage unresolved", e, string(value))
			}
			SQLCommand = "reconcile-restore-sql"
		}
		if result.Status != "sql_restored_other_qualification_pending" || !wire.ValidSHA256(result.ProofSHA256) || result.CompletedSteps != result.TotalSteps {
			t.Fatal("actual durable SQL stage")
		}
		var status Status
		if json.Unmarshal(cli("status"), &status) != nil || status.RestoreSQL == nil || status.RestoreSQL.ProofSHA256 != result.ProofSHA256 || status.BootstrapQualified || status.SchemaQualified || status.RuntimeAccessQualified {
			t.Fatal("SQL proof forged broader qualification")
		}
		before, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if e != nil {
			t.Fatal(e)
		}
		l, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		starts := 0
		for _, event := range l.events {
			if event.Action == "restore_sql_effect" && event.Outcome == "started" && event.RestoreSQL != nil && event.RestoreSQL.Phase == "task-execute" {
				starts++
			}
		}
		l.close()
		if starts != 1 {
			t.Fatal("SQL start intent replayed", starts)
		}
		if json.Unmarshal(cli("restore-sql"), &result) != nil {
			t.Fatal("idempotent SQL stage read")
		}
		after, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if e != nil || !bytes.Equal(before, after) {
			t.Fatal("completed SQL stage replayed")
		}
		value, e := query("SELECT (SELECT count(*) FROM public.users)||'|'||(SELECT count(*) FROM public.datasets)||'|'||(SELECT count(*) FROM public.dataset_versions)||'|'||(SELECT max(version) FROM public.schema_migrations)")
		wanted := "1|1|2|16"
		if expected != nil {
			wanted = fmt.Sprintf("%d|%d|%d|%d", expected.Users, expected.Datasets, expected.DatasetVersions, expected.SchemaVersion)
		}
		if e != nil || value != wanted {
			t.Fatal("actual restored selected business counts", e)
		}
		restoreStageReady = true
		put(t, output, encode(map[string]any{"status": "passed_actual_new_scope_durable_SQL_restore_other_qualification_pending", "scope_id": initial.ScopeID, "source_scope_id": target.SourceScopeID, "target_binding_sha256": target.BindingSHA256, "SQL_proof_sha256": result.ProofSHA256, "steps": steps, "restored_counts": value, "declared_fresh_expectations": expected, "qualification": "actual_local_amd64_emulated_SQL_scope_only_not_schema_access_auth_stores_browser_or_native_target"}))
		return
	}
	// Extension installation is an explicit operator prerequisite from M12,
	// performed only in this newly created disposable database, before migration.
	catalogRaw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_APPLICATION_POSTGRES_CATALOG"))
	if e != nil {
		t.Fatal("approved PostgreSQL catalog required", e)
	}
	var catalog struct {
		Version   int    `json:"postgres_version_number"`
		Extension string `json:"extension_version"`
		Platforms map[string]struct {
			Manifest string `json:"manifest_digest"`
		} `json:"platforms"`
	}
	if wire.UniqueJSON(catalogRaw, &catalog) != nil || catalog.Version < 1 || catalog.Extension == "" || len(catalog.Extension) > 64 || catalog.Platforms["linux-amd64"].Manifest != postgres.Image {
		t.Fatal("approved PostgreSQL catalog mismatch")
	}
	prerequisiteBefore, e := query("SHOW server_version_num;SELECT count(*) FROM pg_catalog.pg_extension WHERE extname='vector'")
	if e != nil || prerequisiteBefore != fmt.Sprintf("%d\n0", catalog.Version) {
		t.Fatal("fresh exact database prerequisite required", e)
	}
	versionLiteral := "'" + strings.ReplaceAll(catalog.Extension, "'", "''") + "'"
	if _, e = query("CREATE EXTENSION vector WITH SCHEMA public VERSION " + versionLiteral + ";REVOKE CREATE ON SCHEMA public FROM PUBLIC"); e != nil {
		t.Fatal("explicit isolated vector prerequisite", e)
	}
	prerequisiteAfter, e := query("SELECT e.extversion||'|'||n.nspname FROM pg_catalog.pg_extension e JOIN pg_catalog.pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='vector';SELECT public.vector_dims('[1,2,3]'::public.vector);SELECT count(*) FROM pg_catalog.pg_namespace n CROSS JOIN LATERAL pg_catalog.aclexplode(COALESCE(n.nspacl,pg_catalog.acldefault('n',n.nspowner))) a WHERE n.nspname='public' AND a.grantee=0 AND a.privilege_type='CREATE'")
	if e != nil || prerequisiteAfter != catalog.Extension+"|public\n3\n0" {
		t.Fatal("actual vector/schema prerequisite verification", e)
	}
	put(t, output+".database-prerequisite.json", encode(map[string]any{"status": "passed_actual_explicit_fresh_scope_operator_prerequisite", "server_version_number": catalog.Version, "extension_version": catalog.Extension, "schema": "public", "vector_dimensions": 3, "public_create_grants": 0, "new_database_extension_count_before": 0, "not_automatic_installation_or_customer_mutation": true}))
	for _, prefix := range []string{"database-migration", "database-schema", "runtime-access", "runtime-access-check", "bootstrap", "bootstrap-check", "dataset-store", "dataset-store-check"} {
		for _, action := range []string{"create", "start", "remove"} {
			cli(action + "-" + prefix + "-task")
		}
	}
	var qualified Status
	if json.Unmarshal(cli("status"), &qualified) != nil || !qualified.SchemaQualified || !qualified.RuntimeAccessQualified || !qualified.BootstrapQualified || !qualified.Stores["datasets"].Qualified {
		t.Fatal("actual independent qualification missing")
	}
	credentialRaw, e := os.ReadFile(filepath.Join(c.ScopeRoot, bootstrapCopyName))
	if e != nil {
		t.Fatal(e)
	}
	var credential struct {
		Password string `json:"password"`
	}
	if json.Unmarshal(credentialRaw, &credential) != nil || credential.Password == "" {
		t.Fatal("protected bootstrap handoff")
	}
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, Proxy: nil}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, origin, path, token string, payload any) (int, []byte) {
		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(encode(payload))
		}
		req, e := http.NewRequestWithContext(ctx, method, origin+path, body)
		if e != nil {
			t.Fatal(e)
		}
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if method != "GET" {
			req.Header.Set("Origin", origin)
			req.Header.Set("X-CSRF-Token", token)
		}
		response, e := httpClient.Do(req)
		if e != nil {
			t.Fatal("actual TLS request", e)
		}
		defer response.Body.Close()
		value, e := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, value
	}
	csrf := func(origin string) string {
		code, value := request("GET", origin, "/api/v1/auth/csrf", "", nil)
		var response struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		if code != 200 || json.Unmarshal(value, &response) != nil || response.Data.Token == "" {
			t.Fatal("actual CSRF response invalid", code)
		}
		return response.Data.Token
	}
	ready := func() {
		for deadline := time.Now().Add(time.Minute); ; {
			response, e := httpClient.Get(portal + "/health/ready")
			if e == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("actual full application TLS readiness timeout")
			}
			time.Sleep(250 * time.Millisecond)
		}
	}
	cli("start-runtime")
	ready()
	applicationReady = true
	login := func(origin string) {
		code, _ := request("POST", origin, "/api/v1/auth/login", csrf(origin), map[string]any{"email": "owner@example.invalid", "password": credential.Password})
		if code != 200 {
			t.Fatal("actual bootstrap login", code)
		}
	}
	login(admin)
	if code, _ := request("GET", portal, "/api/v1/me", "", nil); code != 401 {
		t.Fatal("admin cookie crossed Portal audience", code)
	}
	login(portal)
	if code, _ := request("GET", portal, "/api/v1/admin/users", "", nil); code < 400 {
		t.Fatal("Portal authorized administration")
	}
	if code, _ := request("POST", portal, "/api/v1/auth/logout", "", nil); code != 403 {
		t.Fatal("missing CSRF accepted", code)
	}
	if code, _ := request("GET", portal, "/api/v1/workspaces/00000000-0000-4000-8000-000000000000", "", nil); code != 404 {
		t.Fatal("foreign Workspace disclosed", code)
	}
	before, e := query("SELECT count(*) FROM schema_migrations;SELECT count(*) FROM users;SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'")
	if e != nil || before != "16\n1\n1" {
		t.Fatal("actual SQL baseline", e)
	}
	browserConfig := browserConfigName(parent)
	for _, phase := range []string{"before-restart", "after-restart"} {
		if phase == "after-restart" {
			cli("stop-runtime")
			cli("start-database")
			for deadline := time.Now().Add(time.Minute); ; {
				if value, e := query("SELECT 1"); e == nil && value == "1" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("restarted database readiness")
				}
				time.Sleep(200 * time.Millisecond)
			}
			cli("start-runtime")
			ready()
		}
		put(t, browserConfig, encode(map[string]any{"portal": portal, "admin": admin, "admin_email": "owner@example.invalid", "admin_password": credential.Password, "spki": spki, "phase": phase, "output": output + "." + phase + ".browser.json"}))
		command := exec.CommandContext(ctx, "node", os.Getenv("PROJECT_ONE_TEST_APPLICATION_BROWSER"), browserConfig)
		value, e := command.CombinedOutput()
		if e != nil {
			t.Fatalf("actual browser %s failed: %v %s", phase, e, value)
		}
		steps = append(steps, "actual_browser_"+phase)
	}
	after, e := query("SELECT count(*) FROM schema_migrations;SELECT count(*) FROM users;SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'")
	if e != nil || before != after {
		t.Fatal("identity/schema/bootstrap changed across restart", e)
	}
	put(t, output, encode(map[string]any{"status": "passed_actual_current_application_core_profile_and_recorded_restart", "scope_id": initial.ScopeID, "steps": steps, "control_image": control.Image, "archive_sha256": control.Archive.SHA, "portal_origin": portal, "admin_origin": admin, "tls": "actual_exact_ephemeral_CA_no_shared_trust_changes", "runtime_credentials": "separate_restricted_SQL_role_no_operator_or_bootstrap_mount", "localization": "embedded_English_seed_only_no_Google_or_review_claim", "qualification": "macOS_Docker_Desktop_amd64_emulation_core_application_only_not_full_release_or_target", "optional_profiles_pending": []string{"training", "preparation", "gateway", "SMTP", "full_offline_locales", "backup_restore_upgrade"}}))
}

func browserConfigName(parent string) string { return filepath.Join(parent, "browser.json") }

func applicationCertificate(t *testing.T) ([]byte, []byte, *x509.CertPool, string) {
	t.Helper()
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if e != nil {
		t.Fatal(e)
	}
	certificate := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Synthetic isolated application acceptance"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true}
	der, e := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	keyDER, e := x509.MarshalPKCS8PrivateKey(key)
	if e != nil {
		t.Fatal(e)
	}
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	digest := sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), pool, base64.StdEncoding.EncodeToString(digest[:])
}
