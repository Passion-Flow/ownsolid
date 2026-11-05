package distribution_test

import (
	"context"
	"github.com/google/uuid"
	"net/url"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"strings"
	"testing"
	"time"
)

// All physical/GPU/container/generation reports in this file are explicit test doubles.
// Database/current authority/ownership/immutable history/admission/outcome behavior is real.
func (c scenario) replaceBaseDeployment(t *testing.T, previous string) deployments.Deployment {
	t.Helper()
	ctx := context.Background()
	d, e := c.s.Deployments.Get(ctx, c.a, c.a.Me.Workspace.ID, previous)
	if e != nil {
		t.Fatal(e)
	}
	complete := func(next deployments.Deployment, state string) {
		t.Helper()
		claim, e := c.s.Nodes.Claim(ctx, c.node.NodeID, c.node.Credential, uuid.NewString())
		if e != nil || claim.Lease == nil {
			t.Fatal(e)
		}
		command := claim.Lease.Inference
		o := agentwire.DeploymentObservation{DeploymentID: next.ID, ModelID: command.ModelID, CatalogDigest: command.CatalogDigest, GPUUUID: command.GPUUUID, Revision: command.Revision, State: state}
		if state != "not_installed" {
			o.Managed = true
			o.ContainerID = strings.Repeat("a", 64)
			o.ImageID = agentwire.ApprovedInferenceRecipe().ImageConfigDigest
		}
		if state == "running" {
			o.Ready = true
			o.Verification = &agentwire.GenerationVerification{Model: agentwire.InferenceModelName, PromptSHA256: agentwire.InferencePromptDigest(), ResponseSHA256: strings.Repeat("b", 64), InputTokens: 7, OutputTokens: 1, ObservedAt: time.Now().UTC()}
		}
		if _, e = c.s.Nodes.Complete(ctx, c.node.NodeID, c.node.Credential, claim.Lease.TaskID, agentwire.Result{LeaseToken: claim.Lease.LeaseToken, Outcome: "success", Inference: &agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{}, Deployment: &o, ObservedAt: time.Now().UTC()}}, uuid.NewString()); e != nil {
			t.Fatal(e)
		}
	}
	for _, action := range []string{"stop", "remove"} {
		d, e = c.s.Deployments.Action(ctx, c.a, d.ID, deployments.ActionRequest{Action: action, Revision: d.Revision, Reason: "Synthetic explicit old physical owner cleanup"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		state := "stopped"
		if action == "remove" {
			state = "not_installed"
		}
		complete(d, state)
	}
	if _, e = c.s.Deployments.Models.Retire(ctx, c.a, d.ModelID, "Historical alias rollback source must remain available after physical owner removal", uuid.NewString()); e == nil || e.Error() != "MODEL_VERSION_REFERENCED" {
		t.Fatal("recorded rollback model retired", e)
	}
	h := c.handshake
	h.Inference = &agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{{UUID: d.GPUUUID, Name: "Explicit synthetic GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: time.Now().UTC()}}, ObservedAt: time.Now().UTC()}
	if _, e = c.s.Nodes.Heartbeat(ctx, c.node.NodeID, c.node.Credential, h); e != nil {
		t.Fatal(e)
	}
	next, e := c.s.Deployments.Create(ctx, c.a, deployments.CreateRequest{NodeID: c.node.NodeID, ModelID: d.ModelID, GPUUUID: d.GPUUUID, Reason: "Synthetic fresh verified replacement"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	complete(next, "running")
	next, e = c.s.Deployments.Get(ctx, c.a, c.a.Me.Workspace.ID, next.ID)
	if e != nil {
		t.Fatal(e)
	}
	return next
}
func TestActualAliasCutoverPauseDrainSameKeyRetargetRecordedRollbackAndImmutableHistory(t *testing.T) {
	c := setup(t)
	a := c.access(t)
	ctx := context.Background()
	workspace := a.alias.WorkspaceID
	id := a.alias.ID
	input := a.input()
	admitted, e := c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	pause := distribution.CutoverRequest{Action: "pause", ExpectedRevision: a.alias.Revision, Reason: "Pause new admissions while retaining actual admitted request"}
	key := uuid.NewString()
	paused, e := c.s.Cutover(ctx, c.a, workspace, id, pause, key, uuid.NewString())
	if e != nil || paused.Alias.DisabledAt == nil || paused.Alias.HeldRequests != 1 || paused.AppliedRevision != 2 {
		t.Fatal("pause did not preserve owner", e)
	}
	replay, e := c.s.Cutover(ctx, c.a, workspace, id, pause, key, uuid.NewString())
	if e != nil || !replay.Replayed || replay.AppliedRevision != 2 {
		t.Fatal("pause replay", e)
	}
	_, e = c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, a.input(), uuid.NewString())
	check(t, e, "MODEL_ALIAS_UNAVAILABLE")
	resume := distribution.CutoverRequest{Action: "resume", ExpectedRevision: 2, Reason: "Do not clear an unknown physical request by waiting"}
	_, e = c.s.Cutover(ctx, c.a, workspace, id, resume, uuid.NewString(), uuid.NewString())
	check(t, e, "ALIAS_REQUESTS_UNDRAINED")
	if _, e = c.s.DB.Exec(ctx, "UPDATE gateway_requests SET deadline_at=admitted_at+interval '1 millisecond' WHERE id=$1", input.RequestID); e != nil {
		t.Fatal(e)
	}
	_, e = c.s.Cutover(ctx, c.a, workspace, id, resume, uuid.NewString(), uuid.NewString())
	check(t, e, "ALIAS_REQUESTS_UNDRAINED")
	if _, e = c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	next := c.replaceBaseDeployment(t, a.alias.DeploymentID)
	retarget := distribution.CutoverRequest{Action: "retarget", ExpectedRevision: 2, DeploymentID: &next.ID, DeploymentRevision: &next.Revision, Reason: "Same key explicit verified fresh target"}
	changed, e := c.s.Cutover(ctx, c.a, workspace, id, retarget, uuid.NewString(), uuid.NewString())
	if e != nil || changed.Alias.ID != id || changed.Alias.DisabledAt == nil || changed.Alias.DeploymentID != next.ID || changed.Alias.HeldRequests != 0 {
		t.Fatal("retarget", e)
	}
	_, e = c.s.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, *a.key.Secret)
	check(t, e, "MODEL_ALIAS_UNAVAILABLE")
	resume.ExpectedRevision = 3
	resumed, e := c.s.Cutover(ctx, c.a, workspace, id, resume, uuid.NewString(), uuid.NewString())
	if e != nil || !resumed.Alias.Available {
		t.Fatal("resume", e)
	}
	models, e := c.s.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, *a.key.Secret)
	if e != nil || models.Models[0].DeploymentID != next.ID {
		t.Fatal("same original key changed binding", e)
	}
	var retained string
	if e = c.s.DB.QueryRow(ctx, "SELECT deployment_id::text FROM gateway_requests WHERE id=$1", admitted.RequestID).Scan(&retained); e != nil || retained != a.alias.DeploymentID {
		t.Fatal("admitted snapshot mutated", e)
	}
	pause.ExpectedRevision = 4
	paused, e = c.s.Cutover(ctx, c.a, workspace, id, pause, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	restored := c.replaceBaseDeployment(t, next.ID)
	source := int64(1)
	rollback := distribution.CutoverRequest{Action: "rollback", ExpectedRevision: paused.Alias.Revision, DeploymentID: &restored.ID, DeploymentRevision: &restored.Revision, SourceRevision: &source, Reason: "Selected recorded model redeployed and verified before rollback"}
	rolled, e := c.s.Cutover(ctx, c.a, workspace, id, rollback, uuid.NewString(), uuid.NewString())
	if e != nil || rolled.Alias.DisabledAt == nil {
		t.Fatal("recorded model rollback", e)
	}
	resume.ExpectedRevision = rolled.Alias.Revision
	if _, e = c.s.Cutover(ctx, c.a, workspace, id, resume, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	history, e := c.s.TargetHistory(ctx, c.a, workspace, id, url.Values{"limit": {"2"}})
	if e != nil || len(history.Items) != 2 || history.NextCursor == nil {
		t.Fatal("history page", e)
	}
	page, e := c.s.TargetHistory(ctx, c.a, workspace, id, url.Values{"limit": {"2"}, "cursor": {*history.NextCursor}})
	if e != nil || len(page.Items) != 2 || page.Items[0].ID == history.Items[0].ID {
		t.Fatal("history cursor", e)
	}
	if _, e = c.s.DB.Exec(ctx, "UPDATE model_alias_target_versions SET deployment_revision=deployment_revision+1 WHERE alias_id=$1", id); e == nil {
		t.Fatal("target history mutable")
	}
	if _, e = c.s.DB.Exec(ctx, "DELETE FROM model_alias_cutover_intents WHERE alias_id=$1", id); e == nil {
		t.Fatal("intent deleted")
	}
	replay, e = c.s.Cutover(ctx, c.a, workspace, id, distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "Pause new admissions while retaining actual admitted request"}, key, uuid.NewString())
	if e != nil || replay.AppliedRevision != 2 || !replay.Replayed || replay.Alias.Revision <= 2 {
		t.Fatal("exact historical replay duplicated mutation", e)
	}
	t.Log("Actual PostgreSQL current authority, pause/unknown capacity/normal outcomes, explicit stop-remove-new deployment, same key retarget, recorded-model rollback, immutable paged snapshots and original admitted revision preserved. All physical reports remain explicit test doubles.")
}
func TestActualAliasCutoverRejectsMixedStaleForeignAndRollsBackAudit(t *testing.T) {
	c := setup(t)
	a := c.access(t)
	ctx := context.Background()
	id := a.alias.ID
	workspace := a.alias.WorkspaceID
	in := distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "Synthetic audit and authority boundary"}
	key := uuid.NewString()
	if _, e := c.s.Cutover(ctx, c.p, workspace, id, in, key, uuid.NewString()); e == nil {
		t.Fatal("portal Admin action")
	}
	if _, e := c.s.Cutover(ctx, c.a, workspace, uuid.NewString(), in, key, uuid.NewString()); e == nil {
		t.Fatal("foreign alias")
	}
	if _, e := c.s.DB.Exec(ctx, `CREATE FUNCTION synthetic_cutover_audit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='model.alias_paused' THEN RAISE EXCEPTION 'synthetic audit failure'; END IF;RETURN NEW;END $$;CREATE TRIGGER synthetic_cutover_audit_failure BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION synthetic_cutover_audit_failure();`); e != nil {
		t.Fatal(e)
	}
	if _, e := c.s.Cutover(ctx, c.a, workspace, id, in, key, uuid.NewString()); e == nil {
		t.Fatal("failed audit returned cutover")
	}
	var count int
	if e := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM model_alias_target_versions WHERE alias_id=$1", id).Scan(&count); e != nil || count != 1 {
		t.Fatal("failed audit left history", e)
	}
	if _, e := c.s.DB.Exec(ctx, "DROP TRIGGER synthetic_cutover_audit_failure ON audit_events"); e != nil {
		t.Fatal(e)
	}
	paused, e := c.s.Cutover(ctx, c.a, workspace, id, in, key, uuid.NewString())
	if e != nil {
		t.Fatal("same key post rollback retry", e)
	}
	changed := in
	changed.Reason = "Changed exact body"
	_, e = c.s.Cutover(ctx, c.a, workspace, id, changed, key, uuid.NewString())
	check(t, e, "IDEMPOTENCY_CONFLICT")
	_, e = c.s.Cutover(ctx, c.a, workspace, id, in, uuid.NewString(), uuid.NewString())
	check(t, e, "VERSION_CONFLICT")
	source := int64(999)
	target := a.alias.DeploymentID
	revision := a.alias.DeploymentRevision
	rollback := distribution.CutoverRequest{Action: "rollback", ExpectedRevision: paused.Alias.Revision, DeploymentID: &target, DeploymentRevision: &revision, SourceRevision: &source, Reason: "Unknown recorded source"}
	_, e = c.s.Cutover(ctx, c.a, workspace, id, rollback, uuid.NewString(), uuid.NewString())
	check(t, e, "VALIDATION_FAILED")
	if _, e = c.s.TargetHistory(ctx, a.owner, workspace, id, url.Values{}); e == nil {
		t.Fatal("portal private target history")
	}
	if _, e = c.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", c.a.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = c.s.Cutover(ctx, c.a, workspace, id, distribution.CutoverRequest{Action: "resume", ExpectedRevision: 2, Reason: "Stale administrator"}, uuid.NewString(), uuid.NewString())
	if e == nil {
		t.Fatal("stale Admin epoch")
	}
}

func TestActualAliasPauseAndAdmissionSerializeInBothOrders(t *testing.T) {
	for _, pauseFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "pause_before_admission", false: "admission_before_pause"}[pauseFirst], func(t *testing.T) {
			c := setup(t)
			a := c.access(t)
			ctx := context.Background()
			barrier, e := c.s.DB.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer barrier.Rollback(ctx)
			if _, e = barrier.Exec(ctx, "SELECT id FROM workspaces WHERE id=$1 FOR UPDATE", a.alias.WorkspaceID); e != nil {
				t.Fatal(e)
			}
			pauseDone := make(chan error, 1)
			admitDone := make(chan error, 1)
			input := a.input()
			pause := func() {
				go func() {
					_, e := c.s.Cutover(ctx, c.a, a.alias.WorkspaceID, a.alias.ID, distribution.CutoverRequest{Action: "pause", ExpectedRevision: 1, Reason: "Controlled concurrent pause"}, uuid.NewString(), uuid.NewString())
					pauseDone <- e
				}()
			}
			admit := func() {
				go func() {
					_, e := c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString())
					admitDone <- e
				}()
			}
			if pauseFirst {
				pause()
			} else {
				admit()
			}
			c.waitForDatabaseLock(t, "%FOR NO KEY UPDATE OF w%")
			if pauseFirst {
				admit()
			} else {
				pause()
			}
			deadline := time.Now().Add(2 * time.Second)
			queued := false
			for time.Now().Before(deadline) {
				var count int
				if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FOR NO KEY UPDATE OF w%' AND pid<>pg_backend_pid()").Scan(&count); e != nil {
					t.Fatal(e)
				}
				if count >= 2 {
					queued = true
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !queued {
				t.Fatal("both exact Workspace serialization boundaries not observed")
			}
			if e = barrier.Commit(ctx); e != nil {
				t.Fatal(e)
			}
			if e = <-pauseDone; e != nil {
				t.Fatal(e)
			}
			admission := <-admitDone
			if pauseFirst {
				check(t, admission, "MODEL_ALIAS_UNAVAILABLE")
			} else {
				if admission != nil {
					t.Fatal("earlier admission lost", admission)
				}
				if _, e = c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
