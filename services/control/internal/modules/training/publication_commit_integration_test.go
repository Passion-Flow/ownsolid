//go:build repository_integration

package training_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type publicationQueryHook func(context.Context, string)

func (h publicationQueryHook) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	h(ctx, data.SQL)
	return ctx
}

func (publicationQueryHook) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func (p publicationCostFixture) faultService(cfg *pgxpool.Config, hook publicationQueryHook) *training.Service {
	p.t.Helper()
	cfg.ConnConfig.Tracer = hook
	db, err := pgxpool.NewWithConfig(p.t.Context(), cfg)
	if err != nil {
		p.t.Fatal(err)
	}
	p.t.Cleanup(db.Close)
	owner := *p.f.s.Identity
	owner.DB = db
	handler := transport.New(&owner)
	handler.Training.Store = p.f.s.Store
	handler.Datasets.Service.Store = p.f.s.Datasets.Store
	handler.Datasets.Service.Processor = p.f.s.Datasets.Processor
	return handler.Training
}

func TestActualTrainingPublicationCommitAndRevocationFences(t *testing.T) {
	p := newPublicationCostFixture(t, 0)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	finish := func(s *training.Service, id string) (training.OutputAcceptance, error) {
		return s.FinishUpload(ctx, p.f.input.NodeID, p.f.credential, p.lease.TaskID, p.lease.LeaseToken, id)
	}
	checkpoint := p.stage("checkpoint", p.checkpoint, p.checkpointFiles, "")
	if _, err := p.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_publication_audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.action='training.checkpoint_accepted' THEN RAISE EXCEPTION 'SYNTHETIC_PUBLICATION_AUDIT_FAILURE'; END IF;
RETURN NEW; END $$;
CREATE TRIGGER reject_publication_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_publication_audit()`); err != nil {
		t.Fatal(err)
	}
	out, err := finish(p.f.s, checkpoint)
	if err == nil || out != (training.OutputAcceptance{}) ||
		count(t, p.f, "SELECT count(*) FROM training_checkpoints WHERE id=$1", checkpoint) != 0 ||
		count(t, p.f, "SELECT count(*) FROM training_uploads WHERE id=$1 AND state='uploading' AND active_chunk_id IS NULL", checkpoint) != 1 {
		t.Fatalf("failed publication audit changed committed state or retained its ticket: %+v %v", out, err)
	}
	if _, err = p.f.s.DB.Exec(ctx, "DROP TRIGGER reject_publication_audit ON audit_events"); err != nil {
		t.Fatal(err)
	}

	cfg := p.f.s.DB.Config()
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
	var finalWrites, cleanup atomic.Int32
	fault := p.faultService(cfg, func(_ context.Context, sql string) {
		if strings.HasPrefix(sql, "UPDATE training_uploads SET state='accepted'") && finalWrites.Add(1) == 1 {
			armed <- proxy.Arm(commitfault.DropCommitAcknowledgement)
		}
		if strings.HasPrefix(sql, "UPDATE training_uploads SET active_chunk_id=NULL") {
			cleanup.Add(1)
		}
	})
	out, err = finish(fault, checkpoint)
	expected(t, err, string(transaction.CommitUnknown))
	if out != (training.OutputAcceptance{}) || finalWrites.Load() != 1 || cleanup.Load() != 1 {
		t.Fatal("unknown commit exposed acceptance, repeated a write or skipped exact-ticket cleanup")
	}
	select {
	case err = <-armed:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("fault was never armed at the acceptance write")
	}
	select {
	case observed := <-proxy.Outcomes():
		if !observed.Forwarded || !observed.ServerOK || !observed.Suppressed {
			t.Fatalf("expected a committed PostgreSQL outcome with its acknowledgement suppressed: %+v", observed)
		}
	case <-ctx.Done():
		t.Fatal("commit proxy did not report the acknowledgement loss")
	}
	p.finish(checkpoint, "checkpoint", true)
	if count(t, p.f, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='training.checkpoint_accepted'", p.lease.Training.Scope.JobID) != 1 {
		t.Fatal("checkpoint reconciliation duplicated audit")
	}

	artifact := p.stage("artifact", p.artifact(), p.adapterFiles, "")
	beforeAcceptance := make(chan struct{})
	resume := make(chan struct{})
	var candidates atomic.Int32
	fenced := p.faultService(p.f.s.DB.Config(), func(ctx context.Context, sql string) {
		if strings.HasPrefix(sql, "SELECT j.id::text,j.tenant_id::text,j.workspace_id::text,j.current_node_id::text,j.requested_by::text") && candidates.Add(1) == 3 {
			close(beforeAcceptance)
			select {
			case <-resume:
			case <-ctx.Done():
			}
		}
	})
	type outcome struct {
		result training.OutputAcceptance
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := finish(fenced, artifact)
		done <- outcome{result, err}
	}()
	select {
	case <-beforeAcceptance:
	case got := <-done:
		t.Fatalf("publication did not reach its final authority pass: %+v %v", got.result, got.err)
	case <-ctx.Done():
		t.Fatal("publication did not reach final acceptance")
	}
	command := p.lease.Training
	path := filepath.Join(p.f.s.Identity.Config.Training.ArtifactRoot, "artifacts", command.Scope.JobID, command.AttemptID, artifact, "publication.json")
	if _, err = os.Stat(path); err != nil {
		t.Fatal("final authority probe ran before durable private staging", err)
	}
	_, err = finish(p.f.s, artifact)
	expected(t, err, "TRAINING_UPLOAD_BUSY")
	if _, err = p.f.s.DB.Exec(ctx, "UPDATE users SET auth_epoch=auth_epoch+1 WHERE id=$1", p.f.portal.Me.User.ID); err != nil {
		t.Fatal(err)
	}
	close(resume)
	select {
	case got := <-done:
		expected(t, got.err, "ACCESS_DENIED")
		if got.result != (training.OutputAcceptance{}) {
			t.Fatal("revoked publication exposed acceptance")
		}
	case <-ctx.Done():
		t.Fatal("revoked publication did not finish")
	}
	out, err = finish(p.f.s, checkpoint)
	expected(t, err, "ACCESS_DENIED")
	if out != (training.OutputAcceptance{}) {
		t.Fatal("revoked lease replay exposed an accepted reference")
	}
	if count(t, p.f, "SELECT count(*) FROM training_artifacts WHERE id=$1", artifact) != 0 ||
		count(t, p.f, "SELECT count(*) FROM audit_events WHERE target_id=$1 AND action='training.artifact_accepted'", command.Scope.JobID) != 0 ||
		count(t, p.f, "SELECT count(*) FROM gpu_leases WHERE training_job_id=$1 AND state='held'", command.Scope.JobID) != 1 {
		t.Fatal("revoked acceptance changed artifact, audit or physical ownership")
	}
}
