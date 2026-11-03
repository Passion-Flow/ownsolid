//go:build offlineintegration

package scope

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type lostDatabaseStartResponse struct{ *compose.Client }

type lostDatabaseTaskCreateResponse struct{ *engine.Client }

type lostDatabaseTaskActionResponse struct{ *engine.Client }

func (c lostDatabaseTaskActionResponse) StartDatabaseTask(ctx context.Context, task engine.DatabaseTask, id string, files []engine.ConfigurationFile, reserve int) (engine.ResourceAdmission, error) {
	value, e := c.Client.StartDatabaseTask(ctx, task, id, files, reserve)
	if e == nil {
		os.Exit(89)
	}
	return value, e
}
func (c lostDatabaseTaskActionResponse) RemoveDatabaseTask(ctx context.Context, task engine.DatabaseTask, id string) error {
	e := c.Client.RemoveDatabaseTask(ctx, task, id)
	if e == nil {
		os.Exit(88)
	}
	return e
}
func TestActualUnfinishedDatabaseMigrationActionChild(t *testing.T) {
	target := os.Getenv("PROJECT_ONE_TEST_DATABASE_ACTION_CHILD_TARGET")
	if target == "" {
		return
	}
	if target != "execute" && target != "removed" {
		t.Fatal("invalid isolated child target")
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DATABASE_ACTION_CHILD_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil {
		t.Fatal(e)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	kind := os.Getenv("PROJECT_ONE_TEST_DATABASE_ACTION_CHILD_KIND")
	if kind == "" {
		kind = engine.DatabaseMigrate
	}
	if _, e = databaseTaskAction(ctx, c, lostDatabaseTaskActionResponse{client}, worker, target, false, kind); e != nil {
		t.Fatal(e)
	}
	t.Fatal("expected controlled action process loss")
}

func (c lostDatabaseTaskCreateResponse) CreateDatabaseTask(ctx context.Context, task engine.DatabaseTask) (engine.RuntimeContainer, error) {
	value, e := c.Client.CreateDatabaseTask(ctx, task)
	if e == nil {
		os.Exit(90)
	}
	return value, e
}
func TestActualUnfinishedDatabaseMigrationCreateChild(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DATABASE_TASK_CREATE_CHILD") != "1" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DATABASE_TASK_CREATE_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil {
		t.Fatal(e)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	kind := os.Getenv("PROJECT_ONE_TEST_DATABASE_TASK_CREATE_KIND")
	if kind == "" {
		kind = engine.DatabaseMigrate
	}
	if _, e = createDatabaseTask(ctx, c, lostDatabaseTaskCreateResponse{client}, worker, kind, false); e != nil {
		t.Fatal(e)
	}
	t.Fatal("expected controlled migration task creation loss90")
}

func (w lostDatabaseStartResponse) StartDatabaseRuntime(ctx context.Context, r compose.RuntimeReader, s compose.RenderContext, m compose.Model, known compose.RuntimeStatus) (compose.RuntimeStatus, error) {
	value, e := w.Client.StartDatabaseRuntime(ctx, r, s, m, known)
	if e == nil {
		os.Exit(91)
	}
	return value, e
}
func TestActualUnfinishedDatabaseDependencyStartChild(t *testing.T) {
	if os.Getenv("PROJECT_ONE_TEST_DATABASE_START_CHILD") != "1" {
		return
	}
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DATABASE_START_CHILD_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	c, e := configuration.Load(raw, nil)
	if e != nil || c.Engine == nil {
		t.Fatal(e)
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, e = processRuntimeSelected(ctx, c, client, lostDatabaseStartResponse{worker}, "running", false, []string{"postgres"}); e != nil {
		t.Fatal(e)
	}
	t.Fatal("expected controlled database start loss91")
}
func TestActualSignedDatabaseDependencyStartSQLAndPersistence(t *testing.T) {
	actualSignedDatabase(t, false)
}
func TestActualSignedDatabaseMigrationAndSchemaCheck(t *testing.T) {
	actualSignedDatabase(t, true)
}
func TestActualSignedScopedDatabaseMigrationCreation(t *testing.T) {
	actualSignedDatabase(t, true, true)
}
func TestActualSignedScopedDatabaseMigrationLifecycle(t *testing.T) {
	actualSignedDatabase(t, true, true, true)
}
func TestActualSignedScopedSchemaQualification(t *testing.T) {
	actualSignedDatabase(t, true, true, true, true)
}
func TestActualSignedRuntimeAccessTaskPrimitives(t *testing.T) {
	actualSignedDatabase(t, true, false, false, false, true)
}
func TestActualSignedScopedRuntimeAccessQualification(t *testing.T) {
	actualSignedDatabase(t, true, true, true, true, true)
}
func TestActualSignedScopedStoreQualification(t *testing.T) {
	actualSignedDatabase(t, true, true, true, true, true, true, true)
}
func TestActualSignedStoreTaskPrimitives(t *testing.T) {
	actualSignedDatabase(t, true, false, false, false, true, true, true)
}
func TestActualSignedScopedBootstrapQualification(t *testing.T) {
	actualSignedDatabase(t, true, true, true, true, true, true)
}
func TestActualSignedBootstrapTaskPrimitives(t *testing.T) {
	actualSignedDatabase(t, true, false, false, false, true, true)
}
func actualSignedDatabase(t *testing.T, withTasks bool, scoped ...bool) {
	withScope := len(scoped) > 0 && scoped[0]
	withLifecycle := len(scoped) > 1 && scoped[1]
	withSchema := len(scoped) > 2 && scoped[2]
	withRuntimeAccess := len(scoped) > 3 && scoped[3]
	withBootstrap := len(scoped) > 4 && scoped[4]
	withStores := len(scoped) > 5 && scoped[5]
	storeNamespaces := map[string]engine.StoreTaskObservation{}
	var bootstrapIntentSHA string
	var bootstrapIdentity string
	var qualifiedLedger string
	var verifyRuntimeAfterRestart func()
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()
	raw, e := os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DATABASE_CONTROL_FIXTURE"))
	if e != nil {
		t.Fatal(e)
	}
	var control struct {
		Status, Archive string
		SHA             string `json:"archive_sha256"`
		Image           string `json:"image_manifest"`
		Config          string `json:"image_config"`
	}
	if wire.UniqueJSON(raw, &control) != nil || control.Status != "manufactured_real_control_command_fixture_not_release" {
		t.Fatal("exact control fixture required")
	}
	raw, e = os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DATABASE_POSTGRES_MANIFEST"))
	if e != nil {
		t.Fatal(e)
	}
	var postgres struct {
		Status, Architecture string
		Image                string `json:"image_id"`
		Config               string `json:"image_config_digest"`
		Archive              struct {
			Path  string `json:"path"`
			Bytes int64  `json:"size_bytes"`
			SHA   string `json:"sha256"`
		}
	}
	if wire.UniqueJSON(raw, &postgres) != nil || postgres.Status != "passed_actual_declared_image_archive_and_full_blob_readback" || postgres.Architecture != "amd64" || postgres.Archive.Bytes > 256<<20 {
		t.Fatal("exact approved postgres archive required")
	}
	controlBytes, e := os.ReadFile(control.Archive)
	if e != nil || len(controlBytes) > 64<<20 || wire.Digest(controlBytes) != control.SHA {
		t.Fatal("control archive changed", e)
	}
	postgresBytes, e := os.ReadFile(postgres.Archive.Path)
	if e != nil || int64(len(postgresBytes)) != postgres.Archive.Bytes || wire.Digest(postgresBytes) != postgres.Archive.SHA {
		t.Fatal("postgres archive changed", e)
	}
	binary := os.Getenv("PROJECT_ONE_TEST_DATABASE_OPERATOR")
	binaryBytes, e := os.ReadFile(binary)
	if e != nil || wire.Digest(binaryBytes) != os.Getenv("PROJECT_ONE_TEST_DATABASE_OPERATOR_SHA256") {
		t.Fatal("current operator binary required")
	}
	raw, e = os.ReadFile(os.Getenv("PROJECT_ONE_TEST_DATABASE_CONFIG"))
	if e != nil {
		t.Fatal(e)
	}
	profile, e := configuration.Load(raw, nil)
	if e != nil || profile.Engine == nil || profile.Engine.Utility == nil || profile.Compose == nil || profile.Compose.Process == nil {
		t.Fatal(e)
	}
	assets := []wire.Asset{{ID: "control-initializer", Path: "images/control.tar", Kind: "image_archive", Size: int64(len(controlBytes)), SHA256: control.SHA, ImageID: control.Image, ImageConfigDigest: control.Config, Architecture: "amd64", Licenses: []string{"license"}}, {ID: "postgres", Path: "images/postgres.tar", Kind: "image_archive", Size: int64(len(postgresBytes)), SHA256: postgres.Archive.SHA, ImageID: postgres.Image, ImageConfigDigest: postgres.Config, Architecture: "amd64", Licenses: []string{"license"}}}
	c := fixtureWithAssets(t, assets, [][]byte{controlBytes, postgresBytes})
	if withBootstrap {
		c.MaximumJournalBytes = 1 << 20
	}
	c.Engine = profile.Engine
	if withBootstrap {
		c.Engine.Utility.MemoryBytes = 256 << 20
	}
	c.Compose = profile.Compose
	c.TimeoutSeconds = 180
	parent := filepath.Dir(c.ScopeRoot)
	c.Compose.ModelFile = filepath.Join(parent, "model.json")
	c.Compose.InputsFile = filepath.Join(parent, "sources.json")
	secret := make([]byte, 32)
	if _, e = rand.Read(secret); e != nil {
		t.Fatal(e)
	}
	passwordFile := filepath.Join(parent, "postgres-password")
	put(t, passwordFile, []byte(base64.RawURLEncoding.EncodeToString(secret)))
	const databaseName = "projectone_m17_test"
	model := compose.Model{Services: map[string]compose.Service{
		"control":  {Image: control.Image, User: "10033:10034", MemoryBytes: 64 << 20, MilliCPU: 100, PIDs: 16, Restart: "no", ReadOnly: true},
		"postgres": {Image: postgres.Image, User: "999:999", MemoryBytes: 512 << 20, MilliCPU: 1000, PIDs: 64, Restart: "no", ReadOnly: true, Environment: map[string]string{"POSTGRES_USER": databaseName, "POSTGRES_DB": databaseName, "POSTGRES_PASSWORD_FILE": "/run/projectone/postgres/private/postgres-password", "PGDATA": "/var/lib/postgresql/private/data"}, Mounts: []compose.Mount{{Type: "volume", Source: "postgres-config", Target: "/run/projectone/postgres", ReadOnly: true}, {Type: "volume", Source: "postgres", Target: "/var/lib/postgresql"}}, Tmpfs: []compose.Tmpfs{{Target: "/var/run/postgresql", SizeBytes: 16 << 20, UID: 999, GID: 999}, {Target: "/tmp", SizeBytes: 16 << 20, UID: 999, GID: 999}}},
	}, Volumes: []string{"postgres-config", "postgres"}}
	roles := []SourceRole{{Key: "postgres-config", Files: []SourceFile{{Name: "postgres-password", SourceFile: passwordFile}}}, {Key: "postgres"}}
	var taskFiles []engine.ConfigurationFile
	if withTasks {
		configBytes := encode(map[string]any{"server": map[string]any{"listen_address": "127.0.0.1:18443", "portal_origin": "https://portal.example.invalid", "admin_origin": "https://admin.example.invalid"}, "database": map[string]any{"dsn_file": engine.DatabaseTaskDestination + "/private/operator-dsn", "max_open_connections": 4, "max_idle_connections": 1, "statement_timeout_seconds": 5}, "smtp": map[string]any{"host": "smtp.example.invalid", "port": 587, "from_address": "platform@example.invalid", "tls_mode": "starttls_required", "connect_timeout_seconds": 5, "operation_timeout_seconds": 20}})
		if withBootstrap {
			var configuration map[string]any
			if json.Unmarshal(configBytes, &configuration) != nil {
				t.Fatal("bootstrap config")
			}
			configuration["security"] = map[string]any{"password_blocklist_file": engine.DatabaseTaskDestination + "/private/blocklist"}
			if withStores {
				configuration["models"] = map[string]any{"artifact_root": "/var/lib/projectone/models/private"}
				configuration["datasets"] = map[string]any{"artifact_root": "/var/lib/projectone/datasets/private", "python_executable": "/synthetic-unused/python", "processor_file": "/synthetic-unused/processor.py"}
				configuration["training"] = map[string]any{"artifact_root": "/var/lib/projectone/training/private", "image_reference": "projectone/training@sha256:" + strings.Repeat("a", 64), "image_id": "sha256:" + strings.Repeat("b", 64), "worker_source_sha256": strings.Repeat("c", 64)}
				configuration["model_preparation"] = map[string]any{"artifact_root": "/var/lib/projectone/preparations/private", "image_reference": "projectone/model-lifecycle@sha256:" + strings.Repeat("a", 64), "image_id": "sha256:" + strings.Repeat("b", 64), "worker_source_sha256": strings.Repeat("c", 64)}
			}

			configBytes = encode(configuration)
		}
		u := url.URL{Scheme: "postgres", User: url.UserPassword(databaseName, base64.RawURLEncoding.EncodeToString(secret)), Host: "postgres:5432", Path: "/" + databaseName, RawQuery: "sslmode=disable"}
		taskFiles = []engine.ConfigurationFile{{Name: "control.yaml", Contents: configBytes}, {Name: "operator-dsn", Contents: []byte(u.String())}}
		if withRuntimeAccess {
			runtimeSecret := make([]byte, 32)
			if _, e = rand.Read(runtimeSecret); e != nil {
				t.Fatal(e)
			}
			u.User = url.UserPassword(databaseName+"_runtime", base64.RawURLEncoding.EncodeToString(runtimeSecret))
			var runtimeConfiguration map[string]any
			if json.Unmarshal(configBytes, &runtimeConfiguration) != nil {
				t.Fatal("runtime fixture configuration")
			}
			runtimeConfiguration["database"].(map[string]any)["dsn_file"] = engine.DatabaseTaskDestination + "/private/runtime-dsn"
			taskFiles = append(taskFiles, engine.ConfigurationFile{Name: "runtime.yaml", Contents: encode(runtimeConfiguration)}, engine.ConfigurationFile{Name: "runtime-dsn", Contents: []byte(u.String())})
		}
		if withBootstrap {
			taskFiles = append(taskFiles, engine.ConfigurationFile{Name: "bootstrap-request.json", Contents: []byte(`{"format_version":1,"administrator_email":"owner@example.invalid","display_name":"Owner","installation_name":"Installation","workspace_name":"Workspace"}`)}, engine.ConfigurationFile{Name: "blocklist", Contents: []byte("passwordpassword\n")})
			roles = append(roles, SourceRole{Key: engine.BootstrapStateRole})
			model.Volumes = append(model.Volumes, engine.BootstrapStateRole)
		}
		if withStores {
			taskFiles = append(taskFiles, engine.ConfigurationFile{Name: "store-request.json", Contents: encode(StoreRequest{FormatVersion: 1, Reason: "Synthetic scoped namespace initialization"})})
		}
		sources := []SourceFile{}
		for _, file := range taskFiles {
			p := filepath.Join(parent, "task-"+file.Name)
			put(t, p, file.Contents)
			sources = append(sources, SourceFile{Name: file.Name, SourceFile: p})
		}
		roles = append(roles, SourceRole{Key: "operator-config", Files: sources})
		model.Volumes = append(model.Volumes, "operator-config")
	}
	if withStores {
		controlService := model.Services["control"]
		for _, role := range []string{"datasets", "training", "preparations"} {
			roles = append(roles, SourceRole{Key: role})
			model.Volumes = append(model.Volumes, role)
			controlService.Mounts = append(controlService.Mounts, compose.Mount{Type: "volume", Source: role, Target: "/var/lib/projectone/" + role})
		}
		model.Services["control"] = controlService
	}
	put(t, c.Compose.ModelFile, encode(model))
	put(t, c.Compose.InputsFile, encode(SourceCatalog{FormatVersion: 1, StorageProfile: namedVolumeStorage, ImageAssetID: "control-initializer", Roles: roles}))
	configurationFile := filepath.Join(parent, "operator.json")
	put(t, configurationFile, encode(c))
	output := os.Getenv("PROJECT_ONE_TEST_DATABASE_EVIDENCE")
	client, e := engine.New(*c.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		t.Fatal(e)
	}
	if _, found, e := client.Inspect(ctx, control.Image); e != nil || found {
		t.Fatal("initially absent disposable control fixture required", e)
	}
	if image, found, e := client.Inspect(ctx, postgres.Image); e != nil || !found || image.Architecture != "amd64" {
		t.Fatal("shared approved cached amd64 PostgreSQL required", e)
	}
	cli := func(command string, success bool) []byte {
		out, e := exec.CommandContext(ctx, binary, command, "--config", configurationFile).CombinedOutput()
		if (e == nil) != success {
			t.Fatalf("actual CLI %s %v %s", command, e, out)
		}
		return out
	}
	var initial Status
	if json.Unmarshal(cli("init-scope", true), &initial) != nil {
		t.Fatal("scope creation")
	}
	render := compose.RenderContext{ScopeID: initial.ScopeID, ScopeRoot: c.ScopeRoot, AllowedImages: map[string]bool{control.Image: true, postgres.Image: true}}
	private := filepath.Join(parent, "docker-client")
	if e = os.Mkdir(private, 0700); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(private, "config.json"), []byte("{}\n"))
	docker := func(commandContext context.Context, args ...string) ([]byte, error) {
		command := exec.CommandContext(commandContext, c.Compose.DockerBinary, append([]string{"--config", private, "--host", "unix://" + c.Engine.SocketFile}, args...)...)
		command.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
		return command.CombinedOutput()
	}
	cleaned := false
	var activeTask *engine.DatabaseTask
	var taskID string
	taskResults := []map[string]any{}
	cleanup := func() error {
		cleanupContext, done := context.WithTimeout(context.Background(), 45*time.Second)
		defer done()
		if activeTask != nil {
			value, found, e := client.InspectDatabaseTask(cleanupContext, *activeTask)
			if e != nil {
				return e
			}
			if found {
				if taskID != "" && value.ID != taskID {
					return failure("DATABASE_TEST_TASK_ID_CHANGED")
				}
				if value.State.Running {
					if e = client.StopDatabaseTask(cleanupContext, *activeTask, value.ID, 2); e != nil {
						return e
					}
				}
				if e = client.RemoveDatabaseTask(cleanupContext, *activeTask, value.ID); e != nil {
					return e
				}
			}
			activeTask = nil
			taskID = ""
		}
		l, e := open(c, false)
		if e != nil {
			return e
		}
		events := append([]Event(nil), l.events...)
		l.close()
		if e = os.WriteFile(output+".journal.json", encodeJournal(appendJournal(events)), 0600); e != nil {
			return e
		}
		observed, e := worker.ObserveRuntime(cleanupContext, client, render, model)
		if e != nil {
			return e
		}
		if len(observed.Services) > 0 {
			if _, e = worker.StopRuntime(cleanupContext, client, render, model, observed); e != nil {
				return e
			}
			for _, service := range observed.Services {
				if _, e = docker(cleanupContext, "container", "rm", service.ID); e != nil {
					return failure("DATABASE_TEST_CONTAINER_CLEANUP_FAILED")
				}
			}
		}
		observed, e = worker.ObserveRuntime(cleanupContext, client, render, model)
		if e != nil || len(observed.Services) > 0 {
			return failure("DATABASE_TEST_CONTAINER_REMOVAL_UNCONFIRMED")
		}
		if observed.NetworkID != "" {
			if _, e = docker(cleanupContext, "network", "rm", observed.NetworkID); e != nil {
				return failure("DATABASE_TEST_NETWORK_CLEANUP_FAILED")
			}
		}
		for _, r := range volumeState(events).roles {
			o := engine.Ownership{ScopeID: initial.ScopeID, OperationID: r.ResourceOperationID, Role: r.Key}
			if _, found, e := client.InspectVolume(cleanupContext, o); e != nil || !found {
				return failure("DATABASE_TEST_VOLUME_IDENTITY_CHANGED")
			}
			if e = client.VerifyVolumeConsumers(cleanupContext, o, nil); e != nil {
				return e
			}
			if _, e = docker(cleanupContext, "volume", "rm", o.VolumeName()); e != nil {
				return failure("DATABASE_TEST_VOLUME_CLEANUP_FAILED")
			}
		}
		if image, found, e := client.Inspect(cleanupContext, control.Image); e != nil {
			return e
		} else if found {
			if len(image.RepoTags) > 0 {
				return failure("DATABASE_TEST_NAMED_CONTROL_IMAGE_PRESERVED")
			}
			consumers, e := docker(cleanupContext, "container", "ls", "--all", "--quiet", "--filter", "ancestor="+control.Image)
			if e != nil || len(strings.TrimSpace(string(consumers))) > 0 {
				return failure("DATABASE_TEST_IMAGE_CONSUMER_PRESERVED")
			}
			if _, e = docker(cleanupContext, "image", "rm", "--no-prune", control.Image); e != nil {
				return failure("DATABASE_TEST_CONTROL_IMAGE_CLEANUP_FAILED")
			}
		}
		cleaned = true
		return nil
	}
	t.Cleanup(func() {
		if !cleaned {
			if e := cleanup(); e != nil {
				t.Error(e)
			}
		}
	})
	for _, command := range []string{"import-images", "prepare-storage-inputs", "initialize-storage", "create-runtime"} {
		cli(command, true)
	}
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActualUnfinishedDatabaseDependencyStartChild$", "-test.timeout=70s")
	child.Env = append(os.Environ(), "PROJECT_ONE_TEST_DATABASE_START_CHILD=1", "PROJECT_ONE_TEST_DATABASE_START_CHILD_CONFIG="+configurationFile)
	childOutput, e := child.CombinedOutput()
	exit, ok := e.(*exec.ExitError)
	if !ok || exit.ExitCode() != 91 {
		t.Fatalf("actual database start loss91: %v %s", e, childOutput)
	}
	cli("start-database", false)
	var running ProcessResult
	if json.Unmarshal(cli("reconcile-process", true), &running) != nil || running.Pending || running.Resources.Services["postgres"].State != "running" || running.Resources.Services["control"].State != "created" {
		t.Fatal("selected start reconciliation")
	}
	pgID := running.Resources.Services["postgres"].ID
	query := func(statement string) (string, error) {
		queryContext, done := context.WithTimeout(ctx, 20*time.Second)
		defer done()
		raw, e := docker(queryContext, "exec", "--user", "999:999", pgID, "psql", "-X", "--set", "ON_ERROR_STOP=1", "-U", databaseName, "-d", databaseName, "-At", "-c", statement)
		return strings.TrimSpace(string(raw)), e
	}
	ready := func() {
		for deadline := time.Now().Add(60 * time.Second); ; {
			value, e := query("SELECT 1")
			if e == nil && value == "1" {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("actual isolated PostgreSQL readiness deadline")
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	ready()
	version, e := query("SHOW server_version_num")
	if e != nil || version != "180006" {
		t.Fatal("actual PostgreSQL version", version, e)
	}
	if _, e = query("CREATE EXTENSION vector; CREATE TABLE m17_dependency_probe(id integer PRIMARY KEY,value text NOT NULL); INSERT INTO m17_dependency_probe VALUES(1,'Synthetic durable dependency row')"); e != nil {
		t.Fatal("synthetic database initialization", e)
	}
	assertSQL := func() {
		value, e := query("SELECT extversion FROM pg_extension WHERE extname='vector'; SELECT vector_dims('[1,2,3]'::vector); SELECT value FROM m17_dependency_probe WHERE id=1")
		if e != nil || value != "0.8.7\n3\nSynthetic durable dependency row" {
			t.Fatal("actual vector/persistence assertions", value, e)
		}
	}
	assertSQL()
	if withTasks && !withScope {
		l, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		v := volumeState(l.events).roles["operator-config"]
		bootstrapVolume := volumeState(l.events).roles[engine.BootstrapStateRole]
		storeVolumes := volumeState(l.events).roles
		l.close()
		volumeOwner := engine.Ownership{ScopeID: initial.ScopeID, OperationID: v.ResourceOperationID, Role: v.Key}
		runTask := func(kind string, wantExit int) {
			op, e := nonce()
			if e != nil {
				t.Fatal(e)
			}
			task := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: initial.ScopeID, OperationID: op, Role: "database-" + kind}, VolumeOwner: volumeOwner, Kind: kind, Image: control.Image, UID: 10033, GID: 10034, NetworkID: running.Resources.NetworkID, Peers: map[string]string{"control": running.Resources.Services["control"].ID, "postgres": pgID}}
			if kind == engine.DatabaseBootstrap || kind == engine.DatabaseBootstrapReconcile || kind == engine.DatabaseBootstrapResume {
				task.OutputOwner = &engine.Ownership{ScopeID: initial.ScopeID, OperationID: bootstrapVolume.ResourceOperationID, Role: engine.BootstrapStateRole}
			}
			if withStores && (strings.Contains(kind, "dataset-store") || strings.Contains(kind, "training-store") || strings.Contains(kind, "preparation-store")) {
				role := "datasets"
				if strings.Contains(kind, "training") {
					role = "training"
				}
				if strings.Contains(kind, "preparation") {
					role = "preparations"
				}
				v := storeVolumes[role]
				task.OutputOwner = &engine.Ownership{ScopeID: initial.ScopeID, OperationID: v.ResourceOperationID, Role: role}
				if !strings.HasPrefix(kind, "check-") {
					task.Reason = "Synthetic offline namespace initialization"
				}
			}
			activeTask = &task
			taskID = ""
			created, e := client.CreateDatabaseTask(ctx, task)
			taskID = created.ID
			if e != nil {
				t.Fatal("actual database task creation", e)
			}
			input, e := client.InspectDatabaseTaskInput(ctx, task, created.ID, taskFiles)
			if e != nil {
				t.Fatal("actual private task input", e)
			}
			admission, e := client.StartDatabaseTask(ctx, task, created.ID, taskFiles, 30)
			if e != nil {
				t.Fatal("actual database task start", e)
			}
			var observed engine.RuntimeContainer
			for deadline := time.Now().Add(30 * time.Second); ; {
				var found bool
				observed, found, e = client.InspectDatabaseTask(ctx, task)
				if e != nil || !found {
					t.Fatal("actual task inspection", e)
				}
				if observed.State.Status == "exited" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("actual database task deadline")
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
			if observed.State.ExitCode != wantExit || observed.State.OOMKilled {
				t.Fatal("actual task exit", kind, observed.State.ExitCode, observed.State.OOMKilled)
			}
			if _, e = client.StartDatabaseTask(ctx, task, created.ID, taskFiles, 30); e == nil {
				t.Fatal("database task reexecuted")
			}
			if _, e = client.CreateDatabaseTask(ctx, task); e == nil {
				t.Fatal("database task recreated")
			}
			if task.OutputOwner != nil && task.OutputOwner.Role == engine.BootstrapStateRole {
				intent, e := client.ReadBootstrapIntent(ctx, task, created.ID)
				if e != nil || intent.Bytes < 1 {
					t.Fatal("private bootstrap result", e)
				}
				if bootstrapIntentSHA != "" && bootstrapIntentSHA != intent.SHA256 {
					t.Fatal("bootstrap replaced credential")
				}
				bootstrapIntentSHA = intent.SHA256
				public, e := json.Marshal(intent)
				if e != nil || strings.Contains(string(public), "password") || strings.Contains(string(public), "Contents") {
					t.Fatal("credential exposed in observation")
				}
			}
			if task.OutputOwner != nil && task.OutputOwner.Role != engine.BootstrapStateRole {
				observation, e := client.InspectStoreTaskResult(ctx, task, created.ID)
				if e != nil {
					t.Fatal("actual private namespace readback", kind, e)
				}
				if observation.Namespace.TenantID != strings.Split(bootstrapIdentity, ":")[0] {
					t.Fatal("store tenant differs from actual bootstrapped tenant")
				}
				if previous, ok := storeNamespaces[task.OutputOwner.Role]; ok && previous != observation {
					t.Fatal("store namespace changed", kind)
				}
				storeNamespaces[task.OutputOwner.Role] = observation
			}
			taskResults = append(taskResults, map[string]any{"kind": kind, "id": created.ID, "exit_code": observed.State.ExitCode, "started_at": observed.State.StartedAt, "input": input, "admission": admission})
			if e = client.RemoveDatabaseTask(ctx, task, created.ID); e != nil {
				t.Fatal("actual task removal", e)
			}
			activeTask = nil
			taskID = ""
		}
		runTask(engine.DatabaseMigrate, 0)
		ledger, e := query("SELECT count(*) FROM schema_migrations; SELECT checksum FROM schema_migrations WHERE version=1")
		if e != nil || !strings.HasPrefix(ledger, "16\n") {
			t.Fatal("actual migration ledger", ledger, e)
		}
		checksum := strings.TrimPrefix(ledger, "16\n")
		if len(checksum) != 64 || strings.Trim(checksum, "0123456789abcdef") != "" {
			t.Fatal("invalid fixture checksum")
		}
		runTask(engine.DatabaseSchemaCheck, 0)
		if _, e = query("UPDATE schema_migrations SET checksum=repeat('0',64) WHERE version=1"); e != nil {
			t.Fatal(e)
		}
		runTask(engine.DatabaseSchemaCheck, 1)
		runTask(engine.DatabaseMigrate, 1)
		value, e := query("SELECT checksum FROM schema_migrations WHERE version=1; SELECT count(*) FROM users")
		if e != nil || value != strings.Repeat("0", 64)+"\n0" {
			t.Fatal("failed task repaired data", value, e)
		}
		if _, e = query("UPDATE schema_migrations SET checksum='" + checksum + "' WHERE version=1"); e != nil {
			t.Fatal(e)
		}
		runTask(engine.DatabaseSchemaCheck, 0)
		assertSQL()
		if withRuntimeAccess {
			runTask(engine.DatabaseRuntimeCheck, 1)
			runTask(engine.DatabaseRuntimePrepare, 0)
			runTask(engine.DatabaseRuntimeCheck, 0)
			role, e := query("SELECT rolcanlogin AND NOT rolsuper AND NOT rolcreatedb AND NOT rolcreaterole AND NOT rolreplication AND NOT rolbypassrls AND NOT rolinherit AND rolconnlimit=4 FROM pg_roles WHERE rolname='" + databaseName + "_runtime'")
			if e != nil || role != "t" {
				t.Fatal("actual runtime role attributes", role, e)
			}
			assertSQL()
			verifyRuntimeAfterRestart = func() { runTask(engine.DatabaseRuntimeCheck, 0) }
			if withBootstrap {
				runTask(engine.DatabaseBootstrap, 0)
				runTask(engine.DatabaseBootstrap, 1)
				runTask(engine.DatabaseBootstrapReconcile, 0)
				runTask(engine.DatabaseBootstrapResume, 0)
				value, e := query("SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM tenants),(SELECT count(*) FROM workspaces),(SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'),(SELECT count(*) FROM sessions),(SELECT count(*) FROM mail_deliveries)")
				if e != nil || value != "1|1|1|1|0|0" {
					t.Fatal("actual bootstrap rows", value, e)
				}
				bootstrapIdentity, e = query("SELECT tenant_id::text||':'||default_workspace_id::text||':'||primary_admin_user_id::text FROM installation_state WHERE id=1 AND bootstrap_complete")
				if e != nil || bootstrapIdentity == "" {
					t.Fatal("bootstrap identity", e)
				}
				if withStores {
					for _, kinds := range [][3]string{{engine.DatasetStoreInitialize, engine.DatasetStoreCheck, engine.DatasetStoreRecord}, {engine.TrainingStoreInitialize, engine.TrainingStoreCheck, engine.TrainingStoreRecord}, {engine.PreparationStoreInitialize, engine.PreparationStoreCheck, engine.PreparationStoreRecord}} {
						runTask(kinds[0], 0)
						runTask(kinds[0], 1)
						runTask(kinds[1], 0)
						runTask(kinds[2], 0)
					}
					// Explicit synthetic missing-audit injection, confined to this new
					// disposable database. This is not a production cleanup procedure.
					if _, e := query("DELETE FROM audit_events WHERE action='dataset.store_initialize_verified'"); e != nil {
						t.Fatal(e)
					}
					runTask(engine.DatasetStoreCheck, 1)
					runTask(engine.DatasetStoreRecord, 0)
					runTask(engine.DatasetStoreCheck, 0)
					counts, e := query("SELECT action||':'||count(*)::text FROM audit_events WHERE action IN ('dataset.store_initialize_verified','training.store_initialize_verified','model.preparation_store_initialize_verified') GROUP BY action ORDER BY action")
					if e != nil || counts != "dataset.store_initialize_verified:1\nmodel.preparation_store_initialize_verified:1\ntraining.store_initialize_verified:1" {
						t.Fatal("store initialization audit counts", counts, e)
					}
				}
				verifyRuntimeAfterRestart = func() {
					runTask(engine.DatabaseRuntimeCheck, 0)
					runTask(engine.DatabaseBootstrapReconcile, 0)
					if withStores {
						for _, kind := range []string{engine.DatasetStoreCheck, engine.TrainingStoreCheck, engine.PreparationStoreCheck} {
							runTask(kind, 0)
						}
					}
					value, e := query("SELECT tenant_id::text||':'||default_workspace_id::text||':'||primary_admin_user_id::text FROM installation_state WHERE id=1 AND bootstrap_complete")
					if e != nil || value != bootstrapIdentity {
						t.Fatal("bootstrap identity changed after restart", e)
					}
				}
			}

		}
	}
	if withScope {
		child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActualUnfinishedDatabaseMigrationCreateChild$", "-test.timeout=70s")
		child.Env = append(os.Environ(), "PROJECT_ONE_TEST_DATABASE_TASK_CREATE_CHILD=1", "PROJECT_ONE_TEST_DATABASE_TASK_CREATE_CONFIG="+configurationFile)
		childOutput, e := child.CombinedOutput()
		exit, ok := e.(*exec.ExitError)
		if !ok || exit.ExitCode() != 90 {
			t.Fatalf("actual task create loss90: %v %s", e, childOutput)
		}
		// Recover exact generated ownership immediately, so a later assertion
		// failure can still clean this disposable task without a label-only guess.
		pendingLock, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		pendingRecord := databaseTaskState(pendingLock.events).record
		pendingLock.close()
		if pendingRecord == nil {
			t.Fatal("missing interrupted task ownership")
		}
		activeTask = &pendingRecord.Task
		taskID = pendingRecord.ID
		cli("create-database-migration-task", false)
		var result DatabaseTaskResult
		if json.Unmarshal(cli("reconcile-database-migration-task", true), &result) != nil || result.Pending || result.ID == "" {
			t.Fatal("actual task reconciliation")
		}
		l, e := open(c, false)
		if e != nil {
			t.Fatal(e)
		}
		record := databaseTaskState(l.events).record
		l.close()
		if record == nil || record.ID != result.ID {
			t.Fatal("actual task record missing")
		}
		activeTask = &record.Task
		taskID = record.ID
		observed, found, e := client.InspectDatabaseTask(ctx, record.Task)
		if e != nil || !found || observed.ID != record.ID || !neverStartedModelTask(observed) {
			t.Fatal("migration task was started", e)
		}
		if _, e = client.InspectDatabaseTaskInput(ctx, record.Task, record.ID, taskFiles); e != nil {
			t.Fatal("actual task private input", e)
		}
		if _, e = worker.ObserveRuntime(ctx, client, render, model); e == nil {
			t.Fatal("unfiltered runtime observer ignored operator task")
		}
		before, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if e != nil {
			t.Fatal(e)
		}
		cli("create-database-migration-task", true)
		cli("reconcile-database-migration-task", true)
		after, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if e != nil || string(before) != string(after) {
			t.Fatal("completed task creation repeated")
		}
		var status Status
		if json.Unmarshal(cli("status", true), &status) != nil || status.DatabaseTaskID != record.ID || status.DatabaseTaskPending || status.Status != "isolated_scope_never_started_database_task_recorded" {
			t.Fatal("actual recorded task status")
		}
		taskResults = append(taskResults, map[string]any{"kind": engine.DatabaseMigrate, "id": record.ID, "created_never_started": true, "loss_child_exit": 90, "reconciled_exact_id": true, "completed_repeat_unchanged": true, "original_runtime_observer_still_rejects_operator": true})
		if withLifecycle {
			lostChild := func(target string, wantExit int, kinds ...string) {
				child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActualUnfinishedDatabaseMigrationActionChild$", "-test.timeout=70s")
				child.Env = append(os.Environ(), "PROJECT_ONE_TEST_DATABASE_ACTION_CHILD_TARGET="+target, "PROJECT_ONE_TEST_DATABASE_ACTION_CHILD_CONFIG="+configurationFile)
				if len(kinds) == 1 {
					child.Env = append(child.Env, "PROJECT_ONE_TEST_DATABASE_ACTION_CHILD_KIND="+kinds[0])
				}
				childOutput, e := child.CombinedOutput()
				exit, ok := e.(*exec.ExitError)
				if !ok || exit.ExitCode() != wantExit {
					t.Fatalf("actual task action loss%d: %v %s", wantExit, e, childOutput)
				}
			}
			lostChild("execute", 89)
			cli("start-database-migration-task", false)
			for deadline := time.Now().Add(30 * time.Second); ; {
				observed, found, e = client.InspectDatabaseTask(ctx, record.Task)
				if e != nil || !found || observed.ID != record.ID {
					t.Fatal("actual migration observation", e)
				}
				if observed.State.Status == "exited" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("actual migration process deadline")
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
			if observed.State.ExitCode != 0 || observed.State.OOMKilled {
				t.Fatal("actual migration process failed", observed.State.ExitCode)
			}
			var action DatabaseTaskActionResult
			if json.Unmarshal(cli("reconcile-database-migration-action", true), &action) != nil || action.Pending || !action.ProcessSucceeded {
				t.Fatal("actual migration execution reconciliation")
			}
			ledger, e := query("SELECT count(*) FROM schema_migrations; SELECT count(*) FROM users")
			if e != nil || ledger != "16\n0" {
				t.Fatal("actual migration ledger", ledger, e)
			}
			before, e = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if e != nil {
				t.Fatal(e)
			}
			cli("start-database-migration-task", true)
			after, e = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if e != nil || string(before) != string(after) {
				t.Fatal("completed migration reexecuted")
			}
			startedAt := observed.State.StartedAt
			lostChild("removed", 88)
			cli("remove-database-migration-task", false)
			if json.Unmarshal(cli("reconcile-database-migration-action", true), &action) != nil || action.Pending || !action.Removed || !action.ProcessSucceeded {
				t.Fatal("actual migration removal reconciliation")
			}
			before, e = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if e != nil {
				t.Fatal(e)
			}
			cli("remove-database-migration-task", true)
			after, e = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
			if e != nil || string(before) != string(after) {
				t.Fatal("completed task removal repeated")
			}
			if !strings.Contains(string(cli("start-runtime", false)), "OFFLINE_SCOPE_INDEPENDENT_SCHEMA_QUALIFICATION_REQUIRED") {
				t.Fatal("missing independent schema startup gate")
			}
			if json.Unmarshal(cli("status", true), &status) != nil || !status.MigrationProcessSucceeded || !status.DatabaseTaskRemoved || status.DatabaseTaskPending {
				t.Fatal("actual migration process status")
			}
			taskResults = append(taskResults, map[string]any{"id": record.ID, "process_succeeded": true, "removed": true, "started_at": startedAt, "execution_loss_child_exit": 89, "removal_loss_child_exit": 88, "ordered_migration_versions": 16, "independent_schema_qualification_pending": true})
			if withSchema {
				cli("create-database-schema-task", true)
				l, e := open(c, false)
				if e != nil {
					t.Fatal(e)
				}
				schemaRecord := databaseTaskState(l.events).record
				l.close()
				if schemaRecord == nil || schemaRecord.Task.Kind != engine.DatabaseSchemaCheck || schemaRecord.MigrationSHA256 == "" {
					t.Fatal("schema predecessor proof missing")
				}
				activeTask = &schemaRecord.Task
				taskID = schemaRecord.ID
				if !strings.Contains(string(cli("start-database-migration-task", false)), "OFFLINE_SCOPE_DATABASE_TASK_KIND_MISMATCH") {
					t.Fatal("wrong kind command accepted")
				}
				var checked DatabaseTaskActionResult
				if json.Unmarshal(cli("start-database-schema-task", true), &checked) != nil || !checked.ProcessSucceeded || checked.SchemaQualified || checked.Pending {
					t.Fatal("independent check process result")
				}
				cli("start-runtime", false)
				if json.Unmarshal(cli("remove-database-schema-task", true), &checked) != nil || !checked.SchemaQualified || !checked.Removed || checked.Pending {
					t.Fatal("independent schema qualification result")
				}
				if json.Unmarshal(cli("status", true), &status) != nil || !status.SchemaQualified || !status.MigrationProcessSucceeded || !status.SchemaProcessSucceeded {
					t.Fatal("qualified status")
				}
				qualifiedLedger, e = query("SELECT version::text||':'||checksum FROM schema_migrations ORDER BY version")
				if e != nil || len(strings.Split(qualifiedLedger, "\n")) != 16 {
					t.Fatal("qualified ordered ledger", e)
				}
				taskResults = append(taskResults, map[string]any{"id": schemaRecord.ID, "kind": engine.DatabaseSchemaCheck, "migration_proof_sha256": schemaRecord.MigrationSHA256, "schema_qualified": true, "ordered_ledger_sha256": wire.Digest([]byte(qualifiedLedger))})
				if withRuntimeAccess {
					if !strings.Contains(string(cli("start-runtime", false)), "OFFLINE_SCOPE_RUNTIME_ACCESS_QUALIFICATION_REQUIRED") {
						t.Fatal("runtime access startup gate absent")
					}
					for _, step := range []struct{ kind, prefix, reconcile string }{{engine.DatabaseRuntimePrepare, "runtime-access", "reconcile-runtime-access-action"}, {engine.DatabaseRuntimeCheck, "runtime-access-check", "reconcile-runtime-access-check-action"}} {
						cli("create-"+step.prefix+"-task", true)
						l, e := open(c, false)
						if e != nil {
							t.Fatal(e)
						}
						accessRecord := databaseTaskState(l.events).record
						l.close()
						if accessRecord == nil || accessRecord.Task.Kind != step.kind || accessRecord.SchemaSHA256 == "" {
							t.Fatal("runtime access schema lineage missing")
						}
						activeTask = &accessRecord.Task
						taskID = accessRecord.ID
						lostChild("execute", 89, step.kind)
						cli("start-"+step.prefix+"-task", false)
						var process engine.RuntimeContainer
						for deadline := time.Now().Add(30 * time.Second); ; {
							var found bool
							process, found, e = client.InspectDatabaseTask(ctx, accessRecord.Task)
							if e != nil || !found || process.ID != accessRecord.ID {
								t.Fatal("access task observation", e)
							}
							if process.State.Status == "exited" {
								break
							}
							if time.Now().After(deadline) {
								t.Fatal("access task deadline")
							}
							select {
							case <-ctx.Done():
								t.Fatal(ctx.Err())
							case <-time.After(100 * time.Millisecond):
							}
						}
						if process.State.ExitCode != 0 || process.State.OOMKilled {
							t.Fatal("actual access process failed", step.kind, process.State.ExitCode)
						}
						var result DatabaseTaskActionResult
						if json.Unmarshal(cli(step.reconcile, true), &result) != nil || result.Pending || !result.ProcessSucceeded || !result.SchemaQualified || result.RuntimeAccessQualified {
							t.Fatal("access execution reconciliation")
						}
						before, e = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
						if e != nil {
							t.Fatal(e)
						}
						cli("start-"+step.prefix+"-task", true)
						after, e = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
						if e != nil || string(before) != string(after) {
							t.Fatal("access execution repeated")
						}
						lostChild("removed", 88, step.kind)
						cli("remove-"+step.prefix+"-task", false)
						if json.Unmarshal(cli(step.reconcile, true), &result) != nil || result.Pending || !result.Removed || result.RuntimeAccessQualified != (step.kind == engine.DatabaseRuntimeCheck) {
							t.Fatal("access removal reconciliation")
						}
						cli("remove-"+step.prefix+"-task", true)
						taskResults = append(taskResults, map[string]any{"id": accessRecord.ID, "kind": step.kind, "schema_proof_sha256": accessRecord.SchemaSHA256, "preparation_proof_sha256": accessRecord.AccessSHA256, "execution_loss_child_exit": 89, "removal_loss_child_exit": 88, "runtime_access_qualified": result.RuntimeAccessQualified})
					}
					if json.Unmarshal(cli("status", true), &status) != nil || !status.RuntimeAccessQualified || !status.SchemaQualified || !status.AccessPreparationSucceeded || !status.AccessCheckSucceeded {
						t.Fatal("historical access qualification status")
					}
					if withBootstrap {
						steps := []struct{ kind, prefix string }{{engine.DatabaseBootstrap, "bootstrap"}, {engine.DatabaseBootstrapReconcile, "bootstrap-check"}}
						if withStores {
							steps = append(steps, []struct{ kind, prefix string }{{engine.DatasetStoreInitialize, "dataset-store"}, {engine.DatasetStoreCheck, "dataset-store-check"}, {engine.TrainingStoreInitialize, "training-store"}, {engine.TrainingStoreCheck, "training-store-check"}, {engine.PreparationStoreInitialize, "preparation-store"}, {engine.PreparationStoreCheck, "preparation-store-check"}}...)
						}
						for _, step := range steps {
							child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestActualUnfinishedDatabaseMigrationCreateChild$", "-test.timeout=70s")
							child.Env = append(os.Environ(), "PROJECT_ONE_TEST_DATABASE_TASK_CREATE_CHILD=1", "PROJECT_ONE_TEST_DATABASE_TASK_CREATE_CONFIG="+configurationFile, "PROJECT_ONE_TEST_DATABASE_TASK_CREATE_KIND="+step.kind)
							out, e := child.CombinedOutput()
							exit, ok := e.(*exec.ExitError)
							if !ok || exit.ExitCode() != 90 {
								t.Fatalf("bootstrap create loss: %v %s", e, out)
							}
							l, e := open(c, false)
							if e != nil {
								t.Fatal(e)
							}
							record := databaseTaskState(l.events).record
							l.close()
							if record == nil || record.Task.Kind != step.kind || record.OutputVolumeSHA256 == "" || record.PreviousTaskSHA256 == "" {
								t.Fatal("bootstrap creation lineage")
							}
							activeTask = &record.Task
							taskID = record.ID
							cli("create-"+step.prefix+"-task", false)
							var creation DatabaseTaskResult
							if json.Unmarshal(cli("reconcile-"+step.prefix+"-task", true), &creation) != nil || creation.Pending || creation.ID == "" {
								t.Fatal("bootstrap create reconciliation")
							}
							taskID = creation.ID
							lostChild("execute", 89, step.kind)
							cli("start-"+step.prefix+"-task", false)
							for deadline := time.Now().Add(40 * time.Second); ; {
								observed, found, e := client.InspectDatabaseTask(ctx, record.Task)
								if e != nil || !found || observed.ID != taskID {
									t.Fatal("bootstrap task observation", e)
								}
								if observed.State.Status == "exited" {
									if observed.State.ExitCode != 0 || observed.State.OOMKilled {
										t.Fatal("bootstrap process failed", step.kind, observed.State.ExitCode)
									}
									break
								}
								if time.Now().After(deadline) {
									t.Fatal("bootstrap task deadline")
								}
								select {
								case <-ctx.Done():
									t.Fatal(ctx.Err())
								case <-time.After(100 * time.Millisecond):
								}
							}
							var result DatabaseTaskActionResult
							if json.Unmarshal(cli("reconcile-"+step.prefix+"-action", true), &result) != nil || result.Pending || !result.ProcessSucceeded || result.BootstrapQualified != storeKind(step.kind) {
								t.Fatal("bootstrap/store process reconciliation")
							}
							copy, e := os.ReadFile(filepath.Join(c.ScopeRoot, bootstrapCopyName))
							if e != nil {
								t.Fatal("durable protected host credential copy", e)
							}
							if bootstrapIntentSHA != "" && bootstrapIntentSHA != wire.Digest(copy) {
								t.Fatal("bootstrap credential copy changed")
							}
							bootstrapIntentSHA = wire.Digest(copy)
							before, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
							if e != nil {
								t.Fatal(e)
							}
							cli("start-"+step.prefix+"-task", true)
							after, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
							if e != nil || string(before) != string(after) {
								t.Fatal("bootstrap execution replayed")
							}
							lostChild("removed", 88, step.kind)
							cli("remove-"+step.prefix+"-task", false)
							if json.Unmarshal(cli("reconcile-"+step.prefix+"-action", true), &result) != nil || result.Pending || !result.Removed || result.BootstrapQualified != (step.kind != engine.DatabaseBootstrap) {
								t.Fatal("bootstrap removal reconciliation")
							}
							if role, mode, ok := engine.StoreTaskContext(step.kind); ok {
								var observed Status
								if json.Unmarshal(cli("status", true), &observed) != nil || observed.Stores[role].Qualified != (mode == "check") || observed.Stores[role].Namespace == nil {
									t.Fatal("historical scoped store status")
								}
								namespace := *observed.Stores[role].Namespace
								if previous, exists := storeNamespaces[role]; exists && previous != namespace {
									t.Fatal("scoped namespace changed")
								}
								storeNamespaces[role] = namespace
							}
							taskResults = append(taskResults, map[string]any{"kind": step.kind, "id": taskID, "create_loss_child_exit": 90, "execute_loss_child_exit": 89, "remove_loss_child_exit": 88, "bootstrap_qualified": result.BootstrapQualified, "credential_copy_sha256": bootstrapIntentSHA})
						}
						if json.Unmarshal(cli("status", true), &status) != nil || !status.BootstrapQualified || status.BootstrapCredentialFile != bootstrapCopyName {
							t.Fatal("bootstrap final status")
						}
						rows, e := query("SELECT (SELECT count(*) FROM users),(SELECT count(*) FROM tenants),(SELECT count(*) FROM workspaces),(SELECT count(*) FROM audit_events WHERE action='installation.bootstrapped'),(SELECT count(*) FROM sessions),(SELECT count(*) FROM mail_deliveries)")
						if e != nil || rows != "1|1|1|1|0|0" {
							t.Fatal("scoped bootstrap rows", rows, e)
						}
						bootstrapIdentity, e = query("SELECT tenant_id::text||':'||default_workspace_id::text||':'||primary_admin_user_id::text FROM installation_state WHERE id=1 AND bootstrap_complete")
						if e != nil || bootstrapIdentity == "" {
							t.Fatal("scoped bootstrap identity", e)
						}
						if withStores {
							if len(storeNamespaces) != 3 {
								t.Fatal("three scoped namespaces required")
							}
							for _, ns := range storeNamespaces {
								if ns.Namespace.TenantID != strings.Split(bootstrapIdentity, ":")[0] {
									t.Fatal("scoped namespace tenant binding")
								}
							}
							counts, e := query("SELECT action||':'||count(*)::text FROM audit_events WHERE action IN ('dataset.store_initialize_verified','training.store_initialize_verified','model.preparation_store_initialize_verified') GROUP BY action ORDER BY action")
							if e != nil || counts != "dataset.store_initialize_verified:1\nmodel.preparation_store_initialize_verified:1\ntraining.store_initialize_verified:1" {
								t.Fatal("scoped namespace audit counts", counts, e)
							}
						}

					}
					// A fresh raw SDK task after dependency restart is an independent
					// observation, not a replay or edit of the once-only scope journal.
					verifyRuntimeAfterRestart = func() {
						templates := []engine.DatabaseTask{*activeTask}
						if withStores {
							templates = nil
							l, e := open(c, false)
							if e != nil {
								t.Fatal(e)
							}
							volumes := volumeState(l.events)
							l.close()
							for _, kind := range []string{engine.DatasetStoreCheck, engine.TrainingStoreCheck, engine.PreparationStoreCheck} {
								role, _, _ := engine.StoreTaskContext(kind)
								v := volumes.roles[role]
								task := *activeTask
								task.Kind = kind
								task.Owner.Role = "database-" + kind
								task.OutputOwner = &engine.Ownership{ScopeID: initial.ScopeID, OperationID: v.ResourceOperationID, Role: role}
								task.Reason = ""
								templates = append(templates, task)
							}
						}
						for _, template := range templates {
							op, e := nonce()
							if e != nil {
								t.Fatal(e)
							}
							task := template
							task.Owner.OperationID = op
							activeTask = &task
							taskID = ""
							created, e := client.CreateDatabaseTask(ctx, task)
							taskID = created.ID
							if e != nil {
								t.Fatal("post-restart check create", e)
							}
							if _, e = client.StartDatabaseTask(ctx, task, created.ID, taskFiles, 30); e != nil {
								t.Fatal("post-restart check start", e)
							}
							for deadline := time.Now().Add(30 * time.Second); ; {
								observed, found, e := client.InspectDatabaseTask(ctx, task)
								if e != nil || !found || observed.ID != created.ID {
									t.Fatal("post-restart check inspect", e)
								}
								if observed.State.Status == "exited" {
									if observed.State.ExitCode != 0 || observed.State.OOMKilled {
										t.Fatal("post-restart credential check failed")
									}
									break
								}
								if time.Now().After(deadline) {
									t.Fatal("post-restart check deadline")
								}
								select {
								case <-ctx.Done():
									t.Fatal(ctx.Err())
								case <-time.After(100 * time.Millisecond):
								}
							}
							if withBootstrap && !withStores {
								intent, e := client.ReadBootstrapIntent(ctx, task, created.ID)
								if e != nil || intent.SHA256 != bootstrapIntentSHA {
									t.Fatal("post-restart bootstrap credential readback", e)
								}
								value, e := query("SELECT tenant_id::text||':'||default_workspace_id::text||':'||primary_admin_user_id::text FROM installation_state WHERE id=1 AND bootstrap_complete")
								if e != nil || value != bootstrapIdentity {
									t.Fatal("post-restart bootstrap identity", e)
								}
							}
							if withStores {
								role, _, _ := engine.StoreTaskContext(task.Kind)
								ns, e := client.InspectStoreTaskResult(ctx, task, created.ID)
								if e != nil || ns != storeNamespaces[role] {
									t.Fatal("post-restart scoped store namespace", e)
								}
							}
							if e = client.RemoveDatabaseTask(ctx, task, created.ID); e != nil {
								t.Fatal("post-restart check cleanup", e)
							}
							activeTask = nil
							taskID = ""
						}
					}
				}
			}
		}
	}
	before, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !withScope || withLifecycle {
		cli("start-database", true)
		after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if string(before) != string(after) {
			t.Fatal("completed database start resent")
		}
		cli("stop-runtime", true)
		cli("start-database", true)
		ready()
		assertSQL()
		if verifyRuntimeAfterRestart != nil {
			verifyRuntimeAfterRestart()
		}
		if withSchema {
			afterLedger, e := query("SELECT version::text||':'||checksum FROM schema_migrations ORDER BY version")
			if e != nil || afterLedger != qualifiedLedger {
				t.Fatal("restart changed qualified migration ledger", e)
			}
		}
	}
	controlState, found, e := client.InspectRuntime(ctx, initial.ScopeID, "control")
	if e != nil || !found || controlState.State.Status != "created" || controlState.State.PID != 0 || controlState.State.StartedAt != "0001-01-01T00:00:00Z" {
		t.Fatal("control service was started", e)
	}
	if e = cleanup(); e != nil {
		t.Fatal(e)
	}
	status := "passed_actual_signed_database_only_start_loss91_SQL_vector_restart_persistence"
	qualification := "actual_local_amd64_emulation_database_dependency_only_not_app_migration_bootstrap_native_or_full_release"
	if withTasks {
		status = "passed_actual_signed_database_internal_network_migrate_check_and_failure_no_repair"
		qualification = "actual_local_amd64_emulation_database_task_primitives_not_scoped_task_journal_bootstrap_application_native_or_full_release"
	}
	if withScope {
		status = "passed_actual_signed_scoped_database_task_creation_loss90_reconcile_repeat"
		qualification = "actual_local_amd64_emulation_never_started_migration_task_only_not_migrated_schema_application_or_target"
	}
	if withLifecycle {
		status = "passed_actual_signed_scoped_migration_execute_remove_loss89_88_reconcile_restart"
		qualification = "actual_local_amd64_emulation_migration_process_not_independent_schema_gate_bootstrap_application_or_target"
	}
	if withSchema {
		status = "passed_actual_signed_scoped_independent_schema_qualification_restart_ledger"
		qualification = "actual_local_amd64_emulation_historical_schema_qualification_only_not_bootstrap_application_native_or_full_release"
	}
	if withRuntimeAccess {
		status = "passed_actual_signed_runtime_access_prepare_and_independent_check_tasks"
		qualification = "actual_local_amd64_emulation_runtime_access_task_primitives_not_scoped_access_qualification_application_or_target"
	}
	if withScope && withRuntimeAccess {
		status = "passed_actual_signed_scoped_schema_and_runtime_access_qualification_recovery"
		qualification = "actual_local_amd64_emulation_historical_schema_and_access_qualification_only_not_stores_bootstrap_application_or_target"
	}
	if withBootstrap {
		status = "passed_actual_signed_bootstrap_private_intent_reconcile_resume_restart"
		qualification = "actual_local_amd64_emulation_bootstrap_primitives_only_not_scoped_bootstrap_application_or_target"
	}
	if withBootstrap && withScope {
		status = "passed_actual_signed_scoped_bootstrap_private_copy_loss_recovery_restart"
		qualification = "actual_local_amd64_emulation_historical_bootstrap_qualification_not_full_application_or_target"
	}
	if withStores {
		status = "passed_actual_signed_three_store_tasks_namespace_audit_recovery_restart"
		qualification = "actual_local_amd64_emulation_fixed_store_task_primitives_only_not_scoped_store_qualification_full_application_or_target"
	}
	if withStores && withScope {
		status = "passed_actual_signed_scoped_three_store_namespace_qualification_loss_recovery_restart"
		qualification = "actual_local_amd64_emulation_historical_scoped_store_and_audit_qualification_not_full_application_or_target"
	}
	put(t, output, encode(map[string]any{"store_namespaces": storeNamespaces, "synthetic_missing_dataset_audit_injection": withStores, "bootstrap_intent_sha256": bootstrapIntentSHA, "bootstrap_identity": bootstrapIdentity, "status": status, "database_tasks": taskResults, "scope_id": initial.ScopeID, "process": running, "postgres_version_number": 180006, "vector_version": "0.8.7", "vector_dimensions": 3, "synthetic_row_persisted_after_stop_restart": !withScope || withLifecycle, "control_never_started": true, "password_file_only": true, "child_exit": 91, "shared_postgres_image_retained": true, "exact_disposable_objects_cleaned": cleaned, "qualification": qualification}))
}
