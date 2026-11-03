//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
)

// This fixture explicitly substitutes a previously accepted/stopped record to isolate
// Admin rejection/graph/authority transactions. It does not accept files or execute a GPU.
// Actual full acceptance/file publication has its separate native large-file profile.
func reviewMetadataFixture(t *testing.T) (preparationScenario, modelpreparations.Preparation, modelpreparations.ReviewRequest) {
	t.Helper()
	g, p, l, raw := preparationOutputFixtureWithAccuracy(t, 3000)
	ctx := context.Background()
	u, e := g.service.BeginUpload(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, raw)
	if e != nil {
		t.Fatal(e)
	}
	var descriptor []byte
	if e = g.f.s.DB.QueryRow(ctx, "SELECT descriptor FROM model_preparation_uploads WHERE id=$1", u.ID).Scan(&descriptor); e != nil {
		t.Fatal(e)
	}
	receipt := preparationStopReceipt(t, g, l)
	receipt.AgentErrorCode = ""
	receipt.Proof = "owned_container_removed"
	cid := strings.Repeat("a", 64)
	receipt.ContainerID = &cid
	sha := digest(raw)
	receipt.OutcomeSHA256 = &sha
	proof, _ := json.Marshal(receipt)
	tx, e := g.f.s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "UPDATE model_preparation_uploads SET state='accepted',accepted_at=clock_timestamp() WHERE id=$1", u.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO model_preparation_artifacts(id,tenant_id,workspace_id,preparation_id,attempt_id,upload_id,manifest_sha256,descriptor,report,report_bytes,storage_path) VALUES($1,$2,$3,$4,$5,$1,$6,$7,$8,$9,$10)`, u.ID, l.Preparation.Scope.TenantID, p.WorkspaceID, p.ID, l.Preparation.AttemptID, sha, descriptor, raw, raw, "explicit-test-double-missing-files"); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, `UPDATE model_preparation_attempts SET state='prepared',outcome=$2,outcome_bytes=$3,outcome_sha256=$4,physical_stop_receipt=$5,physical_stop_sha256=$6,physical_stopped_at=clock_timestamp(),finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1`, l.Preparation.AttemptID, raw, raw, sha, proof, digest(proof)); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "UPDATE gpu_leases SET state='released',updated_at=clock_timestamp() WHERE model_preparation_id=$1", p.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "UPDATE node_tasks SET state='succeeded',lease_token_hash=NULL,lease_expires_at=NULL,completed_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", l.TaskID); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, "UPDATE model_preparations SET state='awaiting_review',revision=revision+1,finished_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1", p.ID); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	p, e = g.service.Get(ctx, g.f.admin, p.WorkspaceID, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	return g, p, modelpreparations.ReviewRequest{ExpectedRevision: p.Revision, ArtifactID: u.ID, ReportSHA256: sha, Decision: "reject", Name: "", Reason: "Explicit Admin rejection with synthetic accepted record and missing files"}
}

func TestActualCpuModelPreparationAdminRejectCurrentScopeExactReplayAndNoPublication(t *testing.T) {
	g, p, input := reviewMetadataFixture(t)
	ctx := context.Background()
	key := uuid.NewString()
	if _, e := g.service.Review(ctx, g.f.portal, p.WorkspaceID, p.ID, input, key, uuid.NewString()); e == nil {
		t.Fatal("Workspace owner reviewed")
	}
	changed := input
	changed.ReportSHA256 = strings.Repeat("b", 64)
	if _, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, changed, key, uuid.NewString()); e == nil {
		t.Fatal("foreign report reviewed")
	}
	if _, e := g.service.Review(ctx, g.f.admin, uuid.NewString(), p.ID, input, key, uuid.NewString()); e == nil {
		t.Fatal("foreign Workspace reviewed")
	}
	if _, e := g.f.s.DB.Exec(ctx, "UPDATE managed_nodes SET status='disabled',version=version+1 WHERE id=$1", g.node.NodeID); e != nil {
		t.Fatal(e)
	}
	r, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, input, key, uuid.NewString())
	if e != nil || r.Decision != "reject" || r.ModelID != nil || r.ArtifactID != input.ArtifactID || r.ReportSHA256 != input.ReportSHA256 {
		t.Fatal("scoped stopped artifact rejection", e)
	}
	replay, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, input, key, uuid.NewString())
	if e != nil || replay.ID != r.ID {
		t.Fatal("exact decision replay", e)
	}
	changed = input
	changed.Reason = "Different intent"
	if _, e = g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, changed, key, uuid.NewString()); e == nil {
		t.Fatal("changed decision replay")
	}
	if count(t, g.f, "SELECT count(*) FROM model_derivations") != 0 || count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='rejected' AND review_id IS NOT NULL AND derived_model_id IS NULL", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_review_rejected'") != 1 {
		t.Fatal("rejection published or duplicate decision/audit")
	}
}

func TestActualCpuModelPreparationReviewAuditRollbackAndApprovalRequiresActualFiles(t *testing.T) {
	g, p, input := reviewMetadataFixture(t)
	ctx := context.Background()
	key := uuid.NewString()
	if _, e := g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_review() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_review_rejected' THEN RAISE EXCEPTION 'SYNTHETIC_REVIEW_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_review BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_review()`); e != nil {
		t.Fatal(e)
	}
	if _, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, input, key, uuid.NewString()); e == nil {
		t.Fatal("review audit failure committed")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_reviews") != 0 || count(t, g.f, "SELECT count(*) FROM model_preparations WHERE id=$1 AND state='awaiting_review' AND review_id IS NULL", p.ID) != 1 {
		t.Fatal("review audit rollback partial state")
	}
	approve := input
	approve.Decision = "approve"
	approve.Name = "Missing actual files must refuse"
	if _, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, approve, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("metadata substitute approved without actual files")
	}
	if count(t, g.f, "SELECT count(*) FROM model_derivations") != 0 {
		t.Fatal("failed approval registered model")
	}
	if _, e := g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_review ON audit_events"); e != nil {
		t.Fatal(e)
	}
	if _, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, input, key, uuid.NewString()); e != nil {
		t.Fatal("rejection repair retry", e)
	}
}

func TestActualCpuModelPreparationConcurrentIdenticalRejectionCommitsOneDecision(t *testing.T) {
	g, p, input := reviewMetadataFixture(t)
	ctx := context.Background()
	key := uuid.NewString()
	type result struct {
		review modelpreparations.ModelReview
		err    error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			r, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, input, key, uuid.NewString())
			results <- result{r, e}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.review.ID != second.review.ID {
		t.Fatal("concurrent same intent", first.err, second.err)
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_reviews") != 1 || count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_review_rejected'") != 1 {
		t.Fatal("duplicate concurrent decision/audit")
	}
}

func TestActualCpuModelPreparationReviewRechecksCurrentAdminEpoch(t *testing.T) {
	g, p, input := reviewMetadataFixture(t)
	ctx := context.Background()
	if _, e := g.f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", g.f.admin.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := g.service.Review(ctx, g.f.admin, p.WorkspaceID, p.ID, input, uuid.NewString(), uuid.NewString()); e == nil {
		t.Fatal("old Admin session reviewed")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_reviews") != 0 {
		t.Fatal("revoked authority created decision")
	}
}
