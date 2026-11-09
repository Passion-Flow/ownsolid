package distribution_test

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
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
	previewPath := "/api/v1/admin/distribution/workspaces/" + workspace + "/aliases/" + id + "/cutovers/preview"
	server := httpfixture.NewTLSServer(t, identityhttp.New(c.s.Identity))
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/distribution/openapi.json")
	admin := httpfixture.New(t, server, "admin", schemas)
	admin.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": c.password}, http.StatusOK, "")
	publicState := func(state distribution.CutoverState) map[string]any {
		return map[string]any{
			"revision": float64(state.Revision), "deployment_id": state.DeploymentID,
			"deployment_revision": float64(state.DeploymentRevision), "paused": state.Paused,
		}
	}
	type durableCutover struct {
		state                     distribution.CutoverState
		intents, versions, audits int
	}
	read := func() durableCutover {
		t.Helper()
		var out durableCutover
		err := c.s.DB.QueryRow(ctx, `SELECT revision,deployment_id::text,deployment_revision,disabled_at IS NOT NULL,
			(SELECT count(*) FROM model_alias_cutover_intents WHERE alias_id=$1),
			(SELECT count(*) FROM model_alias_target_versions WHERE alias_id=$1),
			(SELECT count(*) FROM audit_events WHERE target_id=$1)
			FROM model_aliases WHERE id=$1`, id).Scan(
			&out.state.Revision, &out.state.DeploymentID, &out.state.DeploymentRevision, &out.state.Paused,
			&out.intents, &out.versions, &out.audits,
		)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	applyPreview := func(in distribution.CutoverRequest, key string) (distribution.CutoverResult, error) {
		t.Helper()
		before := read()
		preview := admin.Write(http.MethodPost, previewPath, in, http.StatusOK, "AliasCutoverPreviewResponse")
		after, ok := preview["after"].(map[string]any)
		if before != read() || len(preview) != 2 || !reflect.DeepEqual(preview["before"], publicState(before.state)) ||
			!ok || after["revision"] != float64(before.state.Revision+1) {
			t.Fatal("preview changed durable state or projected the wrong revision", in.Action)
		}
		applied, err := c.s.Cutover(ctx, c.a, workspace, id, in, key, uuid.NewString())
		if err == nil && !reflect.DeepEqual(after, publicState(read().state)) {
			t.Fatal("preview and committed cutover disagree", in.Action)
		}
		return applied, err
	}
	input := a.input()
	admitted, e := c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	pause := distribution.CutoverRequest{Action: "pause", ExpectedRevision: a.alias.Revision, Reason: "Pause new admissions while retaining actual admitted request"}
	key := uuid.NewString()
	_, e = c.s.PreviewCutover(ctx, a.owner, workspace, id, pause)
	check(t, e, "ACCESS_DENIED")
	foreign := setup(t)
	if foreign.a.Me.Workspace.TenantID == c.a.Me.Workspace.TenantID {
		t.Fatal("expected a separate tenant")
	}
	foreignServer := httpfixture.NewTLSServer(t, identityhttp.New(foreign.s.Identity))
	foreignAdmin := httpfixture.New(t, foreignServer, "admin", schemas)
	foreignAdmin.Write(http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": foreign.password}, http.StatusOK, "")
	if problem := foreignAdmin.Write(http.MethodPost, previewPath, pause, http.StatusNotFound, "Problem"); problem["code"] != "RESOURCE_NOT_FOUND" {
		t.Fatal("foreign tenant did not receive the scoped problem")
	}
	paused, e := applyPreview(pause, key)
	if e != nil || paused.Alias.DisabledAt == nil || paused.Alias.HeldRequests != 1 || paused.AppliedRevision != 2 {
		t.Fatal("pause did not preserve owner", e)
	}
	if problem := admin.Write(http.MethodPost, previewPath, pause, http.StatusConflict, "Problem"); problem["code"] != "VERSION_CONFLICT" {
		t.Fatal("stale preview did not return a revision conflict")
	}
	replay, e := c.s.Cutover(ctx, c.a, workspace, id, pause, key, uuid.NewString())
	if e != nil || !replay.Replayed || replay.AppliedRevision != 2 {
		t.Fatal("pause replay", e)
	}
	_, e = c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, a.input(), uuid.NewString())
	check(t, e, "MODEL_ALIAS_UNAVAILABLE")
	resume := distribution.CutoverRequest{Action: "resume", ExpectedRevision: 2, Reason: "Do not clear an unknown physical request by waiting"}
	if problem := admin.Write(http.MethodPost, previewPath, resume, http.StatusConflict, "Problem"); problem["code"] != "ALIAS_REQUESTS_UNDRAINED" {
		t.Fatal("preview accepted a target with a held request")
	}
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
	tracePath := "/api/v1/admin/distribution/workspaces/" + workspace + "/requests/" + admitted.RequestID
	trace := admin.Call(http.MethodGet, tracePath, nil, http.StatusOK, "GatewayRequestResponse")
	if trace["id"] != admitted.RequestID || trace["node_id"] != a.alias.NodeID ||
		trace["deployment_id"] != a.alias.DeploymentID || trace["deployment_revision"] != float64(a.alias.DeploymentRevision) ||
		trace["alias_id"] != a.alias.ID || trace["api_key_id"] != a.key.Key.ID || trace["actor_id"] != a.owner.Me.User.ID ||
		trace["state"] != "reported" {
		t.Fatal("request detail did not identify the original admitted target and owner")
	}
	if problem := foreignAdmin.Call(http.MethodGet, tracePath, nil, http.StatusNotFound, "Problem"); problem["code"] != "RESOURCE_NOT_FOUND" {
		t.Fatal("historical request detail crossed tenant scope")
	}
	next := c.replaceBaseDeployment(t, a.alias.DeploymentID)
	retarget := distribution.CutoverRequest{Action: "retarget", ExpectedRevision: 2, DeploymentID: &next.ID, DeploymentRevision: &next.Revision, Reason: "Same key explicit verified fresh target"}
	changed, e := applyPreview(retarget, uuid.NewString())
	if e != nil || changed.Alias.ID != id || changed.Alias.DisabledAt == nil || changed.Alias.DeploymentID != next.ID || changed.Alias.HeldRequests != 0 {
		t.Fatal("retarget", e)
	}
	_, e = c.s.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, *a.key.Secret)
	check(t, e, "MODEL_ALIAS_UNAVAILABLE")
	resume.ExpectedRevision = 3
	resumed, e := applyPreview(resume, uuid.NewString())
	if e != nil || !resumed.Alias.Available {
		t.Fatal("resume", e)
	}
	models, e := c.s.AuthorizeModels(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, *a.key.Secret)
	if e != nil || models.Models[0].DeploymentID != next.ID {
		t.Fatal("same original key changed binding", e)
	}
	if current := admin.Call(http.MethodGet, tracePath, nil, http.StatusOK, "GatewayRequestResponse"); !reflect.DeepEqual(current, trace) {
		t.Fatal("retarget rewrote the historical request detail")
	}
	pause.ExpectedRevision = 4
	paused, e = c.s.Cutover(ctx, c.a, workspace, id, pause, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	restored := c.replaceBaseDeployment(t, next.ID)
	source := int64(1)
	rollback := distribution.CutoverRequest{Action: "rollback", ExpectedRevision: paused.Alias.Revision, DeploymentID: &restored.ID, DeploymentRevision: &restored.Revision, SourceRevision: &source, Reason: "Selected recorded model redeployed and verified before rollback"}
	rolled, e := applyPreview(rollback, uuid.NewString())
	if e != nil || rolled.Alias.DisabledAt == nil {
		t.Fatal("recorded model rollback", e)
	}
	resume.ExpectedRevision = rolled.Alias.Revision
	if _, e = c.s.Cutover(ctx, c.a, workspace, id, resume, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if current := admin.Call(http.MethodGet, tracePath, nil, http.StatusOK, "GatewayRequestResponse"); !reflect.DeepEqual(current, trace) {
		t.Fatal("rollback rewrote the historical request detail")
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
