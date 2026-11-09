package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/google/uuid"
)

type expiryFixture struct {
	s       *Service
	admin   identity.Auth
	ctx     context.Context
	input   ImportRequest
	id, key string
	chunk   []byte
	digest  string
}

func newExpiryFixture(t *testing.T) expiryFixture {
	t.Helper()
	id := fixture.New(t)
	password := fixture.Bootstrap(t, id)
	admin := fixture.Login(t, id, "admin@example.invalid", password, "admin")
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	input := ImportRequest{
		WorkspaceID: admin.Me.Workspace.ID, Name: "Expiry fixture", CatalogDigest: qwenImportManifest.digest,
		ExpectedBytes: qwenImportManifest.minimumArchiveBytes, LicenseAcknowledged: true, Reason: "Test abandoned uploads",
	}
	s := &Service{
		DB: id.DB, Identity: id, Workspaces: &workspaces.Service{DB: id.DB, Identity: id}, RootDirectory: root,
		ImportExpiry: &ImportExpiryPolicy{WorkspaceID: input.WorkspaceID, InactiveFor: time.Hour},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	key := uuid.NewString()
	v, e := s.StartImport(ctx, admin, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	chunk := bytes.Repeat([]byte{0xa5}, int(ChunkBytes))
	digest := sha256.Sum256(chunk)
	x := expiryFixture{s: s, admin: admin, ctx: ctx, input: input, id: v.ID, key: key, chunk: chunk, digest: hex.EncodeToString(digest[:])}
	if _, e = x.upload(0, bytes.NewReader(chunk)); e != nil {
		t.Fatal(e)
	}
	return x
}

func (x expiryFixture) upload(offset int64, reader io.Reader) (Import, error) {
	return x.s.UploadChunk(x.ctx, x.admin, x.id, offset, ChunkBytes, x.digest, reader)
}

func (x expiryFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, e := x.s.DB.Exec(x.ctx, query, args...); e != nil {
		t.Fatal(e)
	}
}

func (x expiryFixture) age(t *testing.T) {
	t.Helper()
	x.exec(t, `UPDATE model_imports SET updated_at=clock_timestamp()-interval '2 hours' WHERE id=$1`, x.id)
}

func (x expiryFixture) uploadDirectory(id string) string {
	return filepath.Join(x.s.RootDirectory, "imports", id)
}

func (x expiryFixture) requireImport(t *testing.T, state, code string, chunks int) Import {
	t.Helper()
	v, e := x.s.GetImport(x.ctx, x.admin, x.id)
	if e != nil || v.State != state || code == "" && v.ErrorCode != nil ||
		code != "" && (v.ErrorCode == nil || *v.ErrorCode != code) {
		t.Fatalf("import state: %+v, %v; expected %s/%s", v, e, state, code)
	}
	var count int
	if e = x.s.DB.QueryRow(x.ctx, `SELECT count(*) FROM model_import_chunks WHERE import_id=$1`, x.id).Scan(&count); e != nil || count != chunks {
		t.Fatalf("chunk records: %d, %v; expected %d", count, e, chunks)
	}
	return v
}

func (x expiryFixture) requireAudit(t *testing.T, requested, finished int) {
	t.Helper()
	var want, done, matching int
	e := x.s.DB.QueryRow(x.ctx, `
		SELECT count(*) FILTER(WHERE action=$1),count(*) FILTER(WHERE action=$2),
		       count(*) FILTER(WHERE actor_user_id IS NULL AND audience='system' AND outcome='success'
		          AND target_type='model_import' AND reason=$3 AND request_id=$4 AND workspace_id=$5)
		FROM audit_events WHERE target_id=$4 AND action IN ($1,$2)`,
		importExpiryRequested, importExpired, modelImportExpired, x.id, x.input.WorkspaceID).Scan(&want, &done, &matching)
	if e != nil || want != requested || done != finished || matching != requested+finished {
		t.Fatalf("expiry audit: %d/%d, %d scoped, %v; expected %d/%d", want, done, matching, e, requested, finished)
	}
}

type expiryBlockingReader struct {
	io.Reader
	ctx     context.Context
	started chan struct{}
	resume  <-chan struct{}
}

func (r *expiryBlockingReader) Read(p []byte) (int, error) {
	if r.started != nil {
		close(r.started)
		r.started = nil
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		case <-r.resume:
		}
	}
	return r.Reader.Read(p)
}

func TestActualAbandonedImportExpiryRacesWithUpload(t *testing.T) {
	x := newExpiryFixture(t)
	x.age(t)
	old := x.requireImport(t, "uploading", "", 1)
	ctx, stop := context.WithCancel(x.ctx)
	defer stop()
	started, resume := make(chan struct{}), make(chan struct{})
	reader := &expiryBlockingReader{Reader: bytes.NewReader(x.chunk), ctx: ctx, started: started, resume: resume}
	result := make(chan error, 1)
	go func() {
		_, e := x.upload(ChunkBytes, reader)
		result <- e
	}()
	select {
	case <-started:
	case e := <-result:
		t.Fatalf("upload never reached the staging write: %v", e)
	case <-x.ctx.Done():
		t.Fatal(x.ctx.Err())
	}

	if through, e := x.s.expireNextImport(x.ctx, ""); e != nil || through != x.id {
		t.Fatalf("expiry did not yield to the in-flight writer: %q %v", through, e)
	}
	close(resume)
	if e := <-result; e != nil {
		t.Fatal(e)
	}
	target := importExpiryTarget{id: x.id, tenant: x.admin.Me.Workspace.TenantID, workspace: x.input.WorkspaceID}
	if e := x.s.expireImport(x.ctx, *x.s.ImportExpiry, target); e != nil {
		t.Fatalf("stale selection rejected fresh progress: %v", e)
	}
	fresh := x.requireImport(t, "uploading", "", 2)
	if fresh.UploadedBytes != 2*ChunkBytes || !fresh.UpdatedAt.After(old.UpdatedAt) {
		t.Fatalf("durable progress was lost: %+v", fresh)
	}
	if through, e := x.s.expireNextImport(x.ctx, ""); e != nil || through != "" {
		t.Fatalf("selected a fresh upload: %q %v", through, e)
	}

	x.age(t)
	old = x.requireImport(t, "uploading", "", 2)
	replay, e := x.upload(ChunkBytes, bytes.NewReader(x.chunk))
	if e != nil || !replay.UpdatedAt.Equal(old.UpdatedAt) {
		t.Fatalf("exact replay moved the inactivity clock: %+v %v", replay, e)
	}

	// Exercise the worker entry point used by the application.
	workerContext, stopWorker := context.WithCancel(x.ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		x.s.Run(workerContext)
	}()
	t.Cleanup(func() { stopWorker(); <-stopped })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		v, e := x.s.GetImport(x.ctx, x.admin, x.id)
		if e != nil {
			t.Fatal(e)
		}
		if v.State == "cancelled" {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("worker did not finish expiry")
		case <-tick.C:
		}
	}
	stopWorker()
	<-stopped
	v := x.requireImport(t, "cancelled", modelImportExpired, 0)
	if v.UploadedBytes != 2*ChunkBytes {
		t.Fatalf("expiry erased upload history: %+v", v)
	}
	if _, e = os.Stat(x.uploadDirectory(x.id)); !os.IsNotExist(e) {
		t.Fatalf("staging survived expiry: %v", e)
	}
	x.requireAudit(t, 1, 1)
}

type expiryUnknownStore struct {
	transaction.Store
	phase int
}

type expiryUnknownSession struct {
	transaction.Store
	phase, calls int
}

func (s expiryUnknownStore) WithSession(ctx context.Context, fn func(context.Context, transaction.Store) error) error {
	return s.Store.(transaction.SessionStore).WithSession(ctx, func(ctx context.Context, session transaction.Store) error {
		return fn(ctx, &expiryUnknownSession{Store: session, phase: s.phase})
	})
}

func (s *expiryUnknownSession) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	if e := s.Store.Within(ctx, readOnly, fn); e != nil {
		return e
	}
	s.calls++
	if s.calls == s.phase {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return nil
}

func (x expiryFixture) withUnknownCommit(t *testing.T, phase int) Service {
	t.Helper()
	store, e := x.s.repository()
	if e != nil {
		t.Fatal(e)
	}
	id := *x.s.Identity
	id.DB, id.Storage = nil, expiryUnknownStore{Store: store, phase: phase}
	worker := *x.s
	worker.DB, worker.Identity, worker.Workspaces = nil, &id, &workspaces.Service{Identity: &id}
	return worker
}

func (x expiryFixture) failAudit(t *testing.T, action string) func() {
	t.Helper()
	x.exec(t, `CREATE FUNCTION reject_expiry_audit() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.action=TG_ARGV[0] THEN RAISE EXCEPTION 'expiry audit fault'; END IF; RETURN NEW; END $$`)
	x.exec(t, `CREATE TRIGGER reject_expiry_audit BEFORE INSERT ON audit_events
		FOR EACH ROW EXECUTE FUNCTION reject_expiry_audit('`+action+`')`)
	return func() {
		x.exec(t, `DROP TRIGGER reject_expiry_audit ON audit_events`)
		x.exec(t, `DROP FUNCTION reject_expiry_audit()`)
	}
}

func TestActualAbandonedImportExpiryRecoversCommittedIntent(t *testing.T) {
	x := newExpiryFixture(t)
	x.age(t)
	unblock := x.failAudit(t, importExpiryRequested)
	if through, e := x.s.expireNextImport(x.ctx, ""); e == nil || through != x.id {
		t.Fatalf("intent audit fault escaped: %q %v", through, e)
	}
	unblock()
	x.requireImport(t, "uploading", "", 1)
	x.requireAudit(t, 0, 0)

	unknown := x.withUnknownCommit(t, 1)
	through, e := unknown.expireNextImport(x.ctx, "")
	var outcome *transaction.Error
	if through != x.id || !errors.As(e, &outcome) || outcome.Kind != transaction.CommitUnknown {
		t.Fatalf("lost intent commit reply: %q %v", through, e)
	}
	x.requireImport(t, "uploading", modelImportExpiryPending, 1)
	x.requireAudit(t, 1, 0)
	path := filepath.Join(x.s.RootDirectory, chunkPath(x.id, 0))
	if got, e := os.ReadFile(path); e != nil || !bytes.Equal(got, x.chunk) {
		t.Fatalf("unacknowledged intent deleted or changed bytes: %v", e)
	}
	if _, e = x.upload(ChunkBytes, bytes.NewReader(x.chunk)); e == nil || e.Error() != "MODEL_IMPORT_STATE_CONFLICT" {
		t.Fatalf("resume bypassed expiry: %v", e)
	}
	if _, e = x.s.QueueImport(x.ctx, x.admin, x.id, x.input.Reason, uuid.NewString()); e == nil || e.Error() != "MODEL_IMPORT_STATE_CONFLICT" {
		t.Fatalf("queue bypassed expiry: %v", e)
	}
	if _, e = x.s.CancelImport(x.ctx, x.admin, x.id, x.input.Reason, uuid.NewString()); e == nil || e.Error() != "MODEL_IMPORT_STATE_CONFLICT" {
		t.Fatalf("manual cancellation overwrote expiry: %v", e)
	}

	// Simulate interruption after one recognized entry was removed.
	stage := filepath.Join(x.uploadDirectory(x.id), "stage-1")
	if e = os.Mkdir(stage, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(stage, "config.json"), []byte("staging"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	unblock = x.failAudit(t, importExpired)
	restarted := *x.s
	if through, e = restarted.expireNextImport(x.ctx, ""); e == nil || through != x.id {
		t.Fatalf("terminal audit fault escaped: %q %v", through, e)
	}
	unblock()
	x.requireImport(t, "uploading", modelImportExpiryPending, 1)
	x.requireAudit(t, 1, 0)
	if _, e = os.Stat(x.uploadDirectory(x.id)); !os.IsNotExist(e) {
		t.Fatalf("resumed cleanup did not discard remaining staging: %v", e)
	}

	unknown = x.withUnknownCommit(t, 2)
	through, e = unknown.expireNextImport(x.ctx, "")
	if through != x.id || !errors.As(e, &outcome) || outcome.Kind != transaction.CommitUnknown {
		t.Fatalf("lost terminal commit reply: %q %v", through, e)
	}
	x.requireImport(t, "cancelled", modelImportExpired, 0)
	for _, cursor := range []string{x.id, ""} {
		if through, e = restarted.expireNextImport(x.ctx, cursor); e != nil || through != "" {
			t.Fatalf("terminal expiry replay was selected: %q %v", through, e)
		}
	}
	replay, e := x.s.StartImport(x.ctx, x.admin, x.input, x.key, uuid.NewString())
	if e != nil || replay.ID != x.id || replay.State != "cancelled" || replay.ErrorCode == nil || *replay.ErrorCode != modelImportExpired {
		t.Fatalf("expiry reason was lost on the original idempotency key: %+v %v", replay, e)
	}
	newImport, e := x.s.StartImport(x.ctx, x.admin, x.input, uuid.NewString(), uuid.NewString())
	if e != nil || newImport.ID == x.id || newImport.State != "uploading" {
		t.Fatalf("expiry did not release the workspace slot: %+v %v", newImport, e)
	}
	x.requireAudit(t, 1, 1)
}

func TestActualAbandonedImportExpiryKeepsOtherScopesAndStates(t *testing.T) {
	x := newExpiryFixture(t)
	x.age(t)
	worker := *x.s
	worker.ImportExpiry = nil
	if through, e := worker.expireNextImport(x.ctx, ""); e != nil || through != "" {
		t.Fatalf("default worker selected an abandoned upload: %q %v", through, e)
	}
	worker.ImportExpiry = &ImportExpiryPolicy{WorkspaceID: x.input.WorkspaceID, InactiveFor: time.Minute}
	if _, e := worker.expireNextImport(x.ctx, ""); e == nil || e.Error() != "MODEL_IMPORT_EXPIRY_POLICY_INVALID" {
		t.Fatalf("invalid interval activated expiry: %v", e)
	}

	other, e := x.s.Workspaces.Create(x.ctx, x.admin, "Other expiry workspace", x.admin.Me.User.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	otherInput := x.input
	otherInput.WorkspaceID = other.ID
	otherImport, e := x.s.StartImport(x.ctx, x.admin, otherInput, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = x.s.UploadChunk(x.ctx, x.admin, otherImport.ID, 0, ChunkBytes, x.digest, bytes.NewReader(x.chunk)); e != nil {
		t.Fatal(e)
	}
	x.exec(t, `UPDATE model_imports SET updated_at=clock_timestamp()-interval '2 hours' WHERE id=$1`, otherImport.ID)
	foreignTenant, foreignWorkspace, foreignImport := uuid.NewString(), uuid.NewString(), uuid.NewString()
	x.exec(t, `INSERT INTO tenants(id,name,status) VALUES($1,'Foreign expiry tenant','active')`, foreignTenant)
	x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Foreign expiry workspace','active')`, foreignWorkspace, foreignTenant)
	x.exec(t, `INSERT INTO model_imports(id,tenant_id,workspace_id,name,catalog_digest,expected_bytes,state,
		requested_by,requested_epoch,idempotency_key,fingerprint,reason,updated_at)
		SELECT $1,$2,$3,name,catalog_digest,expected_bytes,state,requested_by,requested_epoch,$4,fingerprint,reason,updated_at
		FROM model_imports WHERE id=$5`, foreignImport, foreignTenant, foreignWorkspace, uuid.NewString(), x.id)
	worker.ImportExpiry = &ImportExpiryPolicy{WorkspaceID: foreignWorkspace, InactiveFor: time.Hour}
	if through, e := worker.expireNextImport(x.ctx, ""); e != nil || through != "" {
		t.Fatalf("policy selected a workspace outside the installation: %q %v", through, e)
	}
	foreignTarget := importExpiryTarget{id: otherImport.ID, tenant: other.TenantID, workspace: other.ID}
	if e = x.s.expireImport(x.ctx, *x.s.ImportExpiry, foreignTarget); e == nil || e.Error() != "MODEL_IMPORT_STATE_CONFLICT" {
		t.Fatalf("wrong workspace bypassed the explicit policy: %v", e)
	}

	for _, state := range []string{"queued", "verifying", "failed"} {
		x.exec(t, `UPDATE model_imports SET state=$1 WHERE id=$2`, state, x.id)
		if through, e := x.s.expireNextImport(x.ctx, ""); e != nil || through != "" {
			t.Fatalf("expiry selected %s: %q %v", state, through, e)
		}
		x.requireImport(t, state, "", 1)
	}
	x.exec(t, `UPDATE model_imports SET state='uploading',uploaded_bytes=expected_bytes WHERE id=$1`, x.id)
	if through, e := x.s.expireNextImport(x.ctx, ""); e != nil || through != "" {
		t.Fatalf("expiry selected a complete upload awaiting confirmation: %q %v", through, e)
	}

	x.exec(t, `UPDATE model_imports SET uploaded_bytes=$1 WHERE id=$2`, ChunkBytes, x.id)
	object := filepath.Join(x.s.RootDirectory, "objects", x.id)
	if e = os.MkdirAll(object, 0700); e != nil {
		t.Fatal(e)
	}
	payload := filepath.Join(object, "config.json")
	if e = os.WriteFile(payload, []byte("publication sentinel"), 0600); e != nil {
		t.Fatal(e)
	}
	if through, e := x.s.expireNextImport(x.ctx, ""); through != x.id || e == nil || e.Error() != "MODEL_PUBLICATION_CONFLICT" {
		t.Fatalf("expiry did not refuse a conflicting object: %q %v", through, e)
	}
	x.requireImport(t, "uploading", "", 1)

	x.exec(t, `INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by)
		SELECT id,tenant_id,workspace_id,name,$1,catalog_digest,$2,'ready',requested_by FROM model_imports WHERE id=$3`,
		qwenImportManifest.catalog.ModelKey, qwenImportManifest.canonical, x.id)
	x.exec(t, `UPDATE model_imports SET state='ready',model_id=id,uploaded_bytes=expected_bytes WHERE id=$1`, x.id)
	if through, e := x.s.expireNextImport(x.ctx, ""); e != nil || through != "" {
		t.Fatalf("expiry selected a published import: %q %v", through, e)
	}
	x.requireImport(t, "ready", "", 1)
	if got, e := os.ReadFile(payload); e != nil || string(got) != "publication sentinel" {
		t.Fatalf("published object changed: %q %v", got, e)
	}
	if got, e := os.ReadFile(filepath.Join(x.s.RootDirectory, chunkPath(otherImport.ID, 0))); e != nil || !bytes.Equal(got, x.chunk) {
		t.Fatalf("other workspace's staged bytes changed: %v", e)
	}
	v, e := x.s.GetImport(x.ctx, x.admin, otherImport.ID)
	if e != nil || v.State != "uploading" || v.ErrorCode != nil || v.UploadedBytes != ChunkBytes {
		t.Fatalf("other workspace's upload changed: %+v %v", v, e)
	}
	var foreignState string
	var foreignCode *string
	if e = x.s.DB.QueryRow(x.ctx, `SELECT state,error_code FROM model_imports WHERE id=$1`, foreignImport).Scan(&foreignState, &foreignCode); e != nil ||
		foreignState != "uploading" || foreignCode != nil {
		t.Fatalf("other tenant's upload changed: %s %v %v", foreignState, foreignCode, e)
	}
	x.requireAudit(t, 0, 0)
}
