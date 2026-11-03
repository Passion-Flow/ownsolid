//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func preparationEvents(p, attempt string, rows int64) [][]byte {
	out := [][]byte{}
	for sequence := int64(1); sequence <= 2*rows+3; sequence++ {
		e := wire.Event{Sequence: sequence, PreparationID: p, AttemptID: attempt, Kind: "phase"}
		switch sequence {
		case 1:
			e.Phase = "evaluating_base"
		case rows + 2:
			e.Phase = "merging"
		case rows + 3:
			e.Phase = "evaluating_candidate"
		default:
			e.Kind = "evaluation_progress"
			e.Phase = "base"
			completed := sequence - 1
			if sequence > rows+3 {
				e.Phase = "candidate"
				completed = sequence - rows - 3
			}
			e.CompletedRows = &completed
			e.TotalRows = &rows
		}
		raw, _ := json.Marshal(e)
		out = append(out, raw)
	}
	return out
}

func TestActualCpuModelPreparationInputFilesReturnOnlyFrozenActualAdapterAndHeldout(t *testing.T) {
	g := newPreparationScenario(t)
	preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	ctx := context.Background()
	for _, asset := range l.Preparation.Inputs.AdapterFiles {
		out, e := g.service.InputFile(ctx, g.node.NodeID, g.node.Credential, l.TaskID, wire.InputFileRequest{LeaseToken: l.LeaseToken, Kind: "adapter", Asset: asset.Path, Length: asset.SizeBytes})
		if e != nil {
			t.Fatal("actual adapter transfer", e)
		}
		raw, e := io.ReadAll(io.NewSectionReader(out.File, out.Offset, out.Size))
		out.File.Close()
		sum := sha256.Sum256(raw)
		if e != nil || int64(len(raw)) != asset.SizeBytes || out.SHA256 != asset.SHA256 || hex.EncodeToString(sum[:]) != asset.SHA256 {
			t.Fatal("actual adapter range/hash")
		}
	}
	asset := l.Preparation.Inputs.Heldout
	input := wire.InputFileRequest{LeaseToken: l.LeaseToken, Kind: "heldout", Asset: asset.Path, Length: asset.SizeBytes}
	out, e := g.service.InputFile(ctx, g.node.NodeID, g.node.Credential, l.TaskID, input)
	if e != nil {
		t.Fatal("actual heldout transfer", e)
	}
	raw, e := io.ReadAll(io.NewSectionReader(out.File, out.Offset, out.Size))
	out.File.Close()
	sum := sha256.Sum256(raw)
	if e != nil || out.SHA256 != asset.SHA256 || hex.EncodeToString(sum[:]) != asset.SHA256 {
		t.Fatal("actual heldout range/hash")
	}
	for _, mutate := range []func(*wire.InputFileRequest){func(r *wire.InputFileRequest) { r.Asset = "../effective.jsonl" }, func(r *wire.InputFileRequest) { r.Kind = "checkpoint" }, func(r *wire.InputFileRequest) { r.Offset = 1 }, func(r *wire.InputFileRequest) { r.Length-- }, func(r *wire.InputFileRequest) { r.Asset = "labels.json" }} {
		request := input
		mutate(&request)
		result, e := g.service.InputFile(ctx, g.node.NodeID, g.node.Credential, l.TaskID, request)
		if e == nil || result.File != nil {
			if result.File != nil {
				result.File.Close()
			}
			t.Fatal("unfrozen input disclosure")
		}
	}
	if _, e = g.service.InputFile(ctx, g.node.NodeID, g.node.Credential, g.l.TaskID, input); e == nil {
		t.Fatal("foreign operation/task input disclosed")
	}
}

func TestActualCpuModelPreparationInputRechecksCurrentRoleAfterOutsideLockVerification(t *testing.T) {
	g := newPreparationScenario(t)
	preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	actual := g.service.Datasets.(*datasets.Service)
	paused := &pausedPreparationSource{Service: actual, entered: make(chan struct{}), release: make(chan struct{})}
	g.service.Datasets = paused
	var once sync.Once
	release := func() { once.Do(func() { close(paused.release) }) }
	t.Cleanup(release)
	finished := make(chan error, 1)
	asset := l.Preparation.Inputs.Heldout
	go func() {
		out, e := g.service.InputFile(ctx, g.node.NodeID, g.node.Credential, l.TaskID, wire.InputFileRequest{LeaseToken: l.LeaseToken, Kind: "heldout", Asset: asset.Path, Length: asset.SizeBytes})
		if out.File != nil {
			out.File.Close()
		}
		finished <- e
	}()
	select {
	case <-paused.entered:
	case <-ctx.Done():
		t.Fatal("real source verification did not pause")
	}
	if _, e := g.f.s.DB.Exec(ctx, "UPDATE workspace_memberships SET role='viewer' WHERE workspace_id=$1 AND user_id=$2", g.input.WorkspaceID, g.f.portal.Me.User.ID); e != nil {
		release()
		t.Fatal("source verification retained Workspace lock", e)
	}
	release()
	select {
	case e := <-finished:
		expected(t, e, "ACCESS_DENIED")
	case <-ctx.Done():
		t.Fatal("transfer failed to join")
	}
}

func TestActualCpuModelPreparationOrderedEventsStrictReplayAtomicityAndAuditRollback(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	ctx := context.Background()
	events := preparationEvents(p.ID, *p.CurrentAttemptID, 3)
	values, e := g.service.RecordEvents(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, [][]byte{events[0]})
	if e != nil || len(values) != 1 || values[0].State != "running" || values[0].Duplicate {
		t.Fatal("actual phase acceptance", e)
	}
	values, e = g.service.RecordEvents(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, [][]byte{events[0]})
	if e != nil || len(values) != 1 || !values[0].Duplicate {
		t.Fatal("exact event replay", e)
	}
	_, e = g.service.RecordEvents(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, [][]byte{events[1], events[3]})
	expected(t, e, "MODEL_PREPARATION_EVENT_ORDER_CONFLICT")
	if count(t, g.f, "SELECT count(*) FROM model_preparation_events WHERE attempt_id=$1", *p.CurrentAttemptID) != 1 {
		t.Fatal("invalid batch partially committed")
	}
	for _, bad := range [][]byte{bytes.Replace(events[1], []byte(`"total_rows":3`), []byte(`"total_rows":4`), 1), bytes.Replace(events[0], []byte(`"phase":"evaluating_base"`), []byte(`"phase":"evaluating_base","completed_rows":null`), 1), bytes.Replace(events[1], []byte(*p.CurrentAttemptID), []byte(uuid.NewString()), 1)} {
		_, e = g.service.RecordEvents(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, [][]byte{bad})
		expected(t, e, "MODEL_PREPARATION_EVENT_INVALID")
	}
	if _, e = g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_event_audit() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_event_recorded' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_EVENT_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_event_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_event_audit()`); e != nil {
		t.Fatal(e)
	}
	if _, e = g.service.RecordEvents(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, events[1:]); e == nil {
		t.Fatal("event audit failure accepted")
	}
	if count(t, g.f, "SELECT count(*) FROM model_preparation_events WHERE attempt_id=$1", *p.CurrentAttemptID) != 1 {
		t.Fatal("audit rollback retained progress")
	}
	if _, e = g.f.s.DB.Exec(ctx, "DROP TRIGGER reject_preparation_event_audit ON audit_events"); e != nil {
		t.Fatal(e)
	}
	values, e = g.service.RecordEvents(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken, events[1:])
	if e != nil || len(values) != 8 {
		t.Fatal("complete actual ordered progress", e)
	}
	if count(t, g.f, "SELECT last_event_sequence FROM model_preparation_attempts WHERE id=$1", *p.CurrentAttemptID) != 9 || count(t, g.f, "SELECT count(*) FROM model_preparation_events WHERE attempt_id=$1", *p.CurrentAttemptID) != 9 {
		t.Fatal("complete event accounting")
	}
}

func TestActualCpuModelPreparationTLSMachineSchemasEventsAndActualHeldoutRange(t *testing.T) {
	g := newPreparationScenario(t)
	p := preparationDispatched(t, g)
	handler := transport.New(g.f.s.Identity)
	handler.Nodes.Service.Preparations = g.service
	handler.Nodes.PreparationInputFile = g.service.InputFile
	handler.Nodes.PreparationEvents = g.service.RecordEvents
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	machine := httpfixture.New(t, server, "admin", schemas)
	machine.Headers = map[string]string{"Authorization": "Bearer " + g.node.Credential}
	base := "/agent/v1/nodes/" + g.node.NodeID + "/tasks/"
	claimed := machine.Call("POST", base+"claims", map[string]any{}, 200, "ClaimResponse")
	raw, _ := json.Marshal(claimed["lease"])
	var l agentwire.Lease
	if json.Unmarshal(raw, &l) != nil || l.Preparation == nil {
		t.Fatal("typed TLS lease")
	}
	base += l.TaskID + "/"
	events := preparationEvents(p.ID, *p.CurrentAttemptID, 3)
	items := []json.RawMessage{}
	for _, event := range events {
		items = append(items, event)
	}
	machine.Call("POST", base+"model-preparation-events", map[string]any{"lease_token": l.LeaseToken, "events": items}, 200, "ModelPreparationEventBatchResponse")
	problem := machine.Call("POST", base+"model-preparation-input-files", map[string]any{"lease_token": l.LeaseToken, "kind": "heldout", "asset": "heldout.jsonl", "offset": nil, "length": 1}, 400, "Problem")
	if problem["code"] != "VALIDATION_FAILED" {
		t.Fatal("strict transfer refusal")
	}
	asset := l.Preparation.Inputs.Heldout
	body, _ := json.Marshal(wire.InputFileRequest{LeaseToken: l.LeaseToken, Kind: "heldout", Asset: asset.Path, Length: asset.SizeBytes})
	request, _ := http.NewRequest("POST", httpfixture.AdminOrigin+base+"model-preparation-input-files", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+g.node.Credential)
	response, e := server.Client().Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	data, e := io.ReadAll(response.Body)
	sum := sha256.Sum256(data)
	if e != nil || response.StatusCode != 200 || response.ContentLength != asset.SizeBytes || response.Header.Get("X-Artifact-SHA256") != asset.SHA256 || hex.EncodeToString(sum[:]) != asset.SHA256 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("actual TLS immutable range")
	}
}
