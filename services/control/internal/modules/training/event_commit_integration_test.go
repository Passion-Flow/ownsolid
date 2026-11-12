//go:build repository_integration

package training_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

func TestActualTrainingEventBatchUnknownCommitAndExplicitReplay(t *testing.T) {
	f := setup(t)
	j := dispatched(t, f)
	l := claimed(t, f)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cfg := f.s.DB.Config()
	if cfg.ConnConfig.TLSConfig != nil || len(cfg.ConnConfig.Fallbacks) != 0 {
		t.Skip("the PostgreSQL wire fault fixture requires explicit plaintext loopback")
	}
	proxy, err := commitfault.NewPlaintextPostgreSQL(cfg.ConnConfig.Host, cfg.ConnConfig.Port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bounded, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := proxy.Close(bounded); err != nil {
			t.Error(err)
		}
	})
	cfg.ConnConfig.Port = proxy.Port()
	armed := make(chan error, 1)
	var cursorWrites atomic.Int32
	fixture := publicationCostFixture{costRun: &costRun{t: t, f: f}}
	fault := fixture.faultService(cfg, func(_ context.Context, sql string) {
		if strings.HasPrefix(strings.TrimSpace(sql), "UPDATE training_attempts SET last_event_sequence") &&
			cursorWrites.Add(1) == 1 {
			armed <- proxy.Arm(commitfault.DropCommitAcknowledgement)
		}
	})
	batch := append(prelude(*l.Training),
		eventFixture(*l.Training, 3, 1, "metric", map[string]any{"values": map[string]any{"loss": 1.25}}))
	beforeRevision := count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID)
	out, err := fault.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, batch)
	expected(t, err, string(transaction.CommitUnknown))
	if len(out) != 0 || cursorWrites.Load() != 1 {
		t.Fatal("unknown commit exposed acceptance or wrote progress per member")
	}
	select {
	case err = <-armed:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("event commit fault was never armed")
	}
	select {
	case observed := <-proxy.Outcomes():
		if !observed.Forwarded || !observed.ServerOK || !observed.Suppressed {
			t.Fatal("the event batch was not committed with its acknowledgement suppressed")
		}
	case <-ctx.Done():
		t.Fatal("the event commit proxy did not report its outcome")
	}
	if count(t, f, "SELECT count(*) FROM training_events WHERE job_id=$1", j.ID) != 3 ||
		count(t, f, "SELECT last_event_sequence FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 3 ||
		count(t, f, "SELECT last_global_step FROM training_attempts WHERE id=$1", l.Training.AttemptID) != 1 {
		t.Fatal("lost acknowledgement did not retain the committed batch")
	}
	out, err = f.s.RecordEvents(ctx, f.input.NodeID, f.credential, l.TaskID, l.LeaseToken, batch)
	if err != nil || len(out) != 3 {
		t.Fatal("explicit replay could not resolve the event commit", err)
	}
	for _, accepted := range out {
		if !accepted.Duplicate || accepted.JobID != j.ID || accepted.AttemptID != l.Training.AttemptID || accepted.JobState != "running" {
			t.Fatal("explicit replay changed a committed event")
		}
	}
	if count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.event_recorded' AND target_id=$1", j.ID) != 3 ||
		count(t, f, "SELECT revision FROM training_jobs WHERE id=$1", j.ID) != beforeRevision+1 {
		t.Fatal("explicit replay repeated an audit or phase transition")
	}
}
