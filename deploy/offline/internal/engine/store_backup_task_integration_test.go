//go:build offlineintegration

package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestActualStoreBackupOfRetainedInstalledData(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 210*time.Second)
	defer cancel()
	raw, e := bundle.ReadProtectedConfiguration(os.Getenv("PROJECT_ONE_TEST_STORE_BACKUP_CHECKPOINT"))
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
	output := os.Getenv("PROJECT_ONE_TEST_STORE_BACKUP_EVIDENCE")
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
	identity := strings.Split(model.Services["control"].User, ":")
	if len(identity) != 2 {
		t.Fatal("numeric control identity required")
	}
	uid, e := strconv.Atoi(identity[0])
	if e != nil {
		t.Fatal(e)
	}
	gid, e := strconv.Atoi(identity[1])
	if e != nil {
		t.Fatal(e)
	}
	files := []ConfigurationFile{}
	for _, role := range catalog.Roles {
		for _, f := range role.Files {
			if role.Key != "operator-config" && (role.Key != "control-config" || f.Name == "runtime.yaml" || f.Name == "runtime-dsn") {
				continue
			}
			contents, e := bundle.ReadProtectedConfiguration(f.Source)
			if e != nil {
				t.Fatal(e)
			}
			files = append(files, ConfigurationFile{Name: f.Name, Contents: contents})
		}
	}
	if e = c.ValidateConfigurationFiles(files, uid, gid); e != nil {
		t.Fatal(e)
	}
	var active *DatabaseTask
	taskID := ""
	owners := []Ownership{}
	cleaned := false
	cleanup := func() error {
		bounded, done := context.WithTimeout(context.Background(), 60*time.Second)
		defer done()
		if active != nil {
			value, found, e := c.InspectDatabaseTask(bounded, *active)
			if e != nil {
				return e
			}
			if found {
				if taskID != "" && value.ID != taskID {
					return failure("STORE_BACKUP_TEST_ID_CHANGED")
				}
				if value.State.Running {
					if e = c.StopDatabaseTask(bounded, *active, value.ID, 2); e != nil {
						return e
					}
				}
				if e = c.RemoveDatabaseTask(bounded, *active, value.ID); e != nil {
					return e
				}
			}
		}
		for _, owner := range owners {
			if _, found, e := c.InspectUtility(bounded, owner, initializer, uid, gid); e != nil {
				return e
			} else if found {
				if e = c.RemoveUtility(bounded, owner, initializer, uid, gid); e != nil {
					return e
				}
			}
			if _, found, e := c.InspectVolume(bounded, owner); e != nil {
				return e
			} else if found {
				if e = c.VerifyVolumeConsumers(bounded, owner, nil); e != nil {
					return e
				}
				if _, e = docker(bounded, "volume", "rm", owner.VolumeName()); e != nil {
					return failure("STORE_BACKUP_TEST_VOLUME_CLEANUP_FAILED")
				}
			}
		}
		if e := native(bounded, "stop-runtime"); e != nil {
			return e
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
		put("cleanup.json", map[string]any{"exact_generated_objects_removed": cleaned, "current_scope_data_retained_stopped": cleaned, "shared_images_and_original_services_preserved": true})
	})
	if e = native(ctx, "start-database"); e != nil {
		t.Fatal(e)
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
	counts, e := query("SELECT (SELECT count(*) FROM public.datasets)||'|'||(SELECT count(*) FROM public.dataset_versions)||'|'||(SELECT max(version) FROM public.schema_migrations)")
	if e != nil || counts != "1|2|16" {
		t.Fatal("actual installed history required", e)
	}
	control, found, e := c.InspectRuntime(ctx, checkpoint.ScopeID, "control")
	if e != nil || !found || control.ID != ids["control"] || control.Image != initializer || control.State.Status != "exited" || control.State.Running {
		t.Fatal("exact stopped original control required", e)
	}
	// Small actual synthetic data only: independent bounded archive inventory,
	// hashes and modes without exposing or extracting source file contents.
	inventory := func(role string) map[string]any {
		data, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+control.ID+"/archive", url.Values{"path": {"/var/lib/projectone/" + role}}, nil, c.config.RequestSeconds)
		if e != nil || status != http.StatusOK {
			t.Fatal("source readback", e)
		}
		tr := tar.NewReader(bytes.NewReader(data))
		result := map[string]any{}
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			name := strings.TrimSuffix(h.Name, "/")
			paxSafe := true
			for key, value := range h.PAXRecords {
				if key != "path" && key != "linkpath" || key == "path" && value != h.Name || key == "linkpath" && value != h.Linkname {
					paxSafe = false
				}
			}
			if !wire.SafePath(name) || !strings.HasPrefix(name, role) || !paxSafe || len(h.Xattrs) > 0 {
				t.Fatal("unsafe source archive")
			}
			if _, ok := result[name]; ok {
				t.Fatal("duplicate source archive")
			}
			if h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeLink {
				t.Fatal("special source entry")
			}
			if h.Typeflag == tar.TypeLink {
				target, exists := result[h.Linkname]
				if !exists || !wire.SafePath(h.Linkname) || !strings.HasPrefix(h.Linkname, role+"/") || h.Size != 0 {
					t.Fatal("unsafe source hardlink")
				}
				bound := target.(map[string]any)
				if bound["type"] != byte(tar.TypeReg) || bound["mode"] != h.Mode || bound["uid"] != h.Uid || bound["gid"] != h.Gid {
					t.Fatal("source hardlink metadata mismatch")
				}
				result[name] = map[string]any{"mode": h.Mode, "uid": h.Uid, "gid": h.Gid, "type": h.Typeflag, "bytes": bound["bytes"], "sha256": bound["sha256"], "link": h.Linkname}
				continue
			}
			if h.Linkname != "" {
				t.Fatal("unexpected source link")
			}
			hash := sha256.New()
			if _, e = io.Copy(hash, tr); e != nil {
				t.Fatal(e)
			}
			result[name] = map[string]any{"mode": h.Mode, "uid": h.Uid, "gid": h.Gid, "type": h.Typeflag, "bytes": h.Size, "sha256": hex.EncodeToString(hash.Sum(nil))}
		}
		return result
	}
	results := []map[string]any{}
	for _, kind := range []string{ModelFilesExport, DatasetFilesExport} {
		p, _ := storeBackupTask(kind)
		before := inventory(p.role)
		audit := "model.files_exported"
		if p.role == "datasets" {
			audit = "dataset.files_exported"
		}
		auditSQL := "SELECT count(*) FROM public.audit_events WHERE audience='operator' AND outcome='success' AND action='" + audit + "' AND reason='Isolated installed-data recovery acceptance'"
		priorAudit, e := query(auditSQL)
		if e != nil {
			t.Fatal(e)
		}
		priorAuditCount, e := strconv.Atoi(priorAudit)
		if e != nil {
			t.Fatal(e)
		}
		volumeName := "projectone-" + checkpoint.ScopeID + "-" + p.role
		data, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/volumes/"+volumeName, nil, nil, c.config.RequestSeconds)
		var volume Volume
		if e != nil || status != http.StatusOK || wire.UniqueJSON(data, &volume) != nil {
			t.Fatal("source volume metadata", e)
		}
		source := Ownership{checkpoint.ScopeID, volume.Labels["io.projectone.operation"], p.role}
		if _, found, e := c.InspectVolume(ctx, source); e != nil || !found {
			t.Fatal("exact source generation", e)
		}
		bound := false
		for _, m := range control.Mounts {
			if m.Name == source.VolumeName() && m.Destination == "/var/lib/projectone/"+p.role && m.RW {
				bound = true
			}
		}
		if !bound {
			t.Fatal("source not mounted by original control")
		}
		nonce := make([]byte, 16)
		if _, e = rand.Read(nonce); e != nil {
			t.Fatal(e)
		}
		operation := hex.EncodeToString(nonce)
		task := DatabaseTask{Owner: Ownership{checkpoint.ScopeID, operation, "database-" + kind}, VolumeOwner: Ownership{checkpoint.ScopeID, operation, StoreBackupInputRole + "-" + operation}, OutputOwner: &Ownership{checkpoint.ScopeID, operation, "backup-" + p.role + "-" + operation}, SourceOwner: &source, Kind: kind, Image: initializer, UID: uid, GID: gid, NetworkID: retention.Resources.NetworkID, Peers: ids, Reason: "Isolated installed-data recovery acceptance"}
		put(p.role+"-intent.json", task)
		active = &task
		taskID = ""
		for _, owner := range []Ownership{task.VolumeOwner, *task.OutputOwner} {
			owners = append(owners, owner)
			if _, e = c.CreateVolume(ctx, owner); e != nil {
				t.Fatal(e)
			}
			if _, e = c.CreateUtility(ctx, owner, initializer, uid, gid); e != nil {
				t.Fatal(e)
			}
			payload := []ConfigurationFile{}
			if owner == task.VolumeOwner {
				payload = files
			}
			if e = c.PutConfiguration(ctx, owner, initializer, uid, gid, payload); e != nil {
				t.Fatal(e)
			}
			if e = c.RemoveUtility(ctx, owner, initializer, uid, gid); e != nil {
				t.Fatal(e)
			}
		}
		value, e := c.CreateDatabaseTask(ctx, task)
		if e != nil {
			taskID = value.ID
			t.Fatal("create snapshot", e)
		}
		taskID = value.ID
		put(p.role+"-created.json", map[string]any{"task": task, "task_id": taskID})
		if _, e = c.StartDatabaseTask(ctx, task, taskID, files, 30); e != nil {
			t.Fatal("start snapshot", e)
		}
		for deadline := time.Now().Add(60 * time.Second); ; {
			value, found, e = c.InspectDatabaseTask(ctx, task)
			if e != nil || !found {
				t.Fatal(e)
			}
			if value.State.Status == "exited" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("snapshot deadline")
			}
			time.Sleep(100 * time.Millisecond)
		}
		if value.State.ExitCode != 0 || value.State.OOMKilled {
			t.Fatal("actual existing Go export failed", p.role, value.State.ExitCode)
		}
		if _, e = c.StartDatabaseTask(ctx, task, taskID, files, 30); e == nil {
			t.Fatal("terminal export replay")
		}
		partial := filepath.Join(output, p.role+".tar.partial")
		sink, e := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		archive, e := c.StreamStoreBackupArchive(ctx, task, taskID, 64<<20, 10000, 60, sink)
		if e != nil {
			sink.Close()
			t.Fatal("stream snapshot", e)
		}
		if e = sink.Sync(); e != nil {
			sink.Close()
			t.Fatal(e)
		}
		if e = sink.Close(); e != nil {
			t.Fatal(e)
		}
		if hashFile(partial) != archive.SHA256 {
			t.Fatal("snapshot host readback")
		}
		final := filepath.Join(output, p.role+".tar")
		if e = os.Rename(partial, final); e != nil {
			t.Fatal(e)
		}
		dir, e := os.Open(output)
		if e != nil {
			t.Fatal(e)
		}
		e = dir.Sync()
		dir.Close()
		if e != nil {
			t.Fatal(e)
		}
		put(p.role+"-archive.json", archive)
		if e = c.RemoveDatabaseTask(ctx, task, taskID); e != nil {
			t.Fatal(e)
		}
		active = nil
		taskID = ""
		after := inventory(p.role)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("export changed source bytes/modes/inventory")
		}
		put(p.role+"-source-inventory.json", before)
		count, e := query(auditSQL)
		if e != nil || count != strconv.Itoa(priorAuditCount+1) {
			t.Fatal("existing backend export audit required", p.role, count, e)
		}
		results = append(results, map[string]any{"role": p.role, "archive": final, "bytes": archive.Bytes, "sha256": archive.SHA256, "source_unchanged": true, "backend_audit_count": count})
	}
	if e = cleanup(); e != nil {
		t.Fatal(e)
	}
	put("actual.json", map[string]any{"status": "passed_actual_models_datasets_snapshot_tasks_and_durable_archives", "results": results, "schema_dataset_version_counts": counts, "qualification": "two_selected_file_exports_only_not_complete_backup_restore_or_native_target"})
}
