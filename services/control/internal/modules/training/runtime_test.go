package training_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActualTrainingControlWorkerDispatchAndJoinedShutdownRetainDeliveredPhysicalOwner(t *testing.T) {
	f := setup(t)
	attachStore(t, f)
	hardware(t, f)
	j := queued(t, f)
	policy := f.s.Identity.Config.Policy()
	policy.Training.PollMilliseconds = 100
	f.s.Identity.Config.Runtime = &policy
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.s.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("owned control worker did not join")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		var e error
		j, e = f.s.Get(context.Background(), f.portal, f.input.WorkspaceID, j.ID)
		if e != nil {
			t.Fatal(e)
		}
		if j.State == "preparing" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control loop did not dispatch durable queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
	claimed(t, f)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("control worker shutdown not bounded")
	}
	if count(t, f, "SELECT count(*) FROM gpu_leases WHERE state='held'") != 1 || count(t, f, "SELECT count(*) FROM training_attempts WHERE physical_stopped_at IS NULL") != 1 {
		t.Fatal("control shutdown invented remote physical stop")
	}
	if e := f.s.VerifyStored(context.Background()); e != nil {
		t.Fatal("valid private namespace not ready", e)
	}
	store := f.s.Store
	f.s.Store = nil
	e := f.s.VerifyStored(context.Background())
	f.s.Store = store
	expected(t, e, "TRAINING_STORAGE_REQUIRED")
}

func TestActualTrainingOperatorVerificationAuditFailureRetainsExactPrivateNamespace(t *testing.T) {
	f := setup(t)
	attachStore(t, f)
	ctx := context.Background()
	for _, operation := range []string{"unknown", "initialize"} {
		reason := "Explicit synthetic operator scope"
		if operation == "initialize" {
			reason = "invalid\nreason"
		}
		e := f.s.AuditStoreOperation(ctx, operation, reason)
		expected(t, e, "VALIDATION_FAILED")
	}
	if e := f.s.AuditStoreOperation(ctx, "initialize", "Explicit isolated initialized private training namespace"); e != nil {
		t.Fatal(e)
	}
	if count(t, f, "SELECT count(*) FROM audit_events WHERE audience='operator' AND action='training.store_initialize_verified'") != 1 {
		t.Fatal("operator initialization verification not audited")
	}
	marker := filepath.Join(f.s.Identity.Config.Training.ArtifactRoot, "namespace.json")
	before, e := os.ReadFile(marker)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.DB.Exec(ctx, `CREATE FUNCTION reject_store_verify() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='training.store_verify_verified' THEN RAISE EXCEPTION 'SYNTHETIC_STORE_VERIFY_AUDIT_FAILURE'; END IF; RETURN NEW; END$$;CREATE TRIGGER reject_store_verify BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_store_verify()`); e != nil {
		t.Fatal(e)
	}
	e = f.s.AuditStoreOperation(ctx, "verify", "Explicit synthetic operator audit rollback")
	expected(t, e, "STORAGE_UNAVAILABLE")
	after, e := os.ReadFile(marker)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("failed operator audit changed private namespace", e)
	}
	if count(t, f, "SELECT count(*) FROM audit_events WHERE action='training.store_verify_verified'") != 0 {
		t.Fatal("failed audit reported operator success")
	}
}

func TestActualTrainingConfiguredStoreRequiresInputStoresAndExclusiveKnownNamespace(t *testing.T) {
	f := setup(t)
	attachStore(t, f)
	ctx := context.Background()
	probe := *f.s
	probe.Store = nil
	// Explicitly remove the input prerequisite; cumulative fixtures now configure it.
	probe.Identity.Config.Models.ArtifactRoot = ""
	e := probe.OpenConfiguredStore(ctx)
	expected(t, e, "TRAINING_INPUT_STORES_REQUIRED")
	probe.Identity.Config.Models.ArtifactRoot = filepath.Join(t.TempDir(), "configured-model-root")
	e = probe.OpenConfiguredStore(ctx)
	expected(t, e, "TRAINING_STORE_IN_USE")
	if e = f.s.Store.Close(); e != nil {
		t.Fatal(e)
	}
	if e = probe.OpenConfiguredStore(ctx); e != nil {
		t.Fatal("known private store restart", e)
	}
	defer probe.Store.Close()
	if e = probe.VerifyStored(ctx); e != nil {
		t.Fatal("reopened store readiness", e)
	}
}
