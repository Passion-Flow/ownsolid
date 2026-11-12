package training_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

// These are explicitly synthetic authenticated node reports for real business transactions, not GPU computation evidence.
func eventFixture(l wire.Command, sequence, step int64, kind string, extra map[string]any) []byte {
	v := map[string]any{"job_id": l.Scope.JobID, "attempt_id": l.AttemptID, "sequence": sequence, "global_step": step, "kind": kind}
	for k, value := range extra {
		v[k] = value
	}
	raw, _ := json.Marshal(v)
	return raw
}
func prelude(l wire.Command) [][]byte {
	return [][]byte{eventFixture(l, 1, 0, "phase", map[string]any{"phase": "loading_model"}), eventFixture(l, 2, 0, "phase", map[string]any{"phase": "training"})}
}

func TestActualTrainingAtomicOrderedEventBatchConcurrentReplayAndRevocation(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	ctx := context.Background()
	_, e := f.s.RecordEvents(ctx, uuid.NewString(), f.credential, l.TaskID, l.LeaseToken, prelude(*l.Training))
	expected(t, e, "AGENT_LEASE_STALE")
	foreign := *l.Training
	foreign.AttemptID = uuid.NewString()
	_, e = f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, prelude(foreign))
	expected(t, e, "TRAINING_EVENT_INVALID")
	accepted, e := f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, prelude(*l.Training))
	if e != nil || len(accepted) != 2 || accepted[0].JobState != "preparing" || accepted[1].JobState != "running" {
		t.Fatal("actual metadata phase transitions", e)
	}
	raw := eventFixture(*l.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.25, "epoch": 0.1}})
	var wg sync.WaitGroup
	results := make(chan training.EventAcceptance, 2)
	errors := make(chan error, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			result, e := f.s.RecordEvent(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, raw)
			results <- result
			errors <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for e := range errors {
		if e != nil {
			t.Fatal(e)
		}
	}
	duplicates := 0
	for result := range results {
		if result.Duplicate {
			duplicates++
		}
	}
	if duplicates != 1 || count(t, f, "SELECT count(*) FROM training_events WHERE job_id=$1", j.ID) != 3 {
		t.Fatal("concurrent metadata replay duplicated event")
	}
	changed := eventFixture(*l.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 2.5}})
	_, e = f.s.RecordEvent(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, changed)
	expected(t, e, "TRAINING_EVENT_CONFLICT")
	gap := eventFixture(*l.Training, 5, 2, "metric", map[string]any{"values": map[string]any{"loss": 1.0}})
	_, e = f.s.RecordEvent(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, gap)
	expected(t, e, "TRAINING_EVENT_ORDER_CONFLICT")
	regressed := eventFixture(*l.Training, 4, 0, "phase", map[string]any{"phase": "training"})
	_, e = f.s.RecordEvent(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, regressed)
	expected(t, e, "TRAINING_EVENT_ORDER_CONFLICT")
	if count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1", j.ID) != 3 {
		t.Fatal("replayed metadata duplicated audit")
	}
	if count(t, f, `SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1
 AND tenant_id=$2 AND workspace_id=$3 AND actor_user_id=$4 AND audience='portal'`,
		j.ID, f.portal.Me.Workspace.TenantID, f.input.WorkspaceID, f.portal.Me.User.ID) != 3 {
		t.Fatal("event audit lost the exact attempt principal or scope")
	}
	if _, e = f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", f.portal.Me.User.ID); e != nil {
		t.Fatal(e)
	}
	_, e = f.s.RecordEvent(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, raw)
	expected(t, e, "ACCESS_DENIED")
}

func TestActualTrainingInvalidBatchAndAuditFailureRollbackEveryEventAndPhase(t *testing.T) {
	for _, mode := range []string{"foreign_event", "audit_failure"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			l := claimed(t, f)
			ctx := context.Background()
			batch := prelude(*l.Training)
			if mode == "foreign_event" {
				foreign := *l.Training
				foreign.Scope.JobID = uuid.NewString()
				batch[1] = eventFixture(foreign, 2, 0, "phase", map[string]any{"phase": "training"})
			} else {
				if _, e := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_event() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.event_recorded' THEN RAISE EXCEPTION 'SYNTHETIC_TRAINING_EVENT_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_training_event BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_training_event()`); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, batch); e == nil {
				t.Fatal("invalid atomic batch reported success")
			}
			if count(t, f, "SELECT count(*) FROM training_events WHERE job_id=$1", j.ID) != 0 || count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 0 || count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='preparing'", j.ID) != 1 {
				t.Fatal("partial event/phase survived rollback")
			}
			if count(t, f, "SELECT count(*) FROM training_checkpoints") != 0 || count(t, f, "SELECT count(*) FROM training_artifacts") != 0 {
				t.Fatal("metadata created accepted output references")
			}
		})
	}
}

func TestActualTLSOrderedTrainingEventBatchContractAndCancellation(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	handler := transport.New(f.s.Identity)
	handler.Datasets.Service.Store = f.s.Datasets.Store
	handler.Datasets.Service.Processor = f.s.Datasets.Processor
	server := httpfixture.NewTLSServer(t, handler)
	defer server.Close()
	schemas := httpfixture.Schemas(t, "../../../../../api/openapi/v1/nodes/openapi.json")
	client := httpfixture.New(t, server, "admin", schemas)
	client.Headers = map[string]string{"Authorization": "Bearer " + f.credential}
	values := prelude(*l.Training)
	events := []json.RawMessage{values[0], values[1]}
	body := map[string]any{"lease_token": l.LeaseToken, "events": events}
	path := "/agent/v1/sites/default/cells/default/nodes/" + f.input.NodeID + "/tasks/" + l.TaskID + "/training-events"
	first := client.Call("POST", path, body, 200, "TrainingEventBatchResponse")
	if len(first["items"].([]any)) != 2 {
		t.Fatal("actual event batch response")
	}
	client.Call("POST", path, body, 200, "TrainingEventBatchResponse")
	j, e := f.s.Get(context.Background(), f.portal, f.input.WorkspaceID, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Cancel(context.Background(), f.portal, f.input.WorkspaceID, j.ID, training.ActionRequest{ExpectedRevision: j.Revision, Reason: "Cancel actual metadata lease authority"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	client.Call("POST", path, body, 403, "Problem")
}

func TestActualTrainingMixedEventBatchPersistsFinalCursorAndExactReplay(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	ctx := t.Context()
	beforeRevision := count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID)
	batch := append(prelude(*l.Training),
		eventFixture(*l.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.25}}),
		eventFixture(*l.Training, 4, 2, "metric", map[string]any{"values": map[string]any{"loss": 1.0}}))
	out, err := f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, batch)
	if err != nil || len(out) != 4 || out[0].JobState != "preparing" || out[1].JobState != "running" {
		t.Fatal("batch lost its intermediate phase responses", err)
	}
	if count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 4 ||
		count(t, f, "SELECT last_global_step FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 2 ||
		count(t, f, "SELECT count(*) FROM training_attempts WHERE id=$1 AND state='running'", l.Training.AttemptID) != 1 ||
		count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision+1 {
		t.Fatal("batch did not persist its final cursor and one phase transition")
	}
	mixed := [][]byte{batch[2], batch[3], eventFixture(*l.Training, 5, 3, "metric", map[string]any{"values": map[string]any{"loss": 0.75}})}
	for replay := 0; replay < 2; replay++ {
		out, err = f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, mixed)
		if err != nil || len(out) != 3 {
			t.Fatal("mixed batch could not be replayed", err)
		}
		for index, item := range out {
			wantDuplicate := index < 2 || replay == 1
			if item.Duplicate != wantDuplicate || item.JobID != j.ID || item.AttemptID != l.Training.AttemptID ||
				item.Sequence != int64(index+3) || item.JobState != "running" {
				t.Fatal("mixed replay changed event identity or duplicate outcome")
			}
		}
	}
	if count(t, f, "SELECT count(*) FROM training_events WHERE job_id=$1", j.ID) != 5 ||
		count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 5 ||
		count(t, f, "SELECT last_global_step FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 3 ||
		count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision+1 {
		t.Fatal("replay changed the final cursor or repeated a phase transition")
	}
	if count(t, f, `SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1
 AND tenant_id=$2 AND workspace_id=$3 AND actor_user_id=$4 AND audience='portal'
 AND site_id=local_site_id() AND cell_id=local_cell_id()`,
		j.ID, f.portal.Me.Workspace.TenantID, f.input.WorkspaceID, f.portal.Me.User.ID) != 5 {
		t.Fatal("batch lost individual audit attribution or duplicated replay audits")
	}
}

func TestActualTrainingFinalCursorFailureRollsBackEventsPhaseAndAudits(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	ctx := t.Context()
	beforeRevision := count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID)
	batch := append(prelude(*l.Training),
		eventFixture(*l.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.25}}))
	_, err := f.s.DB.Exec(ctx, `CREATE FUNCTION reject_training_cursor() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.last_event_sequence=3 THEN RAISE EXCEPTION 'SYNTHETIC_TRAINING_CURSOR_FAILURE'; END IF;
RETURN NEW; END $$;
CREATE TRIGGER reject_training_cursor BEFORE UPDATE ON training_attempts FOR EACH ROW EXECUTE FUNCTION reject_training_cursor()`)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, batch)
	if err == nil || len(out) != 0 {
		t.Fatal("failed final cursor write exposed accepted events")
	}
	if count(t, f, "SELECT count(*) FROM training_events WHERE job_id=$1", j.ID) != 0 ||
		count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 0 ||
		count(t, f, "SELECT last_global_step FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 0 ||
		count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision ||
		count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='preparing'", j.ID) != 1 ||
		count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1", j.ID) != 0 {
		t.Fatal("final cursor failure retained an event, phase, cursor or audit")
	}
	if _, err = f.s.DB.Exec(ctx, "DROP TRIGGER reject_training_cursor ON training_attempts; DROP FUNCTION reject_training_cursor()"); err != nil {
		t.Fatal(err)
	}
	out, err = f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, batch)
	if err != nil || len(out) != 3 ||
		count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 3 ||
		count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision+1 ||
		count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1", j.ID) != 3 {
		t.Fatal("exact retry after cursor repair did not recover the atomic batch", err)
	}
}

func TestActualTrainingLateBatchOrderFailureRollsBackEveryMember(t *testing.T) {
	for _, mode := range []string{"sequence_gap", "step_regression", "changed_duplicate"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			j := dispatched(t, f)
			l := claimed(t, f)
			ctx := t.Context()
			beforeRevision := count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID)
			metric := eventFixture(*l.Training, 3, 2, "metric", map[string]any{"values": map[string]any{"loss": 1.25}})
			next := eventFixture(*l.Training, 4, 3, "metric", map[string]any{"values": map[string]any{"loss": 1.0}})
			code := "TRAINING_EVENT_ORDER_CONFLICT"
			switch mode {
			case "sequence_gap":
				next = eventFixture(*l.Training, 5, 3, "metric", map[string]any{"values": map[string]any{"loss": 1.0}})
			case "step_regression":
				next = eventFixture(*l.Training, 4, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.0}})
			case "changed_duplicate":
				next = eventFixture(*l.Training, 3, 2, "metric", map[string]any{"values": map[string]any{"loss": 2.5}})
				code = "TRAINING_EVENT_CONFLICT"
			}
			out, err := f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, append(prelude(*l.Training), metric, next))
			expected(t, err, code)
			if len(out) != 0 ||
				count(t, f, "SELECT count(*) FROM training_events WHERE job_id=$1", j.ID) != 0 ||
				count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 0 ||
				count(t, f, "SELECT last_global_step FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 0 ||
				count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision ||
				count(t, f, "SELECT count(*) FROM training_jobs WHERE id=$1 AND state='preparing'", j.ID) != 1 ||
				count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1", j.ID) != 0 {
				t.Fatal("late batch rejection retained an event, phase, cursor or audit")
			}
		})
	}
}

func TestActualTrainingConcurrentWholeEventBatchHasOnePersistedOutcome(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	beforeRevision := count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID)
	batch := append(prelude(*l.Training),
		eventFixture(*l.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.25}}),
		eventFixture(*l.Training, 4, 2, "metric", map[string]any{"values": map[string]any{"loss": 1.0}}))
	type result struct {
		items []training.EventAcceptance
		err   error
	}
	start := make(chan struct{})
	completed := make(chan result, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			<-start
			items, err := f.s.RecordEvents(t.Context(), f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, batch)
			completed <- result{items, err}
		}()
	}
	close(start)
	wg.Wait()
	close(completed)
	duplicateBatches := 0
	for got := range completed {
		if got.err != nil || len(got.items) != 4 {
			t.Fatal("concurrent whole batch failed", got.err)
		}
		duplicate := got.items[0].Duplicate
		if duplicate {
			duplicateBatches++
		}
		for index, item := range got.items {
			if item.Duplicate != duplicate || item.Sequence != int64(index+1) ||
				item.JobID != j.ID || item.AttemptID != l.Training.AttemptID {
				t.Fatal("concurrent batches interleaved acceptance")
			}
		}
	}
	if duplicateBatches != 1 ||
		count(t, f, "SELECT count(*) FROM training_events WHERE job_id=$1", j.ID) != 4 ||
		count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 4 ||
		count(t, f, "SELECT last_global_step FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 2 ||
		count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision+1 ||
		count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1", j.ID) != 4 {
		t.Fatal("concurrent batches duplicated or lost persisted progress")
	}
}
