//go:build repository_integration && object_storage_integration

package artifactstores

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func (g *objectStory) initializeLicense(t *testing.T) {
	t.Helper()
	private, trust, err := wire.GenerateKey("explicit-synthetic-object-fixture-license")
	if err != nil {
		t.Fatal(err)
	}
	g.licenseNow = time.Now().UTC().Truncate(time.Second)
	g.license, err = licensing.New(g.owner, trust, wire.DefaultPolicy(), func() time.Time { return g.licenseNow })
	if err != nil {
		t.Fatal(err)
	}
	g.owner.BusinessAdmission = g.license
	state, err := g.license.StatusAdmin(g.ctx, g.admin)
	if err != nil {
		t.Fatal(err)
	}
	claims := wire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Explicit synthetic object acceptance",
		DeploymentID: state.DeploymentID, IssuedAt: g.licenseNow.Add(-time.Minute).Format(time.RFC3339),
		NotBefore: g.licenseNow.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: g.licenseNow.Add(8 * time.Hour).Format(time.RFC3339),
		GraceUntil: g.licenseNow.Add(9 * time.Hour).Format(time.RFC3339), Scope: wire.WholePlatformScope}
	artifact, err := wire.Issue(claims, private)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.license.Import(g.ctx, g.admin, licensing.ImportInput{Artifact: string(artifact), Reason: "Install actual signed synthetic fixture license"}, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
}

func (g *objectStory) licenseBoundary(t *testing.T, leaf string, source []byte) {
	t.Helper()
	switch leaf {
	case "license_expiry_denies_new_object_work":
		ready := g.ready(t, source)
		pending, _, _ := g.create(t, source)
		g.work(t)
		sum := sha256.Sum256(source)
		g.licenseNow = g.licenseNow.Add(10 * time.Hour)
		before := g.count(t, "SELECT count(*) FROM artifact_objects", "SELECT count(*) FROM artifact_objects", `SELECT COUNT(*) FROM "ARTIFACT_OBJECTS"`)
		out, err := g.s.Create(g.ctx, g.portal, CreateRequest{g.f.Workspace, "Denied after expiry", "dataset_source", int64(len(source)), hex.EncodeToString(sum[:]), "Verify exact expired admission"}, uuid.NewString(), uuid.NewString())
		objectZero(t, out, err, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
		out, err = g.s.PutPart(g.ctx, g.portal, g.f.Workspace, pending.ID, objectstore.Part{Number: 1, Bytes: int64(len(source)), SHA256: hex.EncodeToString(sum[:])}, bytes.NewReader(source), uuid.NewString())
		objectZero(t, out, err, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
		out, err = g.s.Complete(g.ctx, g.portal, g.f.Workspace, pending.ID, "Refuse new completion after expiry", uuid.NewString())
		objectZero(t, out, err, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
		grant, err := g.s.CreateGrant(g.ctx, g.portal, g.f.Workspace, ready.ID, GrantRequest{Recipient: g.f.User, Action: "read", Seconds: 30, Reason: "Refuse new grant after expiry"}, uuid.NewString(), uuid.NewString())
		if err == nil || err.Error() != "LICENSE_NEW_BUSINESS_UNAVAILABLE" || grant.ID != "" {
			t.Fatal("expired grant admitted", err)
		}
		job, err := g.s.IngestDataset(g.ctx, g.portal, g.f.Workspace, ready.ID, "", DatasetImportRequest{DatasetID: uuid.NewString(), SourceName: "synthetic.jsonl", Reason: "Refuse new ingestion after expiry"}, uuid.NewString(), uuid.NewString())
		ingestionZero(t, job, err, "LICENSE_NEW_BUSINESS_UNAVAILABLE")
		if g.count(t, "SELECT count(*) FROM artifact_objects", "SELECT count(*) FROM artifact_objects", `SELECT COUNT(*) FROM "ARTIFACT_OBJECTS"`) != before || g.count(t, "SELECT count(*) FROM artifact_object_parts WHERE object_id=$1", "SELECT count(*) FROM artifact_object_parts WHERE object_id=?", `SELECT COUNT(*) FROM "ARTIFACT_OBJECT_PARTS" WHERE "OBJECT_ID"=?`, pending.ID) != 0 || g.count(t, "SELECT count(*) FROM artifact_object_grants", "SELECT count(*) FROM artifact_object_grants", `SELECT COUNT(*) FROM "ARTIFACT_OBJECT_GRANTS"`) != 0 || g.count(t, "SELECT count(*) FROM artifact_object_ingestions", "SELECT count(*) FROM artifact_object_ingestions", `SELECT COUNT(*) FROM "ARTIFACT_OBJECT_INGESTIONS"`) != 0 {
			t.Fatal("expired business persisted an intent")
		}
	case "license_expiry_preserves_accepted_completion":
		o, _, _ := g.create(t, source)
		g.work(t)
		g.parts(t, o, source)
		if _, err := g.s.Complete(g.ctx, g.portal, g.f.Workspace, o.ID, "Accept completion before expiry", uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		g.licenseNow = g.licenseNow.Add(10 * time.Hour)
		g.work(t)
		download, err := g.s.Download(g.ctx, g.portal, g.f.Workspace, o.ID, "")
		if err != nil {
			t.Fatal("accepted completion unavailable after ordinary expiry", err)
		}
		raw, err := io.ReadAll(download.File)
		download.File.Close()
		if err != nil || !bytes.Equal(raw, source) {
			t.Fatal("accepted source bytes changed", err)
		}
	case "license_corrupt_history_fences_prepared_worker":
		o, _, _ := g.create(t, source)
		// Explicit corruption fault in this disposable real SQL schema only.
		g.exec(t, "UPDATE platform_license_artifacts SET artifact_sha256=repeat('0',64)", "UPDATE platform_license_artifacts SET artifact_sha256=REPEAT('0',64)", `UPDATE "PLATFORM_LICENSE_ARTIFACTS" SET "ARTIFACT_SHA256"='0000000000000000000000000000000000000000000000000000000000000000'`)
		worked, err := g.s.ProcessNext(g.ctx)
		if err == nil || err.Error() != "LICENSE_STORED_STATE_INVALID" || worked {
			t.Fatal("corrupt history admitted an accepted worker", err)
		}
		remote, err := g.s.Transport.FindUploads(g.ctx, objectstore.Ref{Tenant: g.f.Tenant, Workspace: g.f.Workspace, Object: o.ID})
		if err != nil || len(remote) != 0 || g.count(t, "SELECT count(*) FROM artifact_objects WHERE id=$1 AND operation_phase='prepared' AND error_code='LICENSE_STORED_STATE_INVALID' AND remote_upload_id IS NULL", "SELECT count(*) FROM artifact_objects WHERE id=? AND operation_phase='prepared' AND error_code='LICENSE_STORED_STATE_INVALID' AND remote_upload_id IS NULL", `SELECT COUNT(*) FROM "ARTIFACT_OBJECTS" WHERE "ID"=? AND "OPERATION_PHASE"='prepared' AND "ERROR_CODE"='LICENSE_STORED_STATE_INVALID' AND "REMOTE_UPLOAD_ID" IS NULL`, o.ID) != 1 {
			t.Fatal("corrupt-history refusal lost its exact unsent state", err)
		}
	case "license_observation_uses_single_connection":
		if g.f.Store.Dialect() != transaction.PostgreSQL {
			t.Fatal("explicit actual PostgreSQL pool acceptance required")
		}
		configuration := g.f.PostgreSQL.Config().Copy()
		configuration.MaxConns = 1
		pool, err := pgxpool.NewWithConfig(g.ctx, configuration)
		if err != nil {
			t.Fatal(err)
		}
		defer pool.Close()
		store, err := transaction.FromPostgreSQL(pool)
		if err != nil {
			t.Fatal(err)
		}
		original := g.owner.Storage
		g.owner.Storage = store
		defer func() { g.owner.Storage = original }()
		o, _, _ := g.create(t, source)
		g.work(t)
		current, err := g.s.Get(g.ctx, g.portal, g.f.Workspace, o.ID, "")
		if err != nil || current.State != "uploading" || pool.Stat().MaxConns() != 1 || pool.Stat().AcquiredConns() != 0 {
			t.Fatal("license observation nested a pool acquisition", err)
		}
	default:
		t.Fatal("unrecognized frozen object license acceptance")
	}
}
