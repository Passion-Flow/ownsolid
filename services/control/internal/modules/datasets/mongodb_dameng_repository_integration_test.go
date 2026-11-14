//go:build repository_integration && dataset_processor_integration && mongodb_integration && odbc_manager && cgo

package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/mongodatasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

type mongoRevokingReader struct {
	source io.Reader
	revoke func()
	done   bool
}

func (r *mongoRevokingReader) Read(p []byte) (int, error) {
	n, e := r.source.Read(p)
	if n > 0 && !r.done {
		r.done = true
		r.revoke()
	}
	return n, e
}

func TestMongoDamengActualGrantRevocationDuringDraftCopy(t *testing.T) {
	s, a, store := mongoNativeScenario(t)
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	w := a.Me.Workspace.ID
	source := s.Identity.Config.MongoDB.Sources[0]
	d, e := s.Create(ctx, a, CreateRequest{WorkspaceID: w, Name: "Native source fence " + uuid.NewString(), Origin: "Synthetic current-grant copy boundary", License: "Synthetic CC0", RightsAsserted: true, Reason: "Verify grant during source copy"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	input := MongoDBRequest{SourceID: source.ID, PromptField: "question", CompletionField: "answer", Conversion: "string", Reason: "Verify current source grant"}
	captured, e := s.mongoCapture(source)
	if e != nil {
		t.Fatal(e)
	}
	r, e := s.mongoReserve(ctx, a, w, d.ID, input, source, captured, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	data, e := s.mongoRead(ctx, source, captured, input.mapping(), false)
	if e != nil || data.Rows != 8 {
		t.Fatal("real synthetic source required", e)
	}
	sum := sha256.Sum256(data.Data)
	digest := hex.EncodeToString(sum[:])
	size := int64(len(data.Data))
	if _, e = s.Store.PutSource(ctx, r.objectID, r.attemptID, digest, size, bytes.NewReader(data.Data)); e != nil {
		t.Fatal(e)
	}
	if e = s.mongoTransaction(ctx, a, w, true, func(ctx context.Context, q transaction.Executor, scope workspaces.Workspace) error {
		_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `UPDATE "MONGODB_DATASET_IMPORTS" SET "STATE"='exported',"SOURCE_SHA256"=?,"SOURCE_BYTES"=?,"SOURCE_ROWS"=?,"FIRST_ID"=?,"LAST_ID"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_ID"=? AND "REQUESTED_BY"=? AND "ID"=? AND "ATTEMPT_ID"=?`, digest, size, data.Rows, data.FirstID, data.LastID, scope.TenantID, scope.ID, d.ID, a.Me.User.ID, r.ID, r.attemptID))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	r, e = s.MongoDBGetImport(ctx, a, w, d.ID, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	draft, e := s.CreateDraft(ctx, a, w, d.ID, DraftRequest{SourceName: "mongodb-" + r.ID + ".jsonl", SourceSHA256: digest, ExpectedBytes: size, Reason: input.Reason}, r.ID, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.mongoBindDraft(ctx, a, w, d.ID, r, draft.ID); e != nil {
		t.Fatal(e)
	}
	reader := &mongoRevokingReader{source: bytes.NewReader(data.Data), revoke: func() { s.Identity.Config.MongoDB.Sources = nil }}
	out, e := s.uploadWithSourceGuard(ctx, a, w, d.ID, draft.ID, digest, size, reader, uuid.NewString(), func(ctx context.Context, q transaction.Executor, scope workspaces.Workspace) error {
		_, e := s.mongoAttachmentWithin(ctx, q, a, scope, d.ID, r)
		return e
	})
	if !reader.done || e == nil || out.ID != "" {
		t.Fatal("revoked source projected successful upload", e)
	}
	var received *time.Time
	if e = store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "SOURCE_RECEIVED_AT" FROM "DATASET_DRAFTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_ID"=? AND "ID"=?`, a.Me.Workspace.TenantID, w, d.ID, draft.ID)).Scan(&received); e != nil {
		t.Fatal(e)
	}
	var jobs int
	if e = store.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT COUNT(*) FROM "DATASET_JOBS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_ID"=? AND "DRAFT_ID"=?`, a.Me.Workspace.TenantID, w, d.ID, draft.ID)).Scan(&jobs); e != nil {
		t.Fatal(e)
	}
	if received != nil || jobs != 0 {
		t.Fatal("source revoked during copy admitted a receipt/job")
	}
}

// This is an actual selected native-DM + real MongoDB + actual CPython gate.
// Missing native driver, fixture support, grants, or source inputs fail the gate;
// no test double or a different SQL engine can establish this qualification.
func mongoNativeScenario(t *testing.T) (*Service, identity.Auth, transaction.Store) {
	t.Helper()
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "dameng" {
		t.Fatal("explicit isolated native Dameng repository required")
	}
	f := repositoryfixture.New(t)
	if f.Store.Dialect() != transaction.Dameng {
		t.Fatal("native fixture selected a different database")
	}
	if _, ok := f.Store.(transaction.SessionStore); !ok {
		t.Fatal("native pinned SessionStore required")
	}
	account := &identity.Service{Storage: f.Store}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if _, e := f.Store.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO "WORKSPACE_MEMBERSHIPS"("TENANT_ID","WORKSPACE_ID","USER_ID","ROLE","AUTHORITY_ID") VALUES(?,?,?,'owner',?)`, f.Tenant, f.Workspace, f.User, uuid.NewString())); e != nil {
		t.Fatal(e)
	}
	var issued identity.IssuedSession
	if e := f.Store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = account.IssueSessionWithin(ctx, q, f.User, 1, "portal", "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := account.Authenticate(ctx, issued.Token, "portal")
	if e != nil {
		t.Fatal(e)
	}
	processor, root := nativeProcessor(t)
	if e = InitializeStore(root, f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	files, e := OpenStore(root, f.Tenant, 4096)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := files.Close(); e != nil {
			t.Error(e)
		}
	})
	source := mongoIntegrationSource(t)
	source.TenantID = f.Tenant
	source.WorkspaceID = f.Workspace
	p := config.DefaultMongoDBConfiguration()
	p.Enabled = true
	p.BatchSize = 2
	p.Sources = []config.MongoDBSource{source}
	account.Config.MongoDB = p
	return &Service{Identity: account, Workspaces: &workspaces.Service{Identity: account}, Store: files, Processor: processor}, a, f.Store
}

// Only the acknowledgement after a REAL successful native commit is faulted.
// The real statements, source, file copy, authorization and commit still run.
type mongoLostAckStore struct {
	transaction.Store
	armed, fired  bool
	failureWrites int
}
type mongoAckExecutor struct {
	transaction.Executor
	owner    *mongoLostAckStore
	imported *bool
}

func (q mongoAckExecutor) Exec(ctx context.Context, statement transaction.Statement) (int64, error) {
	text := reflect.ValueOf(statement).FieldByName("text").String()
	if strings.Contains(text, `UPDATE "MONGODB_DATASET_IMPORTS" SET "STATE"='failed'`) {
		q.owner.failureWrites++
	}
	n, e := q.Executor.Exec(ctx, statement)
	if e == nil && n == 1 && strings.Contains(text, `UPDATE "MONGODB_DATASET_IMPORTS" SET "STATE"='imported'`) {
		*q.imported = true
	}
	return n, e
}
func (s *mongoLostAckStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	imported := false
	e := s.Store.Within(ctx, readOnly, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, mongoAckExecutor{q, s, &imported})
	})
	if e == nil && imported && s.armed {
		s.armed = false
		s.fired = true
		return errors.Join(context.Canceled, &transaction.Error{Kind: transaction.CommitUnknown})
	}
	return e
}
func (s *mongoLostAckStore) WithSession(ctx context.Context, fn func(context.Context, transaction.Store) error) error {
	return s.Store.(transaction.SessionStore).WithSession(ctx, fn)
}

func TestMongoDamengActualImportUnknownCommitReplayAndImmutableVersion(t *testing.T) {
	s, a, base := mongoNativeScenario(t)
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	w := a.Me.Workspace.ID
	source := s.Identity.Config.MongoDB.Sources[0]
	original, e := mongodatasets.Read(ctx, source, s.Identity.Config.MongoDB, mongodatasets.Mapping{Prompt: "question", Completion: "answer", Conversion: "string"}, s.policy().Limits, false)
	if e != nil || original.Rows != 8 {
		t.Fatal("exact real eight-row synthetic source required", e)
	}
	d, e := s.Create(ctx, a, CreateRequest{WorkspaceID: w, Name: "Native MongoDB " + uuid.NewString(), Origin: "Isolated synthetic native SQL fixture", License: "Synthetic CC0", RightsAsserted: true, Reason: "Verify actual MongoDB native persistence"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	input := MongoDBRequest{SourceID: source.ID, PromptField: "question", CompletionField: "answer", Conversion: "string", Reason: "Import actual synthetic snapshot"}
	checked, e := s.MongoDBPreflight(ctx, a, w, d.ID, input)
	if e != nil || checked.Rows != 1 || len(checked.Data) != 0 {
		t.Fatal("actual preflight", e)
	}
	key := uuid.NewString()
	fault := &mongoLostAckStore{Store: base, armed: true}
	s.Identity.Storage = fault
	first, e := s.MongoDBImport(ctx, a, w, d.ID, input, key, uuid.NewString())
	if !mongoUnknown(e) || first.ID != "" || !fault.fired || fault.failureWrites != 0 {
		t.Fatal("unknown commit projected success or attempted compensation", e)
	}
	s.Identity.Storage = base
	recovered, e := s.MongoDBImport(ctx, a, w, d.ID, input, key, uuid.NewString())
	if e != nil || recovered.State != "imported" || recovered.DraftID == nil || recovered.Rows == nil || *recovered.Rows != 8 || recovered.AttemptCount != 1 {
		t.Fatal("same-key committed replay failed", e)
	}
	repeated, e := s.MongoDBImport(ctx, a, w, d.ID, input, key, uuid.NewString())
	if e != nil || repeated.ID != recovered.ID || *repeated.DraftID != *recovered.DraftID {
		t.Fatal("replay created a new target", e)
	}
	conflict := input
	conflict.Reason = "Different intent"
	if v, e := s.MongoDBImport(ctx, a, w, d.ID, conflict, key, uuid.NewString()); e == nil || e.Error() != "IDEMPOTENCY_CONFLICT" || v.ID != "" {
		t.Fatal("changed intent admitted", e)
	}
	if v, e := s.MongoDBGetImport(ctx, a, uuid.NewString(), d.ID, recovered.ID); e == nil || v.ID != "" {
		t.Fatal("cross-Workspace receipt exposed")
	}
	var audits, drafts int
	if e = base.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT COUNT(*) FROM "AUDIT_EVENTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "TARGET_ID"=? AND "ACTION"='dataset.mongodb_imported'`, a.Me.Workspace.TenantID, w, recovered.ID)).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	if e = base.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT COUNT(*) FROM "DATASET_DRAFTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_ID"=? AND "CREATION_KEY"=?`, a.Me.Workspace.TenantID, w, d.ID, recovered.ID)).Scan(&drafts); e != nil {
		t.Fatal(e)
	}
	if audits != 1 || drafts != 1 {
		t.Fatal("idempotency/audit duplication", audits, drafts)
	}
	for _, statement := range []transaction.Statement{
		transaction.SQL(transaction.Dameng, `UPDATE "MONGODB_DATASET_IMPORTS" SET "STATE"='failed',"ERROR_CODE"='STORAGE_UNAVAILABLE' WHERE "ID"=?`, recovered.ID),
		transaction.SQL(transaction.Dameng, `UPDATE "MONGODB_DATASET_IMPORTS" SET "OBJECT_ID"=? WHERE "ID"=?`, uuid.NewString(), recovered.ID),
		transaction.SQL(transaction.Dameng, `UPDATE "MONGODB_DATASET_IMPORTS" SET "SOURCE_GRANT"='{}' WHERE "ID"=?`, recovered.ID),
	} {
		// Each failed native statement ends its transaction immediately. Readback
		// starts only after rollback on a fresh transaction/connection.
		e = base.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error { _, e := q.Exec(ctx, statement); return e })
		if e == nil {
			t.Fatal("native immutable receipt guard admitted corruption")
		}
		v, e := s.MongoDBGetImport(ctx, a, w, d.ID, recovered.ID)
		if e != nil || v.State != "imported" || v.DraftID == nil || *v.DraftID != *recovered.DraftID {
			t.Fatal("failed native mutation altered receipt", e)
		}
	}
	process(t, s)
	draft, e := s.GetDraft(ctx, a, w, d.ID, *recovered.DraftID)
	if e != nil || draft.State != "editing" || draft.InvalidRows == nil || *draft.InvalidRows != 0 {
		t.Fatal("actual native inspection", e)
	}
	job, e := s.Publish(ctx, a, w, d.ID, draft.ID, PublicationRequest{ExpectedRevision: draft.Revision, Seed: "00112233445566778899aabbccddeeff", ValidationBasisPoints: 2000, Reason: "Publish native MongoDB synthetic source"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	process(t, s)
	job, e = s.GetJob(ctx, a, w, d.ID, job.ID)
	if e != nil || job.VersionID == nil {
		t.Fatal("actual publication", e)
	}
	v, e := s.GetVersion(ctx, a, w, d.ID, *job.VersionID)
	if e != nil || v.Manifest.UniqueRows != 8 {
		t.Fatal("immutable snapshot rows differ", e)
	}
	if e = s.VerifyStored(ctx); e != nil {
		t.Fatal("native MongoDB source inventory/integrity", e)
	}
	// Regrant is a different membership lifetime even when role and user match.
	if e = base.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if _, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `DELETE FROM "WORKSPACE_MEMBERSHIPS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "USER_ID"=?`, a.Me.Workspace.TenantID, w, a.Me.User.ID)); e != nil {
			return e
		}
		_, e := q.Exec(ctx, transaction.SQL(transaction.Dameng, `INSERT INTO "WORKSPACE_MEMBERSHIPS"("TENANT_ID","WORKSPACE_ID","USER_ID","ROLE","AUTHORITY_ID") VALUES(?,?,?,'owner',?)`, a.Me.Workspace.TenantID, w, a.Me.User.ID, uuid.NewString()))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if out, e := s.MongoDBGetImport(ctx, a, w, d.ID, recovered.ID); e == nil || out.ID != "" {
		t.Fatal("replaced membership exposed old receipt")
	}
	s.Identity.Config.MongoDB.Sources = nil
	if out, e := s.MongoDBGetImport(ctx, a, w, d.ID, recovered.ID); e == nil || out.ID != "" {
		t.Fatal("removed source grant exposed receipt")
	}
}

func TestMongoDamengActualSessionSlotContentionAndCancellation(t *testing.T) {
	s, _, store := mongoNativeScenario(t)
	p := s.Identity.Config.MongoDB
	p.MaximumConcurrent = 1
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	e := withMongoSessionSlot(ctx, store, p, 10, 2, func(inner context.Context) error {
		called := false
		e := withMongoSessionSlot(inner, store, p, 5, 2, func(context.Context) error { called = true; return nil })
		if e == nil || e.Error() != "MONGODB_BUSY" || called {
			t.Fatal("native concurrent slot bypass", e)
		}
		return context.Canceled
	})
	if e == nil || e.Error() != string(transaction.Cancelled) {
		t.Fatal("cancelled operation outcome", e)
	}
	if e = withMongoSessionSlot(ctx, store, p, 5, 2, func(context.Context) error { return nil }); e != nil {
		t.Fatal("native slot leaked after cancellation", e)
	}
}
