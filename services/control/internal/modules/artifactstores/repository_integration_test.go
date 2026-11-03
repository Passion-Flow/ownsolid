//go:build repository_integration && object_storage_integration

package artifactstores

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
)

// Every selected leaf calls this constructor and receives a fresh actual
// product database. A real private S3+four-management-mTLS profile is mandatory.
// No fake S3 server or simplified schema can satisfy this suite.
type objectStory struct {
	f             repositoryfixture.Fixture
	ctx           context.Context
	owner         *identity.Service
	s             *Service
	admin, portal identity.Auth
	license       *licensing.Service
	licenseNow    time.Time
}

func objectSetup(t *testing.T) *objectStory {
	return objectSetupWithS3(t, nil)
}
func objectSetupWithS3(t *testing.T, prepare func(*testing.T, config.ObjectStorageConfiguration, string) config.ObjectStorageConfiguration) *objectStory {
	t.Helper()
	path := os.Getenv("PROJECT_ONE_ACTUAL_OBJECT_STORAGE_CONFIGURATION")
	cfg, e := config.Load(path)
	if e != nil || cfg.ObjectStorage == nil || !cfg.ObjectStorage.Enabled {
		t.Fatal("ACTUAL_PRIVATE_S3_CONFIGURATION_REQUIRED")
	}
	if cfg.ObjectStorage.PartBytes > 16<<20 || cfg.ObjectStorage.MaximumObjectBytes < cfg.ObjectStorage.PartBytes+17 {
		t.Fatal("BOUNDED_ACTUAL_S3_FIXTURE_PROFILE_REQUIRED")
	}
	if !regexp.MustCompile(`^m22-fixture-[a-f0-9]{32}$`).MatchString(cfg.ObjectStorage.Bucket) {
		t.Fatal("EXACT_DISPOSABLE_SYNTHETIC_S3_BUCKET_REQUIRED")
	}
	for _, value := range cfg.ObjectStorage.DialAddresses {
		ip, e := netip.ParseAddr(value)
		if e != nil || !ip.IsPrivate() && !ip.IsLoopback() {
			t.Fatal("PRIVATE_FIXTURE_S3_DIAL_REQUIRED")
		}
	}
	f := repositoryfixture.New(t)
	ctx, cancel := context.WithTimeout(t.Context(), 180*time.Second)
	t.Cleanup(cancel)
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	state, e := filepath.EvalSymlinks(os.Getenv("PROJECT_ONE_STATE_ROOT"))
	if e != nil || !strings.HasPrefix(root, state+string(os.PathSeparator)) {
		t.Fatal("OWNED_EXTERNAL_FIXTURE_ROOT_REQUIRED")
	}
	c := *cfg.ObjectStorage
	stageRoot, modelRoot, datasetRoot := filepath.Join(root, "stage"), filepath.Join(root, "models"), filepath.Join(root, "datasets")
	for _, directory := range []string{stageRoot, modelRoot, datasetRoot} {
		if e = os.Mkdir(directory, 0700); e != nil {
			t.Fatal(e)
		}
	}
	c.StagingRoot = stageRoot
	if prepare != nil {
		c = prepare(t, c, root)
		if c.Validate(modelRoot, datasetRoot) != nil {
			t.Fatal("EXPLICIT_PRIVATE_FIXTURE_TRANSPORT_REQUIRED")
		}
	}
	cfg.ObjectStorage = &c
	cfg.Models.ArtifactRoot = modelRoot
	cfg.Datasets.ArtifactRoot = datasetRoot
	if e = models.InitializeStore(modelRoot, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	if e = datasets.InitializeStore(datasetRoot, f.Tenant, os.Geteuid(), os.Getegid()); e != nil {
		t.Fatal(e)
	}
	datasetStore, e := datasets.OpenStore(datasetRoot, f.Tenant, cfg.Policy().Datasets.MinimumFreeBytes)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := datasetStore.Close(); e != nil {
			t.Error(e)
		}
	})
	processor, e := datasets.NewProcessor(cfg.Datasets, cfg.Policy().Datasets)
	if e != nil {
		t.Fatal("ACTUAL_EXISTING_DATASET_PROCESSOR_REQUIRED", e)
	}
	if e = objectstore.InitializeStage(c, f.Tenant); e != nil {
		t.Fatal(e)
	}
	stage, e := objectstore.OpenStage(c, f.Tenant)
	if e != nil {
		t.Fatal(e)
	}
	transport, e := objectstore.NewS3(c)
	if e != nil {
		stage.Close()
		t.Fatal(e)
	}
	owner := &identity.Service{Storage: f.Store, Config: cfg}
	w := &workspaces.Service{Identity: owner}
	m := &models.Service{Identity: owner, Workspaces: w, RootDirectory: modelRoot}
	d := &datasets.Service{Identity: owner, Workspaces: w, Store: datasetStore, Processor: processor}
	g := &objectStory{f: f, ctx: ctx, owner: owner}
	g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", `INSERT INTO "WORKSPACE_MEMBERSHIPS"("TENANT_ID","WORKSPACE_ID","USER_ID","ROLE") VALUES(?,?,?,'owner')`, f.Tenant, f.Workspace, f.User)
	g.admin = g.session(t, f.User, "admin")
	g.portal = g.session(t, f.User, "portal")
	g.initializeLicense(t)
	s, e := NewService(ctx, owner, w, m, d, c, transport, stage)
	if e != nil {
		transport.Close()
		stage.Close()
		t.Fatal("ACTUAL_PRODUCT_SCHEMA_AND_PRIVATE_ADMISSION_REQUIRED", e)
	}
	g.s = s
	t.Cleanup(func() {
		g.cleanup(t)
		closed, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if e := s.Close(closed); e != nil {
			t.Error("owned S3/stage shutdown", e)
		}
	})

	return g
}
func (g *objectStory) exec(t *testing.T, pg, my, dm string, args ...any) {
	t.Helper()
	if _, e := g.f.Store.Exec(g.ctx, statement(g.f.Store, pg, my, dm, args...)); e != nil {
		t.Fatal("actual object fixture SQL", e)
	}
}
func (g *objectStory) count(t *testing.T, pg, my, dm string, args ...any) int {
	t.Helper()
	var n int
	if e := g.f.Store.QueryRow(g.ctx, statement(g.f.Store, pg, my, dm, args...)).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func (g *objectStory) session(t *testing.T, user, audience string) identity.Auth {
	t.Helper()
	var issued identity.IssuedSession
	if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
		var e error
		issued, e = g.owner.IssueSessionWithin(ctx, q, user, 1, audience, "", uuid.NewString(), nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	a, e := g.owner.Authenticate(g.ctx, issued.Token, audience)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func (g *objectStory) recipient(t *testing.T, role string) identity.Auth {
	t.Helper()
	user := uuid.NewString()
	g.exec(t, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$2,'Synthetic recipient','en','active',false)", "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic recipient','en','active',false)", `INSERT INTO "USERS"("ID","EMAIL_CANONICAL","EMAIL_DISPLAY","DISPLAY_NAME","LOCALE","STATUS","IS_PLATFORM_ADMIN") VALUES(?,?,?,'Synthetic recipient','en','active',0)`, recipientArgs(g.f.Store, user)...)
	g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,$4)", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,?)", `INSERT INTO "WORKSPACE_MEMBERSHIPS"("TENANT_ID","WORKSPACE_ID","USER_ID","ROLE") VALUES(?,?,?,?)`, g.f.Tenant, g.f.Workspace, user, role)
	return g.session(t, user, "portal")
}
func recipientArgs(q transaction.Executor, user string) []any {
	email := user + "@example.invalid"
	if positionalArguments(q) {
		return []any{user, email, email}
	}
	return []any{user, email}
}
func (g *objectStory) create(t *testing.T, source []byte) (Object, CreateRequest, string) {
	t.Helper()
	sum := sha256.Sum256(source)
	input := CreateRequest{g.f.Workspace, "Actual synthetic S3 object", "dataset_source", int64(len(source)), hex.EncodeToString(sum[:]), "Create exact synthetic object"}
	key := uuid.NewString()
	o, e := g.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	return o, input, key
}
func (g *objectStory) work(t *testing.T) {
	t.Helper()
	worked, e := g.s.ProcessNext(g.ctx)
	if e != nil || !worked {
		t.Fatal("actual prepared effect", e, worked)
	}
}
func (g *objectStory) parts(t *testing.T, o Object, source []byte) {
	t.Helper()
	for number, offset := int32(1), int64(0); offset < int64(len(source)); number, offset = number+1, offset+o.PartBytes {
		end := offset + o.PartBytes
		if end > int64(len(source)) {
			end = int64(len(source))
		}
		part := source[offset:end]
		sum := sha256.Sum256(part)
		if _, e := g.s.PutPart(g.ctx, g.portal, g.f.Workspace, o.ID, objectstore.Part{Number: number, Bytes: int64(len(part)), SHA256: hex.EncodeToString(sum[:])}, bytes.NewReader(part), uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
}
func (g *objectStory) ready(t *testing.T, source []byte) Object {
	t.Helper()
	o, _, _ := g.create(t, source)
	g.work(t)
	g.parts(t, o, source)
	if _, e := g.s.Complete(g.ctx, g.portal, g.f.Workspace, o.ID, "Complete exact synthetic object", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	g.work(t)
	v, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, o.ID, "")
	if e != nil || v.State != "ready" {
		t.Fatal("whole S3 verification", e)
	}
	return v
}
func (g *objectStory) expireOperation(t *testing.T, object string) {
	g.exec(t, "UPDATE artifact_objects SET operation_expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE artifact_objects SET operation_expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_EXPIRES_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6))-NUMTODSINTERVAL(1,'SECOND') WHERE "ID"=?`, object)
}
func objectZero(t *testing.T, v Object, e error, code string) {
	t.Helper()
	if e == nil || code != "" && e.Error() != code || !reflect.DeepEqual(v, Object{}) {
		t.Fatal("failed/unknown operation projected object", e, code)
	}
}
func (g *objectStory) cleanup(t *testing.T) {
	t.Helper()
	ctx, done := context.WithTimeout(context.Background(), 30*time.Second)
	defer done()
	rows, e := g.f.Store.Query(ctx, statement(g.f.Store, "SELECT "+pgObjectFields+" FROM artifact_objects WHERE tenant_id=$1 AND store_id=$2", "SELECT "+myObjectFields+" FROM artifact_objects WHERE tenant_id=? AND store_id=?", `SELECT `+dmObjectFields+` FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "STORE_ID"=?`, g.f.Tenant, g.s.Stage.Namespace()))
	if e != nil {
		t.Error("retained synthetic namespace: metadata unreadable", g.f.Tenant)
		return
	}
	objects := []storedObject{}
	for rows.Next() {
		o, e := scanObject(rows)
		if e != nil {
			rows.Close()
			t.Error(e)
			return
		}
		objects = append(objects, o)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		t.Error(e)
		return
	}
	for _, o := range objects {
		t.Logf("OBJECT_FIXTURE_RECOVERY tenant=%s workspace=%s object=%s", o.Tenant, o.WorkspaceID, o.ID)
		uploads, e := g.s.Transport.FindUploads(ctx, o.ref())
		if e != nil {
			t.Error("retained exact synthetic uploads", o.ID, e)
			continue
		}
		for _, u := range uploads {
			if e = g.s.Transport.Abort(ctx, o.ref(), u.ID); e != nil {
				t.Error("retained exact synthetic upload", o.ID, e)
			}
		}
		remaining, e := g.s.Transport.FindUploads(ctx, o.ref())
		if e != nil || len(remaining) != 0 {
			t.Error("synthetic multipart cleanup unconfirmed", o.ID, e)
			continue
		}
		info, e := g.s.Transport.Head(ctx, o.ref())
		if e != nil && e.Error() == "OBJECT_NOT_FOUND" {
			continue
		}
		if e != nil || info.Metadata["object-id"] != o.ID {
			t.Error("retained ambiguous synthetic object", o.ID)
			continue
		}
		if e = g.s.Transport.Delete(ctx, o.ref(), info.ETag); e != nil {
			t.Error("retained exact synthetic object", o.ID, e)
		} else if _, e = g.s.Transport.Head(ctx, o.ref()); e == nil || e.Error() != "OBJECT_NOT_FOUND" {
			t.Error("synthetic removal unconfirmed", o.ID)
		}
	}
}

type objectOutcomeStore struct {
	transaction.Store
	unknown, rollback bool
}

func (s *objectOutcomeStore) Within(ctx context.Context, ro bool, fn func(context.Context, transaction.Executor) error) error {
	mutated := false
	e := s.Store.Within(ctx, ro, func(ctx context.Context, q transaction.Executor) error {
		before, err := objectOutcomeSnapshot(ctx, q)
		if err != nil {
			return err
		}
		if e := fn(ctx, q); e != nil {
			return e
		}
		after, err := objectOutcomeSnapshot(ctx, q)
		if err != nil {
			return err
		}
		mutated = !reflect.DeepEqual(before, after)
		if s.rollback && !ro && mutated {
			return errors.New("EXPLICIT_FIXTURE_ROLLBACK")
		}
		return nil
	})
	if e == nil && !ro && mutated && s.unknown {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return e
}

// Explicit fault fixture observes real object mutations, never SQL text/secrets.
func objectOutcomeSnapshot(ctx context.Context, q transaction.Executor) ([]string, error) {
	rows, err := q.Query(ctx, statement(q,
		"SELECT id::text,state,COALESCE(operation_phase,''),COALESCE(operation_id::text,'') FROM artifact_objects ORDER BY id LIMIT 100",
		"SELECT id,state,COALESCE(operation_phase,''),COALESCE(operation_id,'') FROM artifact_objects ORDER BY id LIMIT 100",
		`SELECT "ID","STATE",COALESCE("OPERATION_PHASE",''),COALESCE("OPERATION_ID",'') FROM "ARTIFACT_OBJECTS" ORDER BY "ID" LIMIT 100`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, state, phase, operation string
		if err = rows.Scan(&id, &state, &phase, &operation); err != nil {
			return nil, err
		}
		out = append(out, id, state, phase, operation)
	}
	return out, rows.Err()
}

func TestPairedPrivateObjectStorage(t *testing.T) {
	for _, leaf := range []string{"create_replay_conflict_utc", "multipart_resume_whole_private_download", "creator_current_role", "workspace_isolation", "exact_read_grant_revocation", "ingest_grant_metadata_without_read", "grant_recipient_epoch_fence", "grant_issuer_membership_fence", "audit_rollback_zero", "commit_unknown_zero_readback", "rollback_zero_no_intent", "explicit_unknown_create_reconcile", "prepared_part_interruption_reconcile", "exact_delete_positive_absence", "expiry_cleanup_intent", "restore_fences_pending_and_grants", "snapshot_roundtrip_integrity", "staging_owned_cleanup_and_reference", "immutable_object_namespace_and_digest", "immutable_part_rebind", "immutable_grant_authority", "immutable_ingestion_input", "worker_claim_unknown_zero", "prepared_ingestion_cancel_replay", "prepared_ingestion_cancel_audit_rollback", "prepared_ingestion_cancel_unknown_zero", "claimed_ingestion_cancel_refused", "prepared_ingestion_current_authority", "prepared_worker_origin_session_revoked", "prepared_worker_original_digest_changed", "prepared_legacy_authority_requires_explicit_reconcile", "license_expiry_denies_new_object_work", "license_expiry_preserves_accepted_completion", "license_corrupt_history_fences_prepared_worker", "license_observation_uses_single_connection", "worker_loop_quarantines_revoked_session", "worker_quarantine_rollback_remains_fatal", "worker_quarantine_unknown_remains_fatal"} {
		t.Run(leaf, func(t *testing.T) {
			g := objectSetup(t)
			source := []byte("Synthetic private S3 source \u4e16\u754c\n")
			switch leaf {
			case "worker_loop_quarantines_revoked_session", "worker_quarantine_rollback_remains_fatal", "worker_quarantine_unknown_remains_fatal":
				g.workerContinuity(t, leaf, source)
			case "license_expiry_denies_new_object_work", "license_expiry_preserves_accepted_completion", "license_corrupt_history_fences_prepared_worker", "license_observation_uses_single_connection":
				g.licenseBoundary(t, leaf, source)
			case "prepared_worker_origin_session_revoked", "prepared_worker_original_digest_changed", "prepared_legacy_authority_requires_explicit_reconcile":
				g.workerAuthority(t, leaf, source)
			case "worker_claim_unknown_zero":
				g.workerClaimUnknown(t, source)
			case "prepared_ingestion_cancel_replay", "prepared_ingestion_cancel_audit_rollback", "prepared_ingestion_cancel_unknown_zero", "claimed_ingestion_cancel_refused", "prepared_ingestion_current_authority":
				g.ingestionMaintenance(t, leaf, source)
			case "create_replay_conflict_utc":
				baseline := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events", `SELECT COUNT(*) FROM "AUDIT_EVENTS"`)
				o, input, key := g.create(t, source)
				again, e := g.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
				if e != nil || again.ID != o.ID || o.CreatedAt.Location() != time.UTC || o.ExpiresAt.Location() != time.UTC {
					t.Fatal("replay/UTC", e)
				}
				input.Name = "Changed immutable name"
				v, e := g.s.Create(g.ctx, g.portal, input, key, uuid.NewString())
				objectZero(t, v, e, "IDEMPOTENCY_CONFLICT")
				if g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events", `SELECT COUNT(*) FROM "AUDIT_EVENTS"`) != baseline+1 {
					t.Fatal("replay audit delta")
				}
			case "multipart_resume_whole_private_download":
				source = bytes.Repeat([]byte{'x'}, int(g.s.Configuration.PartBytes)+17)
				o := g.ready(t, source)
				parts, e := g.s.Parts(g.ctx, g.portal, g.f.Workspace, o.ID)
				if e != nil || len(parts) != 2 {
					t.Fatal("real multipart shape", e)
				}
				download, e := g.s.Download(g.ctx, g.portal, g.f.Workspace, o.ID, "")
				if e != nil {
					t.Fatal(e)
				}
				raw, e := io.ReadAll(download.File)
				download.File.Close()
				if e != nil || !bytes.Equal(raw, source) {
					t.Fatal("private descriptor bytes", e)
				}
			case "creator_current_role":
				g.exec(t, "UPDATE workspace_memberships SET role='viewer' WHERE user_id=$1", "UPDATE workspace_memberships SET role='viewer' WHERE user_id=?", `UPDATE "WORKSPACE_MEMBERSHIPS" SET "ROLE"='viewer' WHERE "USER_ID"=?`, g.f.User)
				sum := sha256.Sum256(source)
				v, e := g.s.Create(g.ctx, g.portal, CreateRequest{g.f.Workspace, "Denied", "dataset_source", int64(len(source)), hex.EncodeToString(sum[:]), "Current viewer cannot upload"}, uuid.NewString(), uuid.NewString())
				objectZero(t, v, e, "ACCESS_DENIED")
			case "workspace_isolation":
				o := g.ready(t, source)
				other := uuid.NewString()
				g.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other synthetic scope','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other synthetic scope','active')", `INSERT INTO "WORKSPACES"("ID","TENANT_ID","NAME","STATUS") VALUES(?,?,'Other synthetic scope','active')`, other, g.f.Tenant)
				v, e := g.s.Get(g.ctx, g.admin, other, o.ID, "")
				objectZero(t, v, e, "RESOURCE_NOT_FOUND")
			case "exact_read_grant_revocation", "ingest_grant_metadata_without_read", "grant_recipient_epoch_fence", "grant_issuer_membership_fence":
				o := g.ready(t, source)
				recipient := g.recipient(t, "member")
				action := "read"
				if leaf == "ingest_grant_metadata_without_read" {
					action = "ingest_dataset"
				}
				grant, e := g.s.CreateGrant(g.ctx, g.portal, g.f.Workspace, o.ID, GrantRequest{recipient.Me.User.ID, action, 60, "Grant exact synthetic action"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				if action == "read" {
					content, e := g.s.Download(g.ctx, recipient, g.f.Workspace, o.ID, grant.ID)
					if e != nil {
						t.Fatal("positive exact read grant", e)
					}
					raw, e := io.ReadAll(content.File)
					content.File.Close()
					if e != nil || !bytes.Equal(raw, source) {
						t.Fatal("granted content mismatch", e)
					}
				}
				if _, e = g.s.Get(g.ctx, recipient, g.f.Workspace, o.ID, grant.ID); e != nil {
					t.Fatal("exact granted metadata", e)
				}
				if leaf == "ingest_grant_metadata_without_read" {
					d, e := g.s.Download(g.ctx, recipient, g.f.Workspace, o.ID, grant.ID)
					if e == nil || d.File != nil {
						t.Fatal("ingest grant escalated to read")
					}
					break
				}
				if leaf == "grant_recipient_epoch_fence" {
					g.exec(t, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=?", `UPDATE "USERS" SET "AUTH_EPOCH"="AUTH_EPOCH"+1 WHERE "ID"=?`, recipient.Me.User.ID)
				} else if leaf == "grant_issuer_membership_fence" {
					g.exec(t, "DELETE FROM workspace_memberships WHERE user_id=$1", "DELETE FROM workspace_memberships WHERE user_id=?", `DELETE FROM "WORKSPACE_MEMBERSHIPS" WHERE "USER_ID"=?`, g.f.User)
					g.exec(t, "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES($1,$2,$3,'owner')", "INSERT INTO workspace_memberships(tenant_id,workspace_id,user_id,role) VALUES(?,?,?,'owner')", `INSERT INTO "WORKSPACE_MEMBERSHIPS"("TENANT_ID","WORKSPACE_ID","USER_ID","ROLE") VALUES(?,?,?,'owner')`, g.f.Tenant, g.f.Workspace, g.f.User)
				} else {
					if e = g.s.RevokeGrant(g.ctx, g.portal, g.f.Workspace, o.ID, grant.ID, "Revoke exact synthetic grant", uuid.NewString()); e != nil {
						t.Fatal(e)
					}
				}
				d, e := g.s.Download(g.ctx, recipient, g.f.Workspace, o.ID, grant.ID)
				if e == nil || d.File != nil {
					t.Fatal("revoked authority projected descriptor")
				}
			case "audit_rollback_zero":
				g.exec(t, "CREATE FUNCTION object_fixture_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='object.created' THEN RAISE EXCEPTION 'explicit audit rollback'; END IF; RETURN NEW; END;$$", "CREATE TRIGGER object_fixture_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW BEGIN IF NEW.action='object.created' THEN SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='explicit audit rollback'; END IF; END", `CREATE TRIGGER "OBJECT_FIXTURE_AUDIT_FAILURE" BEFORE INSERT ON "AUDIT_EVENTS" FOR EACH ROW BEGIN IF :NEW."ACTION"='object.created' THEN RAISE_APPLICATION_ERROR(-20024,'EXPLICIT_OBJECT_AUDIT_ROLLBACK'); END IF; END;`)
				if g.f.Store.Dialect() == transaction.PostgreSQL {
					g.exec(t, "CREATE TRIGGER object_fixture_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION object_fixture_audit_failure()", "", "")
				}
				sum := sha256.Sum256(source)
				v, e := g.s.Create(g.ctx, g.portal, CreateRequest{g.f.Workspace, "Rollback", "dataset_source", int64(len(source)), hex.EncodeToString(sum[:]), "Force atomic audit rollback"}, uuid.NewString(), uuid.NewString())
				objectZero(t, v, e, "")
				if g.count(t, "SELECT count(*) FROM artifact_objects", "SELECT count(*) FROM artifact_objects", `SELECT COUNT(*) FROM "ARTIFACT_OBJECTS"`) != 0 {
					t.Fatal("intent escaped audit rollback")
				}
			case "commit_unknown_zero_readback", "rollback_zero_no_intent":
				wrapped := &objectOutcomeStore{Store: g.f.Store, unknown: leaf == "commit_unknown_zero_readback", rollback: leaf == "rollback_zero_no_intent"}
				g.owner.Storage = wrapped
				sum := sha256.Sum256(source)
				v, e := g.s.Create(g.ctx, g.portal, CreateRequest{g.f.Workspace, "Unknown", "dataset_source", int64(len(source)), hex.EncodeToString(sum[:]), "Force actual commit outcome control"}, uuid.NewString(), uuid.NewString())
				g.owner.Storage = g.f.Store
				objectZero(t, v, e, "")
				expected := 0
				if wrapped.unknown {
					expected = 1
				}
				if g.count(t, "SELECT count(*) FROM artifact_objects", "SELECT count(*) FROM artifact_objects", `SELECT COUNT(*) FROM "ARTIFACT_OBJECTS"`) != expected {
					t.Fatal("actual outcome readback mismatch")
				}
			case "explicit_unknown_create_reconcile":
				o, _, _ := g.create(t, source)
				var current storedObject
				if e := g.s.within(g.ctx, g.portal, g.f.Workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
					var e error
					current, e = g.s.objectWithin(ctx, q, w, o.ID)
					return e
				}); e != nil {
					t.Fatal(e)
				}
				if e := g.s.sent(g.ctx, g.portal, current); e != nil {
					t.Fatal(e)
				}
				if _, e := g.s.Transport.Create(g.ctx, current.ref(), metadata(current)); e != nil {
					t.Fatal(e)
				}
				// The real upload exists, but this controlled checkpoint discards
				// its acknowledgment before product metadata can adopt the ID.
				g.expireOperation(t, o.ID)
				v, e := g.s.Reconcile(g.ctx, g.portal, g.f.Workspace, o.ID, "Inspect lost create acknowledgment", uuid.NewString())
				if e != nil || v.State != "uploading" {
					t.Fatal("exact existing real upload not adopted", e)
				}

			case "prepared_part_interruption_reconcile":
				o, _, _ := g.create(t, source)
				g.work(t)
				sum := sha256.Sum256(source)
				v, e := g.s.PutPart(g.ctx, g.portal, g.f.Workspace, o.ID, objectstore.Part{Number: 1, Bytes: int64(len(source)), SHA256: hex.EncodeToString(sum[:])}, bytes.NewReader([]byte("truncated")), uuid.NewString())
				objectZero(t, v, e, "")
				if _, e = g.s.Reconcile(g.ctx, g.portal, g.f.Workspace, o.ID, "Fence interrupted staged part", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				g.parts(t, o, source)
			case "exact_delete_positive_absence":
				o := g.ready(t, source)
				v, e := g.s.Delete(g.ctx, g.portal, g.f.Workspace, o.ID, "Delete exact synthetic object", uuid.NewString())
				if e != nil || v.State != "deleting" {
					t.Fatal(e)
				}
				g.work(t)
				v, e = g.s.Get(g.ctx, g.portal, g.f.Workspace, o.ID, "")
				if e != nil || v.State != "deleted" {
					t.Fatal("positive remote deletion", e)
				}
			case "expiry_cleanup_intent":
				o := g.ready(t, source)
				g.exec(t, "UPDATE artifact_objects SET upload_expires_at=clock_timestamp()-interval '2 seconds',expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", "UPDATE artifact_objects SET upload_expires_at=TIMESTAMPADD(SECOND,-2,UTC_TIMESTAMP(6)),expires_at=TIMESTAMPADD(SECOND,-1,UTC_TIMESTAMP(6)) WHERE id=?", `UPDATE "ARTIFACT_OBJECTS" SET "UPLOAD_EXPIRES_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6))-NUMTODSINTERVAL(2,'SECOND'),"EXPIRES_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6))-NUMTODSINTERVAL(1,'SECOND') WHERE "ID"=?`, o.ID)
				d, e := g.s.Download(g.ctx, g.portal, g.f.Workspace, o.ID, "")
				if e == nil || d.File != nil {
					t.Fatal("expired content projected")
				}
				result, e := g.s.Expire(g.ctx, g.admin, g.f.Workspace, 2, "Queue expired fixture removal", uuid.NewString())
				if e != nil || len(result.Queued) != 1 {
					t.Fatal(e)
				}
				g.work(t)
			case "restore_fences_pending_and_grants":
				ready := g.ready(t, source)
				recipient := g.recipient(t, "member")
				grant, e := g.s.CreateGrant(g.ctx, g.portal, g.f.Workspace, ready.ID, GrantRequest{recipient.Me.User.ID, "read", 60, "Grant before restore fence"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				o, _, _ := g.create(t, source)
				if e := g.f.Store.Within(g.ctx, false, func(ctx context.Context, q transaction.Executor) error {
					return FenceAfterRestoreWithin(ctx, q, g.f.Tenant)
				}); e != nil {
					t.Fatal(e)
				}
				denied, e := g.s.Download(g.ctx, recipient, g.f.Workspace, ready.ID, grant.ID)
				if e == nil || denied.File != nil {
					t.Fatal("restored exact grant remained active")
				}
				if worked, e := g.s.ProcessNext(g.ctx); e != nil || worked {
					t.Fatal("restored uncertain effect relaunched", e)
				}
				v, e := g.s.Reconcile(g.ctx, g.portal, g.f.Workspace, o.ID, "Inspect restored exact intent", uuid.NewString())
				objectZero(t, v, e, "OBJECT_REMOTE_OUTCOME_UNKNOWN")
			case "snapshot_roundtrip_integrity":
				o := g.ready(t, source)
				download, e := g.s.Snapshot(g.ctx, g.admin, g.f.Workspace, o.ID)
				if e != nil {
					t.Fatal(e)
				}
				defer download.File.Close()
				manifest := SnapshotManifest{snapshotSchema, g.f.Tenant, g.f.Workspace, o.ID, g.s.Stage.Namespace(), g.s.Stage.Identity(), o.Purpose, o.SHA256, o.ExpectedBytes}
				if e = VerifySnapshot(g.ctx, download.File, manifest); e != nil {
					t.Fatal(e)
				}
			case "immutable_object_namespace_and_digest":
				o, _, _ := g.create(t, source)
				for _, pair := range [][3]string{{"UPDATE artifact_objects SET sha256=$2 WHERE id=$1", "UPDATE artifact_objects SET sha256=? WHERE id=?", `UPDATE "ARTIFACT_OBJECTS" SET "SHA256"=? WHERE "ID"=?`}, {"UPDATE artifact_objects SET store_id=$2 WHERE id=$1", "UPDATE artifact_objects SET store_id=? WHERE id=?", `UPDATE "ARTIFACT_OBJECTS" SET "STORE_ID"=? WHERE "ID"=?`}} {
					value := strings.Repeat("b", 64)
					if strings.Contains(pair[0], "store_id") {
						value = uuid.NewString()
					}
					args := []any{o.ID, value}
					if positionalArguments(g.f.Store) {
						args = []any{value, o.ID}
					}
					if _, e := g.f.Store.Exec(g.ctx, statement(g.f.Store, pair[0], pair[1], pair[2], args...)); e == nil {
						t.Fatal("immutable object rebound")
					}
				}
				if _, e := g.f.Store.Exec(g.ctx, statement(g.f.Store, "UPDATE artifact_objects SET expires_at=expires_at+interval '1 second' WHERE id=$1", "UPDATE artifact_objects SET expires_at=TIMESTAMPADD(SECOND,1,expires_at) WHERE id=?", `UPDATE "ARTIFACT_OBJECTS" SET "EXPIRES_AT"="EXPIRES_AT"+NUMTODSINTERVAL(1,'SECOND') WHERE "ID"=?`, o.ID)); e == nil {
					t.Fatal("object lifetime extended")
				}
				current, e := g.s.Get(g.ctx, g.portal, g.f.Workspace, o.ID, "")
				if e != nil || current.SHA256 != o.SHA256 {
					t.Fatal("failed mutation changed object", e)
				}
			case "immutable_part_rebind":
				o := g.ready(t, source)
				if _, e := g.f.Store.Exec(g.ctx, statement(g.f.Store, "UPDATE artifact_object_parts SET sha256=$2 WHERE object_id=$1", "UPDATE artifact_object_parts SET sha256=? WHERE object_id=?", `UPDATE "ARTIFACT_OBJECT_PARTS" SET "SHA256"=? WHERE "OBJECT_ID"=?`, objectMutationArgs(g.f.Store, o.ID, strings.Repeat("b", 64))...)); e == nil {
					t.Fatal("uploaded part rebound")
				}
				parts, e := g.s.Parts(g.ctx, g.portal, g.f.Workspace, o.ID)
				if e != nil || len(parts) != 1 || parts[0].SHA256 != o.SHA256 {
					t.Fatal("failed mutation changed part", e)
				}
			case "immutable_grant_authority":
				o := g.ready(t, source)
				recipient := g.recipient(t, "member")
				grant, e := g.s.CreateGrant(g.ctx, g.portal, g.f.Workspace, o.ID, GrantRequest{recipient.Me.User.ID, "read", 60, "Create immutable exact grant"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = g.f.Store.Exec(g.ctx, statement(g.f.Store, "UPDATE artifact_object_grants SET action='ingest_dataset' WHERE id=$1", "UPDATE artifact_object_grants SET action='ingest_dataset' WHERE id=?", `UPDATE "ARTIFACT_OBJECT_GRANTS" SET "ACTION"='ingest_dataset' WHERE "ID"=?`, grant.ID)); e == nil {
					t.Fatal("grant action escalated")
				}
				if _, e = g.f.Store.Exec(g.ctx, statement(g.f.Store, "UPDATE artifact_object_grants SET expires_at=expires_at+interval '1 second' WHERE id=$1", "UPDATE artifact_object_grants SET expires_at=TIMESTAMPADD(SECOND,1,expires_at) WHERE id=?", `UPDATE "ARTIFACT_OBJECT_GRANTS" SET "EXPIRES_AT"="EXPIRES_AT"+NUMTODSINTERVAL(1,'SECOND') WHERE "ID"=?`, grant.ID)); e == nil {
					t.Fatal("grant lifetime extended")
				}
			case "immutable_ingestion_input":
				o := g.ready(t, source)
				dataset, e := g.s.Datasets.Create(g.ctx, g.portal, datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Immutable synthetic ingestion target", Origin: "Synthetic fixture", License: "Synthetic fixture", RightsAsserted: true, Reason: "Create real ingestion target"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				job, e := g.s.IngestDataset(g.ctx, g.portal, g.f.Workspace, o.ID, "", DatasetImportRequest{dataset.ID, "synthetic.jsonl", "Create immutable source intent"}, uuid.NewString(), uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				if _, e = g.f.Store.Exec(g.ctx, statement(g.f.Store, "UPDATE artifact_object_ingestions SET input='{}'::jsonb WHERE id=$1", "UPDATE artifact_object_ingestions SET `input`=JSON_OBJECT() WHERE id=?", `UPDATE "ARTIFACT_OBJECT_INGESTIONS" SET "INPUT"='{}' WHERE "ID"=?`, job.ID)); e == nil {
					t.Fatal("ingestion provenance replaced")
				}

			case "staging_owned_cleanup_and_reference":
				sum := sha256.Sum256(source)
				attempt, e := g.s.Stage.Write(g.ctx, uuid.NewString(), int64(len(source)), hex.EncodeToString(sum[:]), bytes.NewReader(source))
				if e != nil {
					t.Fatal(e)
				}
				records, e := g.s.StagingRecords(g.ctx, g.admin, g.f.Workspace, 2)
				if e != nil || len(records) != 1 {
					t.Fatal(e)
				}
				if e = g.s.CleanupStaging(g.ctx, g.admin, g.f.Workspace, records[0], "Refuse active fixture removal", uuid.NewString()); e == nil {
					t.Fatal("active stage reclaimed")
				}
				attempt.File.Close()
				if e = g.s.CleanupStaging(g.ctx, g.admin, g.f.Workspace, records[0], "Remove inactive owned fixture", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				o, _, _ := g.create(t, source)
				g.work(t)
				if _, e = g.s.PutPart(g.ctx, g.portal, g.f.Workspace, o.ID, objectstore.Part{Number: 1, Bytes: int64(len(source)), SHA256: hex.EncodeToString(sum[:])}, bytes.NewReader([]byte("truncated")), uuid.NewString()); e == nil {
					t.Fatal("partial source accepted")
				}
				records, e = g.s.StagingRecords(g.ctx, g.admin, g.f.Workspace, 2)
				if e != nil || len(records) != 1 {
					t.Fatal("retained exact journal missing", e)
				}
				if e = g.s.CleanupStaging(g.ctx, g.admin, g.f.Workspace, records[0], "Refuse live DB stage reference", uuid.NewString()); e == nil || e.Error() != "OBJECT_STAGING_REFERENCED" {
					t.Fatal("live DB source reclaimed", e)
				}
				if _, e = g.s.Reconcile(g.ctx, g.portal, g.f.Workspace, o.ID, "Fence failed staged intent", uuid.NewString()); e != nil {
					t.Fatal(e)
				}
				if e = g.s.CleanupStaging(g.ctx, g.admin, g.f.Workspace, records[0], "Remove fenced partial source", uuid.NewString()); e != nil {
					t.Fatal(e)
				}

			}
		})
	}
}

func objectMutationArgs(q transaction.Executor, object, value string) []any {
	if positionalArguments(q) {
		return []any{value, object}
	}
	return []any{object, value}
}
