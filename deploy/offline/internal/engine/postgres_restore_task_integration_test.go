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
	"errors"
	"io"
	"net/http"
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

// The response is deliberately lost only after the real Engine has consumed
// the request and returned success. This transport exists in tagged tests only.
type restoreResultLoss struct {
	base           http.RoundTripper
	method, suffix string
	hits           int
}

func (r *restoreResultLoss) RoundTrip(q *http.Request) (*http.Response, error) {
	response, e := r.base.RoundTrip(q)
	if e == nil && q.Method == r.method && strings.HasSuffix(q.URL.Path, r.suffix) && response.StatusCode >= 200 && response.StatusCode < 300 {
		r.hits++
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, errors.New("test_only_actual_restore_result_lost")
	}
	return response, e
}

func TestActualPostgresRestoreFromCompletedBackupIntoIndependentDatabase(t *testing.T) {
	runActualPostgresRestore(t, "sql")
}

func TestActualPostgresRestorePartialCopyAndCancellation(t *testing.T) {
	runActualPostgresRestore(t, "input-faults")
}

func TestActualStoreRestoreAgainstIndependentRestoredDatabase(t *testing.T) {
	runActualPostgresRestore(t, "stores")
}

func TestActualRestoredSchemaRuntimeAccessAndAuthorityInvalidation(t *testing.T) {
	runActualPostgresRestore(t, "authority")
}

type restoreCopyFault struct {
	base     http.RoundTripper
	cutBytes int64
	cancel   context.CancelFunc
	hits     int
}

func (r *restoreCopyFault) RoundTrip(q *http.Request) (*http.Response, error) {
	if q.Method != http.MethodPut || !strings.HasSuffix(q.URL.Path, "/archive") {
		return r.base.RoundTrip(q)
	}
	r.hits++
	if r.cutBytes > 0 {
		original := q.Body
		q.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(original, r.cutBytes), original}
		q.ContentLength, q.GetBody = -1, nil
	}
	response, e := r.base.RoundTrip(q)
	if e == nil && r.cancel != nil && response.StatusCode == http.StatusOK {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		r.cancel()
		return nil, context.Canceled
	}
	return response, e
}

func runActualPostgresRestore(t *testing.T, profile string) {
	inputFaultsOnly, storesOnly := profile == "input-faults", profile == "stores" || profile == "authority"
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	read := func(name string, out any) []byte {
		raw, e := bundle.ReadProtectedConfiguration(name)
		if e != nil || wire.UniqueJSON(raw, out) != nil {
			t.Fatal("protected actual restore fixture", e)
		}
		return raw
	}
	var rawConfig map[string]any
	raw := read(os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_CONFIGURATION"), &rawConfig)
	config, e := configuration.Load(raw, nil)
	if e != nil || config.Engine == nil || config.Compose == nil || !config.Verification.AllowTestFixture {
		t.Fatal("explicit isolated synthetic fixture", e)
	}
	var model struct {
		Services map[string]struct {
			Image, User string
			Command     []string
			Environment map[string]string
			Mounts      []struct {
				Source, Target string
				ReadOnly       bool `json:"read_only"`
			}
			Tmpfs []struct {
				Target   string
				Size     int64 `json:"size_bytes"`
				UID, GID int
			}
		}
	}
	read(config.Compose.ModelFile, &model)
	pg := model.Services["postgres"]
	initializer := model.Services["control"].Image
	if override := os.Getenv("PROJECT_ONE_TEST_RESTORE_CONTROL_IMAGE"); override != "" {
		if !strings.HasPrefix(override, "sha256:") || !containerIdentity.MatchString(strings.TrimPrefix(override, "sha256:")) {
			t.Fatal("explicit verified immutable control image required")
		}
		initializer = override
	}
	backup := os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_BACKUP")
	out := os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_EVIDENCE")
	if info, e := os.Lstat(out); e != nil || info.Mode() != os.ModeDir|0700 {
		t.Fatal("private evidence root")
	}
	var manifest struct {
		Status  string               `json:"status"`
		ScopeID string               `json:"scope_id"`
		Source  PostgresBackupSource `json:"source"`
		Files   []BackupArchiveFile  `json:"files"`
	}
	manifestRaw := read(filepath.Join(backup, "backup.json"), &manifest)
	if wire.Digest(manifestRaw) != os.Getenv("PROJECT_ONE_TEST_POSTGRES_RESTORE_MANIFEST_SHA256") || manifest.Status != "complete_selected_profile_backup_restore_pending" {
		t.Fatal("independent complete backup digest required")
	}
	var source PostgresBackupArchive
	sourceRaw := read(filepath.Join(backup, "postgres.json"), &source)
	archive := filepath.Join(backup, "postgres.tar")
	var descriptor BackupArchiveFile
	for _, f := range manifest.Files {
		if f.Name == "postgres.json" && (f.SHA256 != wire.Digest(sourceRaw) || f.Bytes != int64(len(sourceRaw))) {
			t.Fatal("source metadata changed")
		}
		if f.Name == "postgres.tar" {
			descriptor = f
		}
	}
	if descriptor.Name == "" || source.SHA256 != descriptor.SHA256 || source.Bytes != descriptor.Bytes || source.Source != manifest.Source {
		t.Fatal("source artifact bindings")
	}
	nonce := func() string {
		raw := make([]byte, 16)
		if _, e := rand.Read(raw); e != nil {
			t.Fatal(e)
		}
		return hex.EncodeToString(raw)
	}
	scopeID, setup := nonce(), nonce()
	database, user := "restore_test_"+setup[:8], "restore_operator_"+setup[:8]
	password := nonce() + nonce()
	put := func(name string, value any) {
		f, e := os.OpenFile(filepath.Join(out, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		if json.NewEncoder(f).Encode(value) != nil || f.Sync() != nil || f.Close() != nil {
			t.Fatal("durable metadata")
		}
		d, e := os.Open(out)
		if e != nil {
			t.Fatal(e)
		}
		e = d.Sync()
		d.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
	private := filepath.Join(out, "docker-client")
	if os.Mkdir(private, 0700) != nil || os.WriteFile(filepath.Join(private, "config.json"), []byte("{}\n"), 0600) != nil {
		t.Fatal("private Docker client")
	}
	docker := func(commandContext context.Context, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(commandContext, config.Compose.DockerBinary, append([]string{"--config", private, "--host", "unix://" + config.Engine.SocketFile}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "DOCKER_API_VERSION=1.49"}
		return cmd.CombinedOutput()
	}
	c, e := New(*config.Engine)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if _, e = c.Preflight(ctx); e != nil {
		t.Fatal(e)
	}
	uidParts := strings.Split(pg.User, ":")
	if len(uidParts) != 2 {
		t.Fatal("numeric PG user")
	}
	uid, e := strconv.Atoi(uidParts[0])
	if e != nil {
		t.Fatal(e)
	}
	gid, e := strconv.Atoi(uidParts[1])
	if e != nil {
		t.Fatal(e)
	}
	ids := map[string]string{}
	networkID := ""
	owners := []Ownership{}
	ownerIdentities := map[string][2]int{}
	var active *PostgresBackupTask
	taskID := ""
	cleaned := false
	var cleanupContext context.Context
	var cleanupCancel context.CancelFunc
	independentCleanup := func() context.Context {
		if cleanupContext == nil {
			cleanupContext, cleanupCancel = context.WithTimeout(context.Background(), 70*time.Second)
		}
		return cleanupContext
	}
	cleanup := func() error {
		bounded := independentCleanup()
		if active != nil {
			v, found, e := c.InspectPostgresBackupTask(bounded, *active)
			if e != nil {
				return e
			}
			if found {
				if taskID != "" && v.ID != taskID {
					return failure("ACTUAL_RESTORE_TASK_CHANGED")
				}
				if v.State.Running {
					if e = c.StopPostgresBackupTask(bounded, *active, v.ID, 2); e != nil {
						return e
					}
				}
				if e = c.RemovePostgresBackupTask(bounded, *active, v.ID); e != nil {
					return e
				}
			}
		}
		for _, role := range []string{"control", "postgres"} {
			v, found, e := c.InspectRuntime(bounded, scopeID, role)
			if e != nil {
				return e
			}
			if found {
				if ids[role] != "" && v.ID != ids[role] || v.Config.Labels["io.projectone.isolated-scope"] != scopeID || v.Config.Labels["io.projectone.purpose"] != "isolated-validation" || v.Config.Labels["com.docker.compose.service"] != role {
					return failure("ACTUAL_RESTORE_PEER_CHANGED")
				}
				if v.State.Running {
					if _, e = docker(bounded, "stop", "--time", "2", v.ID); e != nil {
						return e
					}
				}
				if _, e = docker(bounded, "rm", v.ID); e != nil {
					return e
				}
			}
		}
		for _, owner := range owners {
			identity, ok := ownerIdentities[owner.VolumeName()]
			if !ok {
				identity = [2]int{uid, gid}
			}
			if _, found, e := c.InspectUtility(bounded, owner, initializer, identity[0], identity[1]); e != nil {
				return e
			} else if found {
				if e = c.RemoveUtility(bounded, owner, initializer, identity[0], identity[1]); e != nil {
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
					return e
				}
			}
		}
		if networkID != "" {
			n, found, e := c.InspectRuntimeNetwork(bounded, scopeID)
			if e != nil {
				return e
			}
			if found {
				if n.ID != networkID || len(n.Containers) != 0 {
					return failure("ACTUAL_RESTORE_NETWORK_CHANGED")
				}
				if _, e = docker(bounded, "network", "rm", networkID); e != nil {
					return e
				}
			}
		}
		cleaned = true
		return nil
	}
	t.Cleanup(func() {
		defer func() {
			if cleanupCancel != nil {
				cleanupCancel()
			}
		}()
		if !cleaned {
			if e := cleanup(); e != nil {
				t.Error(e)
			}
		}
		put("cleanup.json", map[string]any{"exact_new_target_objects_removed": cleaned, "source_scope_and_complete_backup_preserved": true, "shared_images_and_original_services_preserved": true})
	})
	labels := []string{"--label", "io.projectone.isolated-scope=" + scopeID, "--label", "io.projectone.purpose=isolated-validation", "--label", "com.docker.compose.project=" + RuntimeProject(scopeID)}
	put("target-intent.json", map[string]any{"scope_id": scopeID, "operation_id": setup, "postgres_image": pg.Image, "control_image": initializer, "uid": uid, "gid": gid, "source_manifest_sha256": wire.Digest(manifestRaw), "disposable_synthetic_target_only": true})
	args := append([]string{"network", "create", "--internal", "--driver", "bridge"}, labels...)
	args = append(args, "--label", "com.docker.compose.network=isolated", RuntimeNetworkName(scopeID))
	value, e := docker(ctx, args...)
	if e != nil {
		t.Fatal("new private network", e)
	}
	networkID = strings.TrimSpace(string(value))
	prepare := func(owner Ownership, files []ConfigurationFile) {
		owners = append(owners, owner)
		if _, e := c.CreateVolume(ctx, owner); e != nil {
			t.Fatal(e)
		}
		if _, e := c.CreateUtility(ctx, owner, initializer, uid, gid); e != nil {
			t.Fatal(e)
		}
		if files != nil {
			if e := c.PutConfiguration(ctx, owner, initializer, uid, gid, files); e != nil {
				t.Fatal(e)
			}
		}
		if e := c.RemoveUtility(ctx, owner, initializer, uid, gid); e != nil {
			t.Fatal(e)
		}
	}
	pgConfig, pgData := Ownership{scopeID, setup, "postgres-config"}, Ownership{scopeID, setup, "postgres"}
	prepare(pgConfig, []ConfigurationFile{{Name: "postgres-password", Contents: []byte(password + "\n")}})
	prepare(pgData, []ConfigurationFile{})
	for _, role := range []string{"postgres", "control"} {
		image, identity := pg.Image, pg.User
		if role == "control" {
			image, identity = initializer, model.Services["control"].User
		}
		args := append([]string{"create", "--platform", "linux/amd64", "--name", RuntimeContainerName(scopeID, role), "--network", networkID, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--memory", "512m", "--memory-swap", "512m", "--cpus", "1", "--pids-limit", "64", "--restart", "no", "--log-driver", "none", "--user", identity}, labels...)
		args = append(args, "--label", "com.docker.compose.service="+role)
		if role == "postgres" {
			args = append(args, "--network-alias", postgresPeerAlias)
			for key, v := range pg.Environment {
				if key == "POSTGRES_DB" {
					v = database
				}
				if key == "POSTGRES_USER" {
					v = user
				}
				args = append(args, "--env", key+"="+v)
			}
			for _, m := range pg.Mounts {
				owner := pgData
				if m.Source == "postgres-config" {
					owner = pgConfig
				}
				mount := "type=volume,source=" + owner.VolumeName() + ",target=" + m.Target + ",volume-nocopy"
				if m.ReadOnly {
					mount += ",readonly"
				}
				args = append(args, "--mount", mount)
			}
			for _, m := range pg.Tmpfs {
				args = append(args, "--tmpfs", m.Target+":rw,noexec,nosuid,size="+strconv.FormatInt(m.Size, 10)+",uid="+strconv.Itoa(m.UID)+",gid="+strconv.Itoa(m.GID))
			}
		} else {
			args = append(args, "--entrypoint", modelTaskEntrypoint)
		}
		args = append(args, image)
		if role == "postgres" {
			args = append(args, pg.Command...)
			// Retain no raw SQL log in evidence. Count actual statements privately
			// to prove the corrupted dump failed after schema effects began.
			args = append(args, "-c", "logging_collector=on", "-c", "log_statement=all", "-c", "log_directory=log")
		} else {
			args = append(args, "serve", "--config", "/run/projectone/never-started.json")
		}
		value, e := docker(ctx, args...)
		if e != nil {
			t.Fatal("new owned peer", role, e)
		}
		ids[role] = strings.TrimSpace(string(value))
		if !containerIdentity.MatchString(ids[role]) {
			t.Fatal("unexpected Docker create output; preserve exact intent for independent reconciliation")
		}
		put(role+"-created.json", map[string]string{"id": ids[role]})
	}
	if _, e = docker(ctx, "start", ids["postgres"]); e != nil {
		t.Fatal(e)
	}
	query := func(statement string) (string, error) {
		value, e := docker(ctx, "exec", "--user", pg.User, ids["postgres"], "psql", "-X", "--set", "ON_ERROR_STOP=1", "-U", user, "-d", database, "-At", "-c", statement)
		return strings.TrimSpace(string(value)), e
	}
	for deadline := time.Now().Add(30 * time.Second); ; {
		if v, e := query("SELECT 1"); e == nil && v == "1" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("new PG readiness")
		}
		time.Sleep(200 * time.Millisecond)
	}
	results := []map[string]any{}
	modes := []string{"truncated-dump", "actual", "nonempty-target"}
	if inputFaultsOnly {
		modes = []string{"partial-copy", "cancel-copy"}
	}
	if storesOnly {
		modes = []string{"actual"}
	}
	for _, mode := range modes {
		loseResults := mode == "actual" && !storesOnly
		op := nonce()
		task := PostgresBackupTask{Owner: Ownership{scopeID, op, "database-" + PostgresRestoreKind}, InputOwner: Ownership{scopeID, op, PostgresRestoreInputRole + "-" + op}, OutputOwner: Ownership{scopeID, op, PostgresRestoreOutputRole + "-" + op}, Image: pg.Image, UID: uid, GID: gid, NetworkID: networkID, Peers: ids, Host: postgresPeerAlias, Port: 5432, Database: database, User: user, Restore: &PostgresRestoreProfile{SourceOwner: Ownership{scopeID, op, PostgresRestoreSourceRole + "-" + op}, SourceUID: uid, SourceGID: gid, ArchiveSHA256: descriptor.SHA256, ArchiveBytes: descriptor.Bytes, ExpectedSource: source.Source, ExpectedFiles: source.Files}}
		inputArchive := archive
		if mode == "truncated-dump" {
			// A new explicitly corrupt negative fixture is not a completed backup.
			// Its enclosing hashes are valid; pg_restore must detect inner damage
			// during real SQL execution and roll back all earlier schema effects.
			inputArchive = filepath.Join(out, "corrupt-negative-input.tar")
			broken, files := actualTruncatedRestoreArchive(t, archive, inputArchive, source.Files)
			task.Restore.ArchiveBytes, task.Restore.ArchiveSHA256 = broken.Bytes, broken.SHA256
			task.Restore.ExpectedFiles = files
		}
		put(mode+"-intent.json", task)
		file, e := PostgresBackupPasswordFile(task, password)
		if e != nil {
			t.Fatal(e)
		}
		prepare(task.InputOwner, []ConfigurationFile{file})
		prepare(task.OutputOwner, []ConfigurationFile{})
		owners = append(owners, task.Restore.SourceOwner)
		if _, e = c.CreateVolume(ctx, task.Restore.SourceOwner); e != nil {
			t.Fatal(e)
		}
		if _, e = c.CreateUtility(ctx, task.Restore.SourceOwner, initializer, uid, gid); e != nil {
			t.Fatal(e)
		}
		input, e := os.Open(inputArchive)
		if e != nil {
			t.Fatal(e)
		}
		transport := c.http.Transport
		if inputFaultsOnly {
			copyContext, copyCancel := context.WithCancel(ctx)
			fault := &restoreCopyFault{base: transport}
			if mode == "partial-copy" {
				fault.cutBytes = task.Restore.ArchiveBytes / 2
			} else {
				fault.cancel = copyCancel
			}
			c.http.Transport = fault
			_, copyError := c.PutPostgresRestoreArchive(copyContext, task, initializer, input)
			copyCancel()
			input.Close()
			if copyError == nil || fault.hits != 1 {
				t.Fatal("actual uncertain copy not exercised", copyError, fault.hits)
			}
			_, inspectError := c.InspectPostgresRestoreArchiveInput(ctx, task, initializer)
			if mode == "partial-copy" && inspectError == nil || mode == "cancel-copy" && inspectError != nil {
				t.Fatal("actual copied outcome observation mismatch", mode, inspectError)
			}
			input, e = os.Open(inputArchive)
			if e != nil {
				t.Fatal(e)
			}
			_, e = c.PutPostgresRestoreArchive(ctx, task, initializer, input)
			input.Close()
			c.http.Transport = transport
			if e == nil || e.Error() != "OFFLINE_ENGINE_NONEMPTY_RESTORE_INPUT_PRESERVED" || fault.hits != 1 {
				t.Fatal("uncertain input overwrite or resend", e, fault.hits)
			}
			if e = c.RemoveUtility(ctx, task.Restore.SourceOwner, initializer, uid, gid); e != nil {
				t.Fatal(e)
			}
			empty, er := query("SELECT (" + postgresEmptyTargetPredicate + ")::text")
			if er != nil || empty != "true" {
				t.Fatal("input-only fault changed database", er)
			}
			results = append(results, map[string]any{"mode": mode, "actual_copy_attempts": fault.hits, "uncertain_mutation_not_resent": true, "independent_complete_readback": inspectError == nil, "target_catalog_still_empty": true})
			continue
		}
		loss := &restoreResultLoss{base: transport, method: http.MethodPut, suffix: "/archive"}
		if loseResults {
			c.http.Transport = loss
		}
		_, e = c.PutPostgresRestoreArchive(ctx, task, initializer, input)
		c.http.Transport = transport
		input.Close()
		if loseResults {
			if e == nil || loss.hits != 1 {
				t.Fatal("actual successful upload result loss not exercised")
			}
			// Observe exact copied bytes; never send the uncertain PUT again.
			_, e = c.InspectPostgresRestoreArchiveInput(ctx, task, initializer)
		}
		if e != nil {
			utility, found, inspectError := c.InspectUtility(ctx, task.Restore.SourceOwner, initializer, uid, gid)
			if inspectError == nil && found {
				raw, status, re := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+utility.ID+"/archive", url.Values{"path": {UtilityDestination + "/" + postgresBackupRootName}}, nil, c.config.RequestSeconds)
				if re == nil && status == http.StatusOK {
					tr := tar.NewReader(bytes.NewReader(raw))
					headers := []map[string]any{}
					for {
						h, er := tr.Next()
						if er == io.EOF {
							break
						}
						if er != nil {
							break
						}
						headers = append(headers, map[string]any{"name": h.Name, "mode": h.Mode, "uid": h.Uid, "gid": h.Gid, "bytes": h.Size, "type": h.Typeflag})
					}
					put("failed-input-headers.json", headers)
				}
			}
			t.Fatal("actual bounded input upload/readback", e)
		}
		if e = c.RemoveUtility(ctx, task.Restore.SourceOwner, initializer, uid, gid); e != nil {
			t.Fatal(e)
		}
		active = &task
		taskID = ""
		v, e := c.CreatePostgresBackupTask(ctx, task)
		taskID = v.ID
		if e != nil {
			t.Fatal("actual restore create", e)
		}
		put(mode+"-created.json", map[string]string{"id": taskID})
		loss = &restoreResultLoss{base: transport, method: http.MethodPost, suffix: "/start"}
		if loseResults {
			c.http.Transport = loss
		}
		_, e = c.StartPostgresBackupTask(ctx, task, taskID, []ConfigurationFile{file}, 30)
		c.http.Transport = transport
		if loseResults {
			if e == nil || loss.hits != 1 {
				t.Fatal("actual successful start result loss not exercised")
			}
		} else if e != nil {
			t.Fatal("actual restore start", e)
		}
		for deadline := time.Now().Add(70 * time.Second); ; {
			var found bool
			v, found, e = c.InspectPostgresBackupTask(ctx, task)
			if e != nil || !found {
				t.Fatal(e)
			}
			if v.State.Status == "exited" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("restore deadline")
			}
			time.Sleep(100 * time.Millisecond)
		}
		if _, e = c.StartPostgresBackupTask(ctx, task, taskID, []ConfigurationFile{file}, 30); e == nil {
			t.Fatal("terminal restore replay")
		}
		if mode == "actual" {
			if v.State.ExitCode != 0 || v.State.OOMKilled {
				t.Fatal("actual pg_restore failed", v.State.ExitCode)
			}
			observation, e := c.InspectPostgresRestoreResult(ctx, task, taskID)
			if e != nil {
				t.Fatal("actual restore metadata", e)
			}
			put("restore-result.json", observation)
		} else {
			if v.State.ExitCode == 0 || v.State.OOMKilled {
				t.Fatal("existing target not refused", v.State.ExitCode)
			}
			if _, e = c.InspectPostgresRestoreResult(ctx, task, taskID); e == nil {
				t.Fatal("failed restore qualified")
			}
		}
		if mode == "truncated-dump" {
			empty, er := query("SELECT (" + postgresEmptyTargetPredicate + ")::text")
			if er != nil || empty != "true" {
				t.Fatal("real SQL failure did not roll back all application objects", empty, er)
			}
			log, er := query("SELECT pg_catalog.pg_read_file(pg_catalog.pg_current_logfile('stderr'))")
			created := strings.Count(log, "statement: CREATE TABLE ")
			if er != nil || created < 1 || !strings.Contains(log, "statement: BEGIN") {
				t.Fatal("actual SQL effects before failure not established", created, er)
			}
			results = append(results, map[string]any{"mode": mode, "exit_code": v.State.ExitCode, "empty_after_failed_transaction": true, "actual_create_table_statements_before_failure": created})
			if e = c.RemovePostgresBackupTask(ctx, task, taskID); e != nil {
				t.Fatal(e)
			}
			active, taskID = nil, ""
			continue
		}
		counts, e := query("SELECT (SELECT count(*) FROM public.users)||'|'||(SELECT count(*) FROM public.datasets)||'|'||(SELECT count(*) FROM public.dataset_versions)||'|'||(SELECT max(version) FROM public.schema_migrations)")
		if e != nil || counts != "1|1|2|16" {
			t.Fatal("restored history mismatch", counts, e)
		}
		tenant, e := query("SELECT tenant_id::text FROM public.installation_state WHERE id=1 AND bootstrap_complete")
		if e != nil || tenant != source.Source.TenantID {
			t.Fatal("restored tenant mismatch", e)
		}
		// Compare all exact version/checksum rows with the application's current
		// migration inputs, rather than accepting only MAX(version).
		rows, er := query("SELECT version::text||':'||checksum FROM public.schema_migrations ORDER BY version")
		if er != nil || rows != actualRestoreMigrationLedger(t) {
			t.Fatal("exact restored migration ledger mismatch", er)
		}
		results = append(results, map[string]any{"mode": mode, "exit_code": v.State.ExitCode, "restored_counts": counts, "tenant_id": tenant, "exact_migration_checksums": 16, "upload_and_start_result_losses_observed_without_resend": loseResults})
		if e = c.RemovePostgresBackupTask(ctx, task, taskID); e != nil {
			t.Fatal(e)
		}
		active = nil
		taskID = ""
	}
	if storesOnly {
		controlIdentity := strings.Split(model.Services["control"].User, ":")
		if len(controlIdentity) != 2 {
			t.Fatal("control identity")
		}
		controlUID, ue := strconv.Atoi(controlIdentity[0])
		controlGID, ge := strconv.Atoi(controlIdentity[1])
		if ue != nil || ge != nil {
			t.Fatal("control identity", ue, ge)
		}
		storeResults := runActualSelectedStoreImports(t, actualStoreRestoreFixture{Context: ctx, CleanupContext: independentCleanup, QualifyAuthority: profile == "authority", Client: c, BackupRoot: backup, SourceFiles: manifest.Files, ScopeID: scopeID, NetworkID: networkID, Peers: ids, Image: initializer, UID: controlUID, GID: controlGID, Database: database, User: user, Password: password, Nonce: nonce, Put: put, Query: query, Track: func(o Ownership) {
			owners = append(owners, o)
			ownerIdentities[o.VolumeName()] = [2]int{controlUID, controlGID}
		}})
		results = append(results, storeResults...)
	}
	input, e := os.Open(archive)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, input); e != nil {
		t.Fatal(e)
	}
	input.Close()
	if hex.EncodeToString(hash.Sum(nil)) != descriptor.SHA256 {
		t.Fatal("source archive changed")
	}
	if e = cleanup(); e != nil {
		t.Fatal(e)
	}
	status := "passed_actual_independent_SQL_restore_transaction_rollback_and_result_loss"
	if inputFaultsOnly {
		status = "passed_actual_restore_partial_copy_and_cancellation_no_resend"
	}
	if storesOnly {
		status = "passed_actual_selected_store_import_verification_and_audit"
	}
	if profile == "authority" {
		status = "passed_actual_restored_schema_runtime_access_and_authority_invalidation"
	}
	put("actual.json", map[string]any{"status": status, "scope_id": scopeID, "source_scope_id": manifest.ScopeID, "results": results, "complete_source_archive_unchanged": true, "qualification": "real_pg_restore_input_SQL_metadata_only_not_runtime_access_auth_files_full_restore_or_native_target"})
}

func actualRestoreMigrationLedger(t *testing.T) string {
	t.Helper()
	paths, e := filepath.Glob("../../../../services/control/migrations/postgresql/*.up.sql")
	if e != nil || len(paths) != 16 {
		t.Fatal("exact current migration fixture required", e)
	}
	rows := make([]string, 0, len(paths))
	for _, path := range paths {
		raw, e := os.ReadFile(path)
		version, ve := strconv.Atoi(filepath.Base(path)[:6])
		if e != nil || ve != nil {
			t.Fatal("current migration input", e, ve)
		}
		rows = append(rows, strconv.Itoa(version)+":"+wire.Digest(raw))
	}
	return strings.Join(rows, "\n")
}

func actualTruncatedRestoreArchive(t *testing.T, source, target string, original []BackupArchiveFile) (BackupArchiveFile, []BackupArchiveFile) {
	t.Helper()
	in, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	tr, tw := tar.NewReader(in), tar.NewWriter(out)
	files := append([]BackupArchiveFile(nil), original...)
	corrupted := false
	for {
		h, er := tr.Next()
		if er == io.EOF {
			break
		}
		if er != nil || h.Size > 1<<20 {
			t.Fatal("bounded synthetic corruption fixture", er)
		}
		raw, er := io.ReadAll(io.LimitReader(tr, (1<<20)+1))
		if er != nil || len(raw) > 1<<20 {
			t.Fatal("fixture body", er)
		}
		if h.Name == postgresBackupRootName+"/private/database.dump" {
			if len(raw) < 4096 {
				t.Fatal("sufficient real SQL archive required")
			}
			raw = raw[:len(raw)-2048]
			h.Size = int64(len(raw))
			for i := range files {
				if files[i].Name == "database.dump" {
					files[i].SHA256, files[i].Bytes = wire.Digest(raw), h.Size
				}
			}
			corrupted = true
		}
		if tw.WriteHeader(h) != nil {
			t.Fatal("fixture header")
		}
		if _, e = tw.Write(raw); e != nil {
			t.Fatal(e)
		}
	}
	if !corrupted || tw.Close() != nil || out.Sync() != nil || out.Close() != nil {
		t.Fatal("durable corrupt negative fixture")
	}
	raw, e := os.ReadFile(target)
	if e != nil {
		t.Fatal(e)
	}
	return BackupArchiveFile{Name: filepath.Base(target), Bytes: int64(len(raw)), SHA256: wire.Digest(raw)}, files
}
