//go:build repository_integration && object_storage_integration

package artifactstores

import (
	"bufio"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/backup-hold.up.sql
var backupHoldSchema string

//go:embed testdata/backup-hold.down.sql
var backupHoldRollback string

const backupHoldChild = "OWNSOLID_TEST_BACKUP_HOLD_CHILD"

func checkActualDurableBackupHold(t *testing.T, g *objectStory, proxy *backupTestProxy, ready Object) {
	t.Helper()
	site := strings.Repeat("a", 64)
	h, err := g.s.backupHoldCandidate(site)
	requireBackupHoldCode(t, err, "")
	requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
	provision := func() {
		t.Helper()
		requireBackupHoldCode(t, applyHoldDDL(g, backupHoldSchema), "")
		requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
		g.exec(t, `INSERT INTO artifact_store_bindings(tenant_id,store_id,site_sha256,backend_sha256)
			VALUES($1,$2,$3,$4)`, "", "", h.tenant, h.namespace, h.site, h.backend)
		seed := `INSERT INTO artifact_backup_holds(system_identifier,database_oid,tenant_id,store_id,site_sha256,backend_sha256,cell_id)
			SELECT c.system_identifier::text,d.oid,$1,$2,$3,$4,$5 FROM pg_catalog.pg_database d
			CROSS JOIN pg_catalog.pg_control_system() c WHERE d.datname=pg_catalog.current_database()`
		_, err := g.f.PostgreSQL.Exec(g.ctx, seed, h.tenant, h.namespace, h.site, h.backend, "other-cell")
		requireHoldSQLState(t, err, "23503")
		g.exec(t, seed, "", "", h.tenant, h.namespace, h.site, h.backend, "default")
		requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "")
	}
	g.exec(t, "INSERT INTO cells(id,site_id,display_name) VALUES('other-cell','default','Other cell')", "", "")
	provision()
	requireBackupHoldCode(t, applyHoldDDL(g, backupHoldRollback), "")
	requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
	var candidates int
	if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT num_nonnulls(
		to_regclass('artifact_backup_holds'), to_regclass('artifact_store_bindings'))`).Scan(&candidates); err != nil || candidates != 0 {
		t.Fatalf("rollback left candidate tables: %d %v", candidates, err)
	}
	provision()
	foreign := h
	foreign.site = strings.Repeat("b", 64)
	requireBackupHoldCode(t, foreign.checkWriterGate(g.ctx), "OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
	foreign = h
	foreign.tenant = uuid.NewString()
	requireBackupHoldCode(t, foreign.checkWriterGate(g.ctx), "OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")

	stageData := "detached staging artifact"
	stageSHA := sha256.Sum256([]byte(stageData))
	stage, err := g.s.Stage.Write(g.ctx, uuid.NewString(), int64(len(stageData)),
		hex.EncodeToString(stageSHA[:]), strings.NewReader(stageData))
	requireBackupHoldCode(t, err, "")
	requireBackupHoldCode(t, stage.File.Close(), "")
	records, err := g.s.StagingRecords(g.ctx, g.admin, g.f.Workspace, 8)
	requireBackupHoldCode(t, err, "")
	if len(records) != 1 {
		t.Fatalf("wanted one detached staging artifact, got %d", len(records))
	}
	cleanup := func() error {
		return g.s.CleanupStaging(g.ctx, g.admin, g.f.Workspace, records[0],
			"Cleanup fixture stage", uuid.NewString())
	}

	// The data-plane writer is separately paused in this fixture. Only the
	// privileged coordinator process is killed. No provider exclusion is inferred.
	local, err := g.s.AcquireBackupFence(g.ctx)
	requireBackupHoldCode(t, err, "")
	previous := backupHoldToken{operation: uuid.NewString(), owner: uuid.NewString()}
	killBackupHoldCoordinator(t, g.ctx, h, previous)

	// A different connection pool also exercises loss of the original session.
	restarted, err := pgxpool.NewWithConfig(g.ctx, g.f.PostgreSQL.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	h.store, err = transaction.FromPostgreSQL(restarted)
	requireBackupHoldCode(t, err, "")
	requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "OBJECT_BUSY")
	op, owner := previous.operation, previous.owner
	err = h.withRow(g.ctx, func(_ transaction.Executor, row backupHoldRow, _ time.Time) error {
		previous = row.token
		return nil
	})
	requireBackupHoldCode(t, err, "")
	if previous.operation != op || previous.owner != owner || previous.generation != 1 || previous.binding.scope.CellID != "default" {
		t.Fatal("restart recovered a different hold or cell")
	}
	requireHoldSQLState(t, applyHoldDDL(g, backupHoldRollback), "55000")
	// A different local cell in the same catalog cannot use the old namespace.
	g.exec(t, "UPDATE cells SET is_local=false WHERE id='default'; UPDATE cells SET is_local=true WHERE id='other-cell'", "", "")
	requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
	requireBackupHoldCode(t, h.release(g.ctx, previous, backupHoldCopy{}), "OBJECT_BACKUP_AUTHORITY_UNAVAILABLE")
	g.exec(t, "UPDATE cells SET is_local=false WHERE id='other-cell'; UPDATE cells SET is_local=true WHERE id='default'", "", "")
	_, err = h.takeOver(g.ctx, previous, uuid.NewString(), time.Minute)
	requireBackupHoldCode(t, err, "OBJECT_BACKUP_TOKEN_INVALID")
	requireBackupHoldCode(t, h.release(g.ctx, previous, backupHoldCopy{}), "OBJECT_BACKUP_COPY_UNCONFIRMED")

	expire := func() {
		g.exec(t, "UPDATE artifact_backup_holds SET lease_until=pg_catalog.clock_timestamp()-interval '1 second'", "", "")
		requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "OBJECT_BUSY")
	}
	expire()
	replay := previous
	replay.binding.scope.CellID = "other-cell"
	_, err = h.takeOver(g.ctx, replay, uuid.NewString(), time.Minute)
	requireBackupHoldCode(t, err, "OBJECT_BACKUP_TOKEN_INVALID")

	type claim struct {
		token backupHoldToken
		err   error
	}
	claims := make(chan claim, 2)
	for range 2 {
		go func() {
			token, err := h.takeOver(g.ctx, previous, uuid.NewString(), time.Minute)
			claims <- claim{token, err}
		}()
	}
	a, b := awaitBackupValue(t, claims), awaitBackupValue(t, claims)
	if a.err != nil {
		a, b = b, a
	}
	requireBackupHoldCode(t, a.err, "")
	requireBackupHoldCode(t, b.err, "OBJECT_BACKUP_TOKEN_INVALID")
	current := a.token
	if current.operation != previous.operation || current.generation != previous.generation+1 || current.owner == previous.owner {
		t.Fatalf("takeover did not preserve the operation with a new owner and generation: %+v", current)
	}
	retry, err := h.takeOver(g.ctx, previous, current.owner, time.Minute)
	requireBackupHoldCode(t, err, "")
	if retry != current {
		t.Fatal("same takeover did not recover the persisted token")
	}
	_, err = h.takeOver(g.ctx, replay, current.owner, time.Minute)
	requireBackupHoldCode(t, err, "OBJECT_BACKUP_TOKEN_INVALID")

	requests := proxy.requests.Load()
	_, err = h.copyAndConfirm(g.ctx, previous, uuid.NewString(), local, backupCopyFixture(t), 8, 4096)
	requireBackupHoldCode(t, err, "OBJECT_BACKUP_TOKEN_INVALID")
	requireBackupHoldCode(t, h.release(g.ctx, previous, backupHoldCopy{}), "OBJECT_BACKUP_TOKEN_INVALID")
	requireBackupHoldCode(t, h.release(g.ctx, current, backupHoldCopy{}), "OBJECT_BACKUP_COPY_UNCONFIRMED")
	replay = current
	replay.binding.scope.CellID = "other-cell"
	_, err = h.copyAndConfirm(g.ctx, replay, uuid.NewString(), local, backupCopyFixture(t), 8, 4096)
	requireBackupHoldCode(t, err, "OBJECT_BACKUP_TOKEN_INVALID")
	g.exec(t, "UPDATE artifact_objects SET cell_id='other-cell' WHERE id=$1", "", "", ready.ID)
	_, err = h.copyAndConfirm(g.ctx, current, uuid.NewString(), local, backupCopyFixture(t), 8, 4096)
	requireBackupHoldCode(t, err, "OBJECT_BACKUP_SCOPE_MISMATCH")
	g.exec(t, "UPDATE artifact_objects SET cell_id='default' WHERE id=$1", "", "", ready.ID)
	requireBackupHoldCode(t, cleanup(), "OBJECT_BUSY")
	worked, err := g.s.ProcessNext(g.ctx)
	requireBackupHoldCode(t, err, "OBJECT_BUSY")
	if worked || proxy.requests.Load() != requests {
		t.Fatal("denied requests reached the data plane")
	}

	file := backupCopyFixture(t)
	first, err := h.copyAndConfirm(g.ctx, current, uuid.NewString(), local, file, 8, 4096)
	requireBackupHoldCode(t, err, "")
	if first.bytes <= ready.ExpectedBytes || !objectstore.ValidDigest(first.archive) || !objectstore.ValidDigest(first.inventory) ||
		proxy.requests.Load() <= requests {
		t.Fatal("copy confirmation did not bind a real archive")
	}
	requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "OBJECT_BUSY")
	expire()
	last, err := h.takeOver(g.ctx, current, uuid.NewString(), time.Minute)
	requireBackupHoldCode(t, err, "")
	requireBackupHoldCode(t, h.release(g.ctx, current, first), "OBJECT_BACKUP_TOKEN_INVALID")
	requireBackupHoldCode(t, h.release(g.ctx, last, first), "OBJECT_BACKUP_COPY_UNCONFIRMED")

	block := newBackupReadHold()
	proxy.hold.Store(block)
	unblock := sync.OnceFunc(func() { close(block.resume) })
	t.Cleanup(unblock)
	staleFile := backupCopyFixture(t)
	stale := make(chan error, 1)
	go func() {
		_, err := h.copyAndConfirm(g.ctx, last, uuid.NewString(), local, staleFile, 8, 4096)
		stale <- err
	}()
	awaitBackupValue(t, block.started)
	expire()
	successor, err := h.takeOver(g.ctx, last, uuid.NewString(), time.Minute)
	requireBackupHoldCode(t, err, "")
	unblock()
	requireBackupHoldCode(t, awaitBackupValue(t, stale), "OBJECT_BACKUP_TOKEN_INVALID")
	proxy.hold.Store(nil)
	last = successor
	requireBackupHoldCode(t, h.release(g.ctx, last, first), "OBJECT_BACKUP_COPY_UNCONFIRMED")

	second, err := h.copyAndConfirm(g.ctx, last, uuid.NewString(), local, backupCopyFixture(t), 8, 4096)
	requireBackupHoldCode(t, err, "")
	if second.archive != first.archive || second.id == first.id {
		t.Fatal("recapture did not verify the same bytes with a new identity")
	}
	requireBackupHoldCode(t, h.release(g.ctx, last, first), "OBJECT_BACKUP_COPY_UNCONFIRMED")
	requireBackupHoldCode(t, h.release(g.ctx, last, second), "")
	requireBackupHoldCode(t, h.release(g.ctx, last, second), "")
	replay = last
	replay.binding.scope.CellID = "other-cell"
	requireBackupHoldCode(t, h.release(g.ctx, replay, second), "OBJECT_BACKUP_TOKEN_INVALID")
	requireHoldSQLState(t, applyHoldDDL(g, backupHoldRollback), "55000")
	requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "")
	requireBackupHoldCode(t, local.Release(), "")
	requireBackupHoldCode(t, cleanup(), "")
	records, err = g.s.StagingRecords(g.ctx, g.admin, g.f.Workspace, 8)
	requireBackupHoldCode(t, err, "")
	if len(records) != 0 {
		t.Fatal("explicit release did not permit the retained staging cleanup")
	}

	newHold, err := h.acquire(g.ctx, uuid.NewString(), uuid.NewString(), time.Minute)
	requireBackupHoldCode(t, err, "")
	if newHold.generation != last.generation+1 {
		t.Fatal("released generation was not retained")
	}
	requireBackupHoldCode(t, h.release(g.ctx, last, second), "OBJECT_BACKUP_TOKEN_INVALID")
	requireBackupHoldCode(t, h.checkWriterGate(g.ctx), "OBJECT_BUSY")
	t.Log("qualified PostgreSQL upgrade, unused rollback, downgrade refusal after use, and rejected cell replay")
	t.Log("killed coordinator, serialized takeover, rejected stale in-flight confirmation and release, and preserved staging until exact copy release")
}

func applyHoldDDL(g *objectStory, script string) error {
	tx, err := g.f.PostgreSQL.Begin(g.ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(g.ctx)
	if _, err := tx.Exec(g.ctx, script); err != nil {
		return err
	}
	return tx.Commit(g.ctx)
}

func requireHoldSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != code {
		t.Fatalf("got %v, want PostgreSQL SQLSTATE %s", err, code)
	}
}

func requireBackupHoldCode(t *testing.T, err error, want string) {
	t.Helper()
	if want == "" && err == nil || err != nil && err.Error() == want {
		return
	}
	t.Fatalf("got %v, want %q", err, want)
}

func killBackupHoldCoordinator(t *testing.T, ctx context.Context, h backupHoldCandidate, token backupHoldToken) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, exe, "-test.run=^TestDurableBackupHoldCoordinator$")
	child.Env = append(os.Environ(), backupHoldChild+"=1",
		"OWNSOLID_TEST_HOLD_SCOPE="+strings.Join([]string{h.tenant, h.namespace, h.site, h.backend, token.operation, token.owner}, ":"))
	out, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	reader := bufio.NewScanner(out)
	if !reader.Scan() || reader.Text() != "held" {
		t.Fatalf("coordinator did not commit the hold: %q (%v)", reader.Text(), reader.Err())
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("coordinator did not exit from the kill")
	}
}

func TestDurableBackupHoldCoordinator(t *testing.T) {
	if os.Getenv(backupHoldChild) != "1" {
		t.Skip("only run in the isolated coordinator child")
	}
	raw, err := config.Secret(os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_DSN_FILE"))
	if err != nil {
		t.Fatal("fixture DSN unavailable")
	}
	pg, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil || pg.ConnConfig.Host != "127.0.0.1" || !strings.HasSuffix(pg.ConnConfig.Database, "_test") {
		t.Fatal("isolated fixture DSN required")
	}
	pg.ConnConfig.RuntimeParams["search_path"] = os.Getenv("PROJECT_ONE_REPOSITORY_POSTGRES_SCHEMA")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pg)
	if err != nil {
		t.Fatal("fixture database unavailable")
	}
	defer pool.Close()
	store, err := transaction.FromPostgreSQL(pool)
	requireBackupHoldCode(t, err, "")
	scope := strings.Split(os.Getenv("OWNSOLID_TEST_HOLD_SCOPE"), ":")
	if len(scope) != 6 {
		t.Fatal("fixture scope is incomplete")
	}
	h := backupHoldCandidate{store: store, tenant: scope[0], namespace: scope[1], site: scope[2], backend: scope[3]}
	token, err := h.acquire(ctx, scope[4], scope[5], time.Minute)
	requireBackupHoldCode(t, err, "")
	if token.generation != 1 {
		t.Fatalf("initial generation = %d", token.generation)
	}
	if _, err := os.Stdout.WriteString("held\n"); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
	t.Fatal("expected coordinator to be killed")
}
