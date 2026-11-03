//go:build offlineintegration

package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualPostgresLogicalBackupOfRetainedInstalledData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	raw, e := bundle.ReadProtectedConfiguration(os.Getenv("PROJECT_ONE_TEST_POSTGRES_BACKUP_CHECKPOINT"))
	if e != nil {
		t.Fatal(e)
	}
	var checkpoint struct {
		Status           string `json:"status"`
		ScopeID          string `json:"scope_id"`
		Binary           string `json:"operator_binary"`
		BinarySHA        string `json:"operator_binary_sha256"`
		Configuration    string `json:"operator_config"`
		ConfigurationSHA string `json:"operator_config_sha256"`
		Retention        string `json:"retention"`
	}
	if wire.UniqueJSON(raw, &checkpoint) != nil || checkpoint.Status != "passed_core_application_retained_stopped_next_data_recovery_acceptance" {
		t.Fatal("accepted retained synthetic scope required")
	}
	hashFile := func(path string) string {
		f, e := os.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		defer f.Close()
		hash := sha256.New()
		if _, e = io.Copy(hash, f); e != nil {
			t.Fatal(e)
		}
		return hex.EncodeToString(hash.Sum(nil))
	}
	if hashFile(checkpoint.Binary) != checkpoint.BinarySHA || hashFile(checkpoint.Configuration) != checkpoint.ConfigurationSHA {
		t.Fatal("retained operator/config changed")
	}
	raw, e = bundle.ReadProtectedConfiguration(checkpoint.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	config, e := configuration.Load(raw, nil)
	if e != nil || config.Engine == nil || config.Engine.Utility == nil || config.Compose == nil || !config.Verification.AllowTestFixture {
		t.Fatal("explicit synthetic local operator profile", e)
	}
	c, e := New(*config.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.Preflight(ctx); e != nil {
		t.Fatal(e)
	}
	raw, e = bundle.ReadProtectedConfiguration(config.Compose.ModelFile)
	if e != nil {
		t.Fatal(e)
	}
	var model struct {
		Services map[string]struct {
			Image, User string
			Environment map[string]string
		}
	}
	if wire.UniqueJSON(raw, &model) != nil {
		t.Fatal("frozen model")
	}
	pg := model.Services["postgres"]
	initializer := model.Services["control"].Image
	ids := map[string]string{}
	raw, e = bundle.ReadProtectedConfiguration(checkpoint.Retention)
	if e != nil {
		t.Fatal(e)
	}
	var retention struct {
		ScopeID   string `json:"scope_id"`
		Resources struct {
			NetworkID string `json:"network_id"`
			Services  map[string]struct{ ID, State string }
		} `json:"resources"`
	}
	if wire.UniqueJSON(raw, &retention) != nil || retention.ScopeID != checkpoint.ScopeID {
		t.Fatal("retention identity")
	}
	for role, service := range retention.Resources.Services {
		ids[role] = service.ID
	}
	raw, e = bundle.ReadProtectedConfiguration(config.Compose.InputsFile)
	if e != nil {
		t.Fatal(e)
	}
	var catalog struct {
		Roles []struct {
			Key   string
			Files []struct {
				Name   string
				Source string `json:"source_file"`
			}
		}
	}
	if wire.UniqueJSON(raw, &catalog) != nil {
		t.Fatal("input catalog")
	}
	dsnPath := ""
	for _, role := range catalog.Roles {
		if role.Key == "operator-config" {
			for _, f := range role.Files {
				if f.Name == "operator-dsn" {
					dsnPath = f.Source
				}
			}
		}
	}
	dsn, e := bundle.ReadProtectedConfiguration(dsnPath)
	if e != nil {
		t.Fatal(e)
	}
	connection, e := url.Parse(strings.TrimSpace(string(dsn)))
	if e != nil {
		t.Fatal("operator connection")
	}
	password, found := connection.User.Password()
	if !found {
		t.Fatal("protected operator password required")
	}
	port, e := strconv.Atoi(connection.Port())
	if e != nil {
		t.Fatal("explicit port")
	}
	output := os.Getenv("PROJECT_ONE_TEST_POSTGRES_BACKUP_EVIDENCE")
	if output == "" {
		t.Fatal("private evidence directory required")
	}
	info, e := os.Lstat(output)
	if e != nil || info.Mode() != os.ModeDir|0700 {
		t.Fatal("fresh external private evidence root")
	}
	put := func(name string, value any) {
		f, e := os.OpenFile(filepath.Join(output, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		if json.NewEncoder(f).Encode(value) != nil || f.Sync() != nil || f.Close() != nil {
			t.Fatal("durable metadata")
		}
		parent, e := os.Open(output)
		if e != nil {
			t.Fatal(e)
		}
		e = parent.Sync()
		parent.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	private := filepath.Join(output, "docker-client")
	if os.Mkdir(private, 0700) != nil {
		t.Fatal("private Docker client")
	}
	if os.WriteFile(filepath.Join(private, "config.json"), []byte("{}\n"), 0600) != nil {
		t.Fatal("private Docker client config")
	}
	docker := func(commandContext context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(commandContext, config.Compose.DockerBinary, append([]string{"--config", private, "--host", "unix://" + config.Engine.SocketFile}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
		return cmd.CombinedOutput()
	}
	native := func(commandContext context.Context, action string) error {
		_, e := exec.CommandContext(commandContext, checkpoint.Binary, action, "--config", checkpoint.Configuration).CombinedOutput()
		return e
	}
	query := func(statement string) (string, error) {
		value, e := docker(ctx, "exec", "--user", pg.User, ids["postgres"], "psql", "-X", "--set", "ON_ERROR_STOP=1", "-U", pg.Environment["POSTGRES_USER"], "-d", pg.Environment["POSTGRES_DB"], "-At", "-c", statement)
		return strings.TrimSpace(string(value)), e
	}
	var active *PostgresBackupTask
	taskID := ""
	owners := []Ownership{}
	cleaned := false
	cleanup := func() error {
		cleanupContext, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if active != nil {
			value, found, e := c.InspectPostgresBackupTask(cleanupContext, *active)
			if e != nil {
				return e
			}
			if found {
				if taskID != "" && value.ID != taskID {
					return failure("BACKUP_TEST_TASK_CHANGED")
				}
				if value.State.Running {
					if e = c.StopPostgresBackupTask(cleanupContext, *active, value.ID, 2); e != nil {
						return e
					}
				}
				if e = c.RemovePostgresBackupTask(cleanupContext, *active, value.ID); e != nil {
					return e
				}
			}
		}
		for _, owner := range owners {
			if _, found, e := c.InspectUtility(cleanupContext, owner, initializer, 999, 999); e != nil {
				return e
			} else if found {
				if e = c.RemoveUtility(cleanupContext, owner, initializer, 999, 999); e != nil {
					return e
				}
			}
			if _, found, e := c.InspectVolume(cleanupContext, owner); e != nil {
				return e
			} else if found {
				if e = c.VerifyVolumeConsumers(cleanupContext, owner, nil); e != nil {
					return e
				}
				if _, e = docker(cleanupContext, "volume", "rm", owner.VolumeName()); e != nil {
					return failure("BACKUP_TEST_VOLUME_CLEANUP_FAILED")
				}
			}
		}
		if e := native(cleanupContext, "stop-runtime"); e != nil {
			return failure("BACKUP_TEST_RECORDED_STOP_FAILED")
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
		put("cleanup.json", map[string]any{"exact_generated_backup_objects_removed": cleaned, "current_scope_data_retained_stopped": cleaned, "shared_images_and_original_services_preserved": true})
	})
	if e = native(ctx, "start-database"); e != nil {
		t.Fatal("recorded database start", e)
	}
	for deadline := time.Now().Add(30 * time.Second); ; {
		if value, e := query("SELECT 1"); e == nil && value == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("database readiness")
		}
		time.Sleep(200 * time.Millisecond)
	}
	const countsSQL = "SELECT (SELECT count(*) FROM public.datasets)||'|'||(SELECT count(*) FROM public.dataset_versions)||'|'||(SELECT max(version) FROM public.schema_migrations)"
	before, e := query(countsSQL)
	if e != nil || before != "1|2|16" {
		t.Fatal("actual retained installed data required", e)
	}
	results := []map[string]any{}
	for _, mode := range []string{"nonempty", "actual"} {
		nonce := make([]byte, 16)
		if _, e = rand.Read(nonce); e != nil {
			t.Fatal(e)
		}
		operation := hex.EncodeToString(nonce)
		task := PostgresBackupTask{Owner: Ownership{checkpoint.ScopeID, operation, "database-" + PostgresBackupKind}, InputOwner: Ownership{checkpoint.ScopeID, operation, PostgresBackupInputRole + "-" + operation}, OutputOwner: Ownership{checkpoint.ScopeID, operation, PostgresBackupOutputRole + "-" + operation}, Image: pg.Image, UID: 999, GID: 999, NetworkID: retention.Resources.NetworkID, Peers: ids, Host: connection.Hostname(), Port: port, Database: strings.TrimPrefix(connection.Path, "/"), User: connection.User.Username()}
		put(mode+"-intent.json", task)
		active = &task
		taskID = ""
		file, e := PostgresBackupPasswordFile(task, password)
		if e != nil {
			t.Fatal(e)
		}
		files := []ConfigurationFile{file}
		for _, owner := range []Ownership{task.InputOwner, task.OutputOwner} {
			owners = append(owners, owner)
			if _, e = c.CreateVolume(ctx, owner); e != nil {
				t.Fatal(e)
			}
			if _, e = c.CreateUtility(ctx, owner, initializer, task.UID, task.GID); e != nil {
				t.Fatal(e)
			}
			payload := []ConfigurationFile{}
			if owner == task.InputOwner {
				payload = files
			} else if mode == "nonempty" {
				payload = []ConfigurationFile{{Name: "preserved.txt", Contents: []byte("Synthetic existing output must remain unchanged\n")}}
			}
			if e = c.PutConfiguration(ctx, owner, initializer, task.UID, task.GID, payload); e != nil {
				t.Fatal(e)
			}
			if e = c.RemoveUtility(ctx, owner, initializer, task.UID, task.GID); e != nil {
				t.Fatal(e)
			}
		}
		value, e := c.CreatePostgresBackupTask(ctx, task)
		if e != nil {
			taskID = value.ID
			t.Fatal("actual create", e)
		}
		taskID = value.ID
		put(mode+"-created.json", map[string]any{"task_id": taskID, "task": task})
		if _, e = c.CreatePostgresBackupTask(ctx, task); e == nil {
			t.Fatal("repeated create not refused")
		}
		_, e = c.StartPostgresBackupTask(ctx, task, taskID, files, 30)
		if mode == "nonempty" {
			if e == nil || !strings.Contains(e.Error(), "NONEMPTY_BACKUP_OUTPUT_PRESERVED") {
				t.Fatal("existing output not preserved", e)
			}
			value, found, e := c.InspectPostgresBackupTask(ctx, task)
			if e != nil || !found || value.State.Status != "created" || value.State.StartedAt != neverStarted {
				t.Fatal("refused task started", e)
			}
		} else {
			if e != nil {
				t.Fatal("actual start", e)
			}
			for deadline := time.Now().Add(60 * time.Second); ; {
				value, found, e = c.InspectPostgresBackupTask(ctx, task)
				if e != nil || !found {
					t.Fatal("actual observation", e)
				}
				if value.State.Status == "exited" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("logical backup deadline")
				}
				time.Sleep(100 * time.Millisecond)
			}
			if value.State.ExitCode != 0 || value.State.OOMKilled {
				t.Fatal("actual pg_dump/list/source task failed", value.State.ExitCode)
			}
			if _, e = c.StartPostgresBackupTask(ctx, task, taskID, files, 30); e == nil {
				t.Fatal("terminal physical task replayed")
			}
			path := filepath.Join(output, "database.tar.partial")
			sink, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				t.Fatal(e)
			}
			archive, e := c.StreamPostgresBackupArchive(ctx, task, taskID, 64<<20, 60, sink)
			if e != nil {
				sink.Close()
				t.Fatal("actual streamed archive", e)
			}
			if e = sink.Sync(); e != nil {
				sink.Close()
				t.Fatal(e)
			}
			if e = sink.Close(); e != nil {
				t.Fatal(e)
			}
			if hashFile(path) != archive.SHA256 {
				t.Fatal("host file readback differs")
			}
			final := filepath.Join(output, "database.tar")
			if e = os.Rename(path, final); e != nil {
				t.Fatal(e)
			}
			parent, e := os.Open(output)
			if e != nil {
				t.Fatal(e)
			}
			e = parent.Sync()
			parent.Close()
			if e != nil {
				t.Fatal(e)
			}
			put("archive.json", archive)
			results = append(results, map[string]any{"status": "actual_dump_custom_format_list_source_and_durable_stream_passed", "archive": final, "bytes": archive.Bytes, "sha256": archive.SHA256, "source": archive.Source})
		}
		if e = c.RemovePostgresBackupTask(ctx, task, taskID); e != nil {
			t.Fatal(e)
		}
		active = nil
		taskID = ""
	}
	after, e := query(countsSQL)
	if e != nil || before != after {
		t.Fatal("backup changed product data", e)
	}
	if e = cleanup(); e != nil {
		t.Fatal(e)
	}
	put("actual.json", map[string]any{"status": "passed_actual_postgres_logical_task_and_durable_archive_primitive", "results": results, "existing_output_refused_before_start": true, "single_physical_task_execution": true, "schema_dataset_version_counts_unchanged": before, "qualification": "real_logical_export_only_not_full_four_store_backup_restore_or_native_target"})
}
