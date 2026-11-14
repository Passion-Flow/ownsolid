//go:build repository_integration

package models_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const modelProcessInputFormat = "projectone.model-worker-process-fixture.v1"
const modelProcessInputLimit = 16 * 1024
const modelProcessChildSeconds = 60

type modelProcessInput struct {
	Format               string `json:"format"`
	Engine               string `json:"engine"`
	PostgreSQLSecretFile string `json:"postgresql_secret_file"`
	PostgreSQLSchema     string `json:"postgresql_schema"`
	MySQLConfiguration   string `json:"mysql_configuration"`
	Root                 string `json:"root"`
	ImportID             string `json:"import_id"`
	TenantID             string `json:"tenant_id"`
	WorkspaceID          string `json:"workspace_id"`
	ActorID              string `json:"actor_id"`
	TimeoutSeconds       int    `json:"timeout_seconds"`
}

// This entry opens an existing exact fixture. It intentionally never calls the
// fixture constructor, migration, bootstrap or a synthetic worker implementation.
func TestModelWorkerProcessChild(t *testing.T) {
	path := os.Getenv("PROJECT_ONE_MODEL_PROCESS_INPUT_FILE")
	if path == "" {
		return
	}
	canonical, e := filepath.EvalSymlinks(path)
	info, se := os.Lstat(path)
	if e != nil || se != nil || canonical != path || !filepath.IsAbs(path) || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > modelProcessInputLimit {
		t.Fatal("private process input required")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		t.Fatal("owned single-link process input required")
	}
	file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		t.Fatal("private process input open refused")
	}
	opened, e := file.Stat()
	if e != nil || !os.SameFile(info, opened) {
		file.Close()
		t.Fatal("process input identity changed")
	}
	raw, e := io.ReadAll(io.LimitReader(file, modelProcessInputLimit+1))
	closed := file.Close()
	current, ce := os.Lstat(path)
	if e != nil || closed != nil || ce != nil || !os.SameFile(info, current) || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) || len(raw) != int(info.Size()) {
		t.Fatal("process input changed during read")
	}
	var input modelProcessInput
	if e != nil || lifecyclewire.DecodeStrict(raw, &input) != nil || input.Format != modelProcessInputFormat || input.TimeoutSeconds < 10 || input.TimeoutSeconds > 300 {
		t.Fatal("strict process input invalid")
	}
	for _, id := range []string{input.ImportID, input.TenantID, input.WorkspaceID, input.ActorID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			t.Fatal("canonical process scope required")
		}
	}
	root, e := filepath.EvalSymlinks(input.Root)
	rootInfo, re := os.Lstat(input.Root)
	state := os.Getenv("PROJECT_ONE_STATE_ROOT")
	if e != nil || re != nil || root != input.Root || !rootInfo.IsDir() || rootInfo.Mode().Perm() != 0700 || state == "" || !strings.HasPrefix(root, filepath.Clean(state)+string(os.PathSeparator)) {
		t.Fatal("exact owned external process root required")
	}
	rootOwner, ok := rootInfo.Sys().(*syscall.Stat_t)
	if !ok || rootOwner.Uid != uint32(os.Geteuid()) {
		t.Fatal("owned process root required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(input.TimeoutSeconds)*time.Second)
	defer cancel()
	var store transaction.Store
	if input.Engine == "postgresql" {
		if input.MySQLConfiguration != "" || !regexp.MustCompile(`^m20_repository_[a-f0-9]{32}$`).MatchString(input.PostgreSQLSchema) {
			t.Fatal("exact existing PostgreSQL scope required")
		}
		secret, err := config.Secret(input.PostgreSQLSecretFile)
		if err != nil {
			t.Fatal("private PostgreSQL reference required")
		}
		pc, err := pgxpool.ParseConfig(strings.TrimSpace(string(secret)))
		if err != nil || pc.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(pc.ConnConfig.Database, "_test") {
			t.Fatal("isolated PostgreSQL process scope required")
		}
		pc.MaxConns = 2
		pc.ConnConfig.RuntimeParams["search_path"] = input.PostgreSQLSchema
		pc.ConnConfig.RuntimeParams["timezone"] = "UTC"
		pc.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
		pc.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
		pool, err := pgxpool.NewWithConfig(ctx, pc)
		if err != nil {
			t.Fatal("actual child PostgreSQL open failed")
		}
		defer pool.Close()
		store, e = transaction.FromPostgreSQL(pool)
	} else if input.Engine == "mysql" {
		if input.PostgreSQLSecretFile != "" || input.PostgreSQLSchema != "" {
			t.Fatal("mixed child engine references refused")
		}
		c, err := mysqlstore.Load(input.MySQLConfiguration, "")
		if err != nil || c.Host != "127.0.0.1" || !strings.HasPrefix(c.Database, "m20_repository_") || !strings.HasSuffix(c.Database, "_test") {
			t.Fatal("isolated child MySQL reference required")
		}
		c.Policy.MaximumOpenConnections, c.Policy.MaximumIdleConnections = 2, 0
		kernel, observed, err := mysqlstore.Open(ctx, c)
		if err != nil {
			t.Fatal("actual child MySQL open failed")
		}
		defer kernel.Close()
		if !observed.TLSAuthenticated {
			t.Fatal("verified child MySQL TLS required")
		}
		store, e = transaction.FromMySQL(kernel)
	} else {
		t.Fatal("explicit child engine required")
	}
	if e != nil {
		t.Fatal("child repository boundary refused")
	}
	statement := func(pg, my string, args ...any) transaction.Statement {
		if store.Dialect() == transaction.MySQL {
			return transaction.SQL(transaction.MySQL, my, args...)
		}
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	}
	var tenant, workspace, actor, installed string
	var pending int
	if e = store.QueryRow(ctx, statement(`SELECT tenant_id::text,workspace_id::text,requested_by::text FROM model_imports WHERE id=$1`, `SELECT tenant_id,workspace_id,requested_by FROM model_imports WHERE id=?`, input.ImportID)).Scan(&tenant, &workspace, &actor); e != nil || tenant != input.TenantID || workspace != input.WorkspaceID || actor != input.ActorID {
		t.Fatal("existing child import binding refused")
	}
	if e = store.QueryRow(ctx, statement(`SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete`, `SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=TRUE`)).Scan(&installed); e != nil || installed != tenant {
		t.Fatal("existing installation binding refused")
	}
	if e = store.QueryRow(ctx, statement(`SELECT COUNT(*) FROM model_imports WHERE state IN ('queued','verifying')`, `SELECT COUNT(*) FROM model_imports WHERE state IN ('queued','verifying')`)).Scan(&pending); e != nil || pending != 1 {
		t.Fatal("exactly one existing pending child command required")
	}
	identityService := &identity.Service{Storage: store}
	s := &models.Service{Identity: identityService, Workspaces: &workspaces.Service{Identity: identityService}, RootDirectory: input.Root}
	fmt.Fprintln(os.Stdout, "MODEL_WORKER_PROCESS_STARTED")
	owned, e := s.ProcessNext(ctx)
	if !owned || e != nil {
		t.Fatal("actual process worker refused", e)
	}
	fmt.Fprintln(os.Stdout, "MODEL_WORKER_PROCESS_COMPLETED")
}

type modelProcess struct {
	command              *exec.Cmd
	done                 chan struct{}
	err                  error
	output               *os.File
	once                 sync.Once
	test                 *testing.T
	inputPath, directory string
}

func startModelProcess(t *testing.T, x humanImportFixture, id string) *modelProcess {
	t.Helper()
	input := modelProcessInput{Format: modelProcessInputFormat, Engine: string(x.f.Store.Dialect()), Root: x.root, ImportID: id, TenantID: x.f.Tenant, WorkspaceID: x.f.Workspace, ActorID: x.f.User, TimeoutSeconds: modelProcessChildSeconds}
	if input.Engine == "mysql" {
		input.MySQLConfiguration = os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION")
	} else {
		input.PostgreSQLSecretFile, input.PostgreSQLSchema = os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE"), os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA")
	}
	evidence := os.Getenv("PROJECT_ONE_REPOSITORY_MODEL_PROCESS_EVIDENCE_ROOT")
	canonical, e := filepath.EvalSymlinks(evidence)
	info, se := os.Lstat(evidence)
	if e != nil || se != nil || canonical != evidence || !filepath.IsAbs(evidence) || !info.IsDir() || info.Mode().Perm() != 0700 {
		t.Fatal("explicit private retained process evidence root required")
	}
	directory := filepath.Join(evidence, "child-"+uuid.NewString())
	if e := os.Mkdir(directory, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(directory, "worker-input.json")
	raw, _ := json.Marshal(input)
	if e := writeModelProcessEvidence(path, raw); e != nil {
		t.Fatal(e)
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	output, e := os.OpenFile(filepath.Join(directory, "worker-output.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	command := exec.CommandContext(x.ctx, executable, "-test.run=^TestModelWorkerProcessChild$", "-test.v", fmt.Sprintf("-test.timeout=%ds", input.TimeoutSeconds))
	command.Env = append(os.Environ(), "PROJECT_ONE_MODEL_PROCESS_INPUT_FILE="+path)
	command.Stdout, command.Stderr = output, output
	p := &modelProcess{command: command, done: make(chan struct{}), output: output, test: t, inputPath: path, directory: directory}
	if e = command.Start(); e != nil {
		output.Close()
		t.Fatal("actual worker process start failed", e)
	}
	go func() { p.err = command.Wait(); close(p.done) }()
	t.Cleanup(func() { p.stop() })
	t.Logf("Actual worker process started: pid=%d", command.Process.Pid)
	return p
}

func (p *modelProcess) stop() {
	p.once.Do(func() {
		select {
		case <-p.done:
		default:
			p.command.Process.Kill()
			<-p.done
		}
		if e := p.output.Sync(); e != nil {
			p.test.Error("child output sync failed")
		}
		if e := p.output.Close(); e != nil {
			p.test.Error("child output close failed")
		}
		input, ie := os.ReadFile(p.inputPath)
		output, oe := os.ReadFile(p.output.Name())
		if ie != nil || oe != nil || len(output) > 1<<20 {
			p.test.Error("bounded retained child evidence required")
			return
		}
		i, o := sha256.Sum256(input), sha256.Sum256(output)
		signal := ""
		signalNumber := 0
		if status, ok := p.command.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			signal = status.Signal().String()
			signalNumber = int(status.Signal())
		}
		raw, e := json.MarshalIndent(map[string]any{"format": "projectone.model-worker-process-result.v1", "test": p.test.Name(), "pid": p.command.Process.Pid, "exit_code": p.command.ProcessState.ExitCode(), "signal": signal, "signal_number": signalNumber, "input_sha256": hex.EncodeToString(i[:]), "output_sha256": hex.EncodeToString(o[:]), "output_bytes": len(output)}, "", "  ")
		if e != nil || writeModelProcessEvidence(filepath.Join(p.directory, "result.json"), append(raw, '\n')) != nil {
			p.test.Error("retained child receipt write failed")
		}
	})
}

func writeModelProcessEvidence(path string, raw []byte) error {
	file, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	_, e = file.Write(raw)
	if e == nil {
		e = file.Sync()
	}
	closed := file.Close()
	if e != nil {
		return e
	}
	if closed != nil {
		return closed
	}
	directory, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	e = directory.Sync()
	closed = directory.Close()
	if e != nil {
		return e
	}
	return closed
}

func modelProcessState(t *testing.T, x humanImportFixture, id string) string {
	t.Helper()
	var state string
	if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT state FROM model_imports WHERE id=$1`, `SELECT state FROM model_imports WHERE id=?`, id)).Scan(&state); e != nil {
		t.Fatal(e)
	}
	return state
}

func TestActualModelImportProcessRecovery(t *testing.T) {
	for _, name := range []string{"normal_new_process", "killed_after_claim_new_process_recovers"} {
		t.Run(name, func(t *testing.T) {
			x := archiveSetup(t)
			v := x.start(t)
			archiveRecoveryObjects(t, x, v.ID)
			archiveQueue(t, x, v.ID)
			var firstPID int
			if name == "killed_after_claim_new_process_recovers" {
				e := x.f.Store.(transaction.SessionStore).WithSession(x.ctx, func(ctx context.Context, gate transaction.Store) error {
					hash := sha256.Sum256([]byte("model-worker-process-gate:v1:" + v.ID))
					key := int64(binary.LittleEndian.Uint64(hash[:8]) & (1<<63 - 1))
					var lock any = key
					if gate.Dialect() == transaction.MySQL {
						lock = "m20-process-gate-" + strings.ReplaceAll(v.ID, "-", "")
					}
					var acquired bool
					if e := gate.QueryRow(ctx, x.f.Statement(`SELECT pg_try_advisory_lock($1)`, `SELECT GET_LOCK(?,0)`, lock)).Scan(&acquired); e != nil || !acquired {
						t.Fatal("actual scoped publication gate unavailable", e)
					}
					held, function, trigger := true, false, false
					var child *modelProcess
					release := func(clean context.Context) {
						if !held {
							return
						}
						var unlocked bool
						if e := gate.QueryRow(clean, x.f.Statement(`SELECT pg_advisory_unlock($1)`, `SELECT RELEASE_LOCK(?)`, lock)).Scan(&unlocked); e != nil || !unlocked {
							t.Error("owned publication gate release failed", e)
						}
						held = false
					}
					defer func() {
						if child != nil {
							child.stop()
						}
						clean, stop := context.WithTimeout(context.Background(), 5*time.Second)
						defer stop()
						release(clean)
						if trigger {
							if _, e := x.f.Store.Exec(clean, x.f.Statement(`DROP TRIGGER m20_model_process_gate ON audit_events`, `DROP TRIGGER m20_model_process_gate`)); e != nil {
								t.Error("owned trigger cleanup", e)
							}
						}
						if function {
							if _, e := x.f.Store.Exec(clean, x.f.Statement(`DROP FUNCTION m20_model_process_gate_fn()`, ``)); e != nil {
								t.Error("owned function cleanup", e)
							}
						}
					}()
					if gate.Dialect() == transaction.PostgreSQL {
						x.exec(t, fmt.Sprintf(`CREATE FUNCTION m20_model_process_gate_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.import_ready' AND NEW.target_id='%s' THEN PERFORM pg_advisory_xact_lock(%d); END IF; RETURN NEW; END$$`, v.ID, key), ``)
						function = true
						x.exec(t, `CREATE TRIGGER m20_model_process_gate BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION m20_model_process_gate_fn()`, ``)
					} else {
						x.exec(t, ``, fmt.Sprintf(`CREATE TRIGGER m20_model_process_gate BEFORE INSERT ON audit_events FOR EACH ROW BEGIN DECLARE acquired INT; IF NEW.action='model.import_ready' AND NEW.target_id='%s' THEN SET acquired=GET_LOCK('%s',10); END IF; END`, v.ID, lock))
					}
					trigger = true
					child = startModelProcess(t, x, v.ID)
					firstPID = child.command.Process.Pid
					for modelProcessState(t, x, v.ID) != "verifying" {
						select {
						case <-child.done:
							t.Fatal("worker exited before durable claim", child.err)
						case <-ctx.Done():
							t.Fatal("durable claim deadline")
						case <-time.After(10 * time.Millisecond):
						}
					}
					workerState(t, x, v.ID, "verifying", 1, "")
					t.Log("Actual durable claim observed before SIGKILL: state=verifying attempts=1")
					if e := child.command.Process.Kill(); e != nil {
						t.Fatal("actual worker SIGKILL failed", e)
					}
					<-child.done
					var exit *exec.ExitError
					if value, ok := child.err.(*exec.ExitError); ok {
						exit = value
					}
					if exit == nil || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
						t.Fatal("actual killed worker exit required")
					}
					child.stop()
					release(ctx)
					// Drain the server transaction after killing the client; an OS
					// exit alone is never treated as proof of database rollback.
					if e := x.f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
						var state string
						return q.QueryRow(ctx, x.f.Statement(`SELECT state FROM model_imports WHERE id=$1 FOR UPDATE`, `SELECT state FROM model_imports WHERE id=? FOR UPDATE`, v.ID)).Scan(&state)
					}); e != nil {
						t.Fatal("old server operation drain failed", e)
					}
					for range 2 {
						workerState(t, x, v.ID, "verifying", 1, "")
						archiveNoPublication(t, x, v.ID)
					}
					file, e := os.OpenFile(filepath.Join(x.root, "import-locks", ".lock-"+v.ID), os.O_RDWR|syscall.O_NOFOLLOW, 0600)
					if e != nil {
						t.Fatal(e)
					}
					defer file.Close()
					if e = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
						t.Fatal("killed worker file lock remained", e)
					}
					if e = syscall.Flock(int(file.Fd()), syscall.LOCK_UN); e != nil {
						t.Fatal(e)
					}
					return nil
				})
				if e != nil {
					t.Fatal("actual parent session cleanup failed", e)
				}
			}
			child := startModelProcess(t, x, v.ID)
			if child.command.Process.Pid == firstPID {
				t.Fatal("different actual recovery process required")
			}
			select {
			case <-child.done:
			case <-x.ctx.Done():
				child.stop()
				t.Fatal("actual process completion deadline")
			}
			if child.err != nil {
				t.Fatal("actual recovery child failed; retain private child output", child.err)
			}
			child.stop()
			archiveReady(t, x, v.ID, 1)
			if owned, e := x.s.ProcessNext(x.ctx); owned || e != nil {
				t.Fatal("completed process command was replayed", e)
			}
			archiveReady(t, x, v.ID, 1)
		})
	}
}
