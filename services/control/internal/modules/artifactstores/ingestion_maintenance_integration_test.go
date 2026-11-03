//go:build repository_integration && object_storage_integration

package artifactstores

import (
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

func (g *objectStory) workerClaimUnknown(t *testing.T, source []byte) {
	t.Helper()
	o, _, _ := g.create(t, source)
	g.owner.Storage = &objectOutcomeStore{Store: g.f.Store, unknown: true}
	worked, e := g.s.ProcessNext(g.ctx)
	g.owner.Storage = g.f.Store
	if e == nil || worked {
		t.Fatal("unknown real claim projected worker success", e, worked)
	}
	if g.count(t, "SELECT count(*) FROM artifact_objects WHERE id=$1 AND operation_phase='sent' AND remote_upload_id IS NULL", "SELECT count(*) FROM artifact_objects WHERE id=? AND operation_phase='sent' AND remote_upload_id IS NULL", `SELECT COUNT(*) FROM "ARTIFACT_OBJECTS" WHERE "ID"=? AND "OPERATION_PHASE"='sent' AND "REMOTE_UPLOAD_ID" IS NULL`, o.ID) != 1 {
		t.Fatal("actual unknown claim readback missing")
	}
	remote, e := g.s.Transport.FindUploads(g.ctx, objectstore.Ref{Tenant: g.f.Tenant, Workspace: g.f.Workspace, Object: o.ID})
	if e != nil || len(remote) != 0 {
		t.Fatal("remote upload followed an unknown claim", e)
	}
}

func ingestionZero(t *testing.T, value Ingestion, e error, code string) {
	t.Helper()
	if e == nil || code != "" && e.Error() != code || !reflect.DeepEqual(value, Ingestion{}) {
		t.Fatal("failed/unknown ingestion projected a result", e)
	}
}

func (g *objectStory) ingestionMaintenance(t *testing.T, leaf string, source []byte) {
	t.Helper()
	o := g.ready(t, source)
	dataset, e := g.s.Datasets.Create(g.ctx, g.portal, datasets.CreateRequest{WorkspaceID: g.f.Workspace, Name: "Synthetic cancellation target", Origin: "Synthetic source", License: "CC0 synthetic", RightsAsserted: true, Reason: "Create actual owner for unclaimed intent"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	job, e := g.s.IngestDataset(g.ctx, g.portal, g.f.Workspace, o.ID, "", DatasetImportRequest{DatasetID: dataset.ID, SourceName: "synthetic.jsonl", Reason: "Create real unclaimed ingestion"}, uuid.NewString(), uuid.NewString())
	if e != nil || job.State != "intent" || job.TargetID != nil {
		t.Fatal("actual unclaimed intent missing", e)
	}
	blocked, e := g.s.Delete(g.ctx, g.portal, g.f.Workspace, o.ID, "Refuse active source removal", uuid.NewString())
	objectZero(t, blocked, e, "OBJECT_REFERENCED")
	baseline := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events", `SELECT COUNT(*) FROM "AUDIT_EVENTS"`)
	switch leaf {
	case "prepared_ingestion_cancel_audit_rollback":
		g.exec(t, "ALTER TABLE audit_events ADD CONSTRAINT object_cancel_fixture_audit_failure CHECK(action<>'object.ingestion_cancelled')", "ALTER TABLE audit_events ADD CONSTRAINT object_cancel_fixture_audit_failure CHECK(action<>'object.ingestion_cancelled')", `ALTER TABLE "AUDIT_EVENTS" ADD CONSTRAINT "OBJECT_CANCEL_FIXTURE_AUDIT_FAILURE" CHECK("ACTION"<>'object.ingestion_cancelled')`)
		v, e := g.s.CancelIngestion(g.ctx, g.portal, g.f.Workspace, o.ID, job.ID, "Force actual cancellation audit rollback", uuid.NewString())
		ingestionZero(t, v, e, "")
		retained, e := g.s.GetIngestion(g.ctx, g.portal, g.f.Workspace, o.ID, job.ID)
		if e != nil || retained.State != "intent" || g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events", `SELECT COUNT(*) FROM "AUDIT_EVENTS"`) != baseline {
			t.Fatal("cancellation escaped atomic audit rollback", e)
		}
		return
	case "claimed_ingestion_cancel_refused":
		// Retained exact sent-boundary envelope; not physical process qualification.
		g.exec(t, "UPDATE artifact_objects SET operation_phase='sent' WHERE id=$1", "UPDATE artifact_objects SET operation_phase='sent' WHERE id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_PHASE"='sent' WHERE "ID"=?`, o.ID)
		v, e := g.s.CancelIngestion(g.ctx, g.portal, g.f.Workspace, o.ID, job.ID, "Refuse unconfirmed physical stop", uuid.NewString())
		ingestionZero(t, v, e, "OBJECT_INGESTION_STOP_UNCONFIRMED")
		return
	case "prepared_ingestion_current_authority":
		other := g.recipient(t, "member")
		v, e := g.s.CancelIngestion(g.ctx, other, g.f.Workspace, o.ID, job.ID, "Refuse foreign private intent", uuid.NewString())
		ingestionZero(t, v, e, "RESOURCE_NOT_FOUND")
		workspace := uuid.NewString()
		g.exec(t, "INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Other cancellation scope','active')", "INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Other cancellation scope','active')", `INSERT INTO "WORKSPACES"("ID","TENANT_ID","NAME","STATUS") VALUES(?,?,'Other cancellation scope','active')`, workspace, g.f.Tenant)
		v, e = g.s.CancelIngestion(g.ctx, g.admin, workspace, o.ID, job.ID, "Refuse crossed Workspace cancellation", uuid.NewString())
		ingestionZero(t, v, e, "RESOURCE_NOT_FOUND")
		g.exec(t, "UPDATE workspace_memberships SET role='viewer' WHERE user_id=$1", "UPDATE workspace_memberships SET role='viewer' WHERE user_id=?", `UPDATE "WORKSPACE_MEMBERSHIPS" SET "ROLE"='viewer' WHERE "USER_ID"=?`, g.f.User)
		v, e = g.s.CancelIngestion(g.ctx, g.portal, g.f.Workspace, o.ID, job.ID, "Refuse current viewer cancellation", uuid.NewString())
		ingestionZero(t, v, e, "ACCESS_DENIED")
		cancelled, e := g.s.CancelIngestion(g.ctx, g.admin, g.f.Workspace, o.ID, job.ID, "Current platform administrator cancels unclaimed intent", uuid.NewString())
		if e != nil || cancelled.State != "failed" {
			t.Fatal("current administrator cancellation failed", e)
		}
		return
	}
	if leaf == "prepared_ingestion_cancel_unknown_zero" {
		g.owner.Storage = &objectOutcomeStore{Store: g.f.Store, unknown: true}
		v, e := g.s.CancelIngestion(g.ctx, g.portal, g.f.Workspace, o.ID, job.ID, "Retain actual unknown cancellation commit", uuid.NewString())
		g.owner.Storage = g.f.Store
		ingestionZero(t, v, e, "")
	}
	value, e := g.s.CancelIngestion(g.ctx, g.portal, g.f.Workspace, o.ID, job.ID, "Cancel exact unclaimed intent", uuid.NewString())
	if e != nil || value.ID != job.ID || value.State != "failed" || value.TargetID != nil || value.ErrorCode == nil || *value.ErrorCode != ingestionCancelled {
		t.Fatal("unclaimed cancellation/replay failed", e)
	}
	again, e := g.s.CancelIngestion(g.ctx, g.portal, g.f.Workspace, o.ID, job.ID, "Read back exact cancelled intent", uuid.NewString())
	if e != nil || !reflect.DeepEqual(value, again) || g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events", `SELECT COUNT(*) FROM "AUDIT_EVENTS"`) != baseline+1 {
		t.Fatal("cancellation replay changed immutable history", e)
	}
	if worked, e := g.s.ProcessNext(g.ctx); e != nil || worked || g.count(t, "SELECT count(*) FROM dataset_drafts", "SELECT count(*) FROM dataset_drafts", `SELECT COUNT(*) FROM "DATASET_DRAFTS"`) != 0 {
		t.Fatal("cancelled intent launched a target", e)
	}
	if _, e = g.s.Delete(g.ctx, g.portal, g.f.Workspace, o.ID, "Remove unreferenced cancelled source", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	g.work(t)
}

func (g *objectStory) removeSubmittedSource(t *testing.T, actor identity.Auth, o Object, receipt Ingestion) {
	t.Helper()
	if g.count(t, "SELECT count(*) FROM artifact_object_ingestions WHERE id=$1 AND state='submitted' AND source_verified_sha256=$2 AND receipt IS NOT NULL", "SELECT count(*) FROM artifact_object_ingestions WHERE id=? AND state='submitted' AND source_verified_sha256=? AND receipt IS NOT NULL", `SELECT COUNT(*) FROM "ARTIFACT_OBJECT_INGESTIONS" WHERE "ID"=? AND "STATE"='submitted' AND "SOURCE_VERIFIED_SHA256"=? AND "RECEIPT" IS NOT NULL`, receipt.ID, o.SHA256) != 1 {
		t.Fatal("whole verified independent source receipt missing")
	}
	if _, e := g.s.Delete(g.ctx, actor, g.f.Workspace, o.ID, "Remove exact source after durable local handoff", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	g.work(t)
	_, e := g.s.Transport.Head(g.ctx, objectstore.Ref{Tenant: g.f.Tenant, Workspace: g.f.Workspace, Object: o.ID})
	if e == nil || e.Error() != "OBJECT_NOT_FOUND" {
		t.Fatal("remote removal not positively confirmed", e)
	}
	after, e := g.s.GetIngestion(g.ctx, actor, g.f.Workspace, o.ID, receipt.ID)
	if e != nil || !reflect.DeepEqual(receipt, after) {
		t.Fatal("remote cleanup changed immutable ingestion receipt", e)
	}
}
