package training_test

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestActualTrainingImmutableAttemptEventAndNullableOutcomeConstraints(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	ctx := context.Background()
	for _, statement := range []string{
		"UPDATE training_attempts SET input_inventory='{}' WHERE id=$1",
		"UPDATE training_attempts SET requested_epoch=requested_epoch+1 WHERE id=$1",
		"UPDATE training_attempts SET outcome_sha256=repeat('a',64) WHERE id=$1",
		"UPDATE training_attempts SET physical_stop_sha256=repeat('a',64) WHERE id=$1",
	} {
		if _, e := f.s.DB.Exec(ctx, statement, l.Training.AttemptID); e == nil {
			t.Fatal("immutable/partial metadata update accepted")
		}
	}
	if _, e := f.s.DB.Exec(ctx, "UPDATE training_jobs SET id=$2 WHERE id=$1", j.ID, uuid.NewString()); e == nil {
		t.Fatal("frozen job identity changed")
	}
	if _, e := f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, prelude(*l.Training)); e != nil {
		t.Fatal(e)
	}
	for _, statement := range []string{"UPDATE training_events SET canonical_sha256=repeat('b',64) WHERE attempt_id=$1", "DELETE FROM training_events WHERE attempt_id=$1"} {
		if _, e := f.s.DB.Exec(ctx, statement, l.Training.AttemptID); e == nil {
			t.Fatal("accepted event metadata mutated")
		}
	}
	if count(t, f, "SELECT count(*) FROM training_events WHERE attempt_id=$1", l.Training.AttemptID) != 2 {
		t.Fatal("immutable events lost")
	}
	if _, e := f.s.DB.Exec(ctx, "UPDATE training_jobs SET current_node_id=$2 WHERE id=$1", j.ID, uuid.NewString()); e == nil {
		t.Fatal("unscoped dispatch node accepted")
	}
}
