//go:build training_artifact_integration && model_lifecycle_admission_integration

package training_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

func TestActualCpuModelPreparationControlWorkerShutdownAndStoreRestart(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	if e := g.service.VerifyStored(ctx); e != nil {
		t.Fatal(e)
	}
	preparationHardware(t, g)
	p := preparationQueued(t, g)
	policy := g.f.s.Identity.Config.Policy()
	policy.ModelLifecycle.PollMilliseconds = 100
	g.f.s.Identity.Config.Runtime = &policy
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { g.service.Run(run); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("control worker did not join")
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var e error
		p, e = g.service.Get(ctx, g.f.portal, p.WorkspaceID, p.ID)
		if e != nil {
			t.Fatal(e)
		}
		if p.State == "preparing" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bounded loop did not dispatch")
		}
		time.Sleep(10 * time.Millisecond)
	}
	preparationClaimed(t, g)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("control shutdown timeout")
	}
	if count(t, g.f, "SELECT count(*) FROM gpu_leases WHERE model_preparation_id=$1 AND state='held'", p.ID) != 1 || count(t, g.f, "SELECT count(*) FROM model_preparation_attempts WHERE preparation_id=$1 AND physical_stopped_at IS NULL", p.ID) != 1 {
		t.Fatal("shutdown invented remote stop")
	}
	if e := g.store.Close(); e != nil {
		t.Fatal(e)
	}
	g.service.Store = nil
	expected(t, g.service.VerifyStored(ctx), "MODEL_PREPARATION_STORAGE_REQUIRED")
	if e := g.service.OpenConfiguredStore(ctx); e != nil {
		t.Fatal("actual configured restart", e)
	}
	defer g.service.Store.Close()
	if g.service.Store.Namespace() != g.store.Namespace() {
		t.Fatal("restart replaced namespace")
	}
}

func TestActualCpuModelPreparationOperatorInitVerificationAuditAndRestoreRefusal(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	if e := g.service.AuditInitializedStore(ctx, g.root, os.Geteuid(), os.Getegid(), "Verify exact private initialized preparation namespace"); e != nil {
		t.Fatal(e)
	}
	if e := g.service.AuditStoreOperation(ctx, "verify", "Actual empty preparation store verification"); e != nil {
		t.Fatal(e)
	}
	if _, e := g.f.s.DB.Exec(ctx, `CREATE FUNCTION reject_preparation_store_verify() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN IF NEW.action='model.preparation_store_verify_verified' THEN RAISE EXCEPTION 'SYNTHETIC_PREPARATION_AUDIT_FAILURE';END IF;RETURN NEW;END;$$;CREATE TRIGGER reject_preparation_store_verify BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_preparation_store_verify()`); e != nil {
		t.Fatal(e)
	}
	expected(t, g.service.AuditStoreOperation(ctx, "verify", "Injected audit failure preserves exact files"), "STORAGE_UNAVAILABLE")
	if count(t, g.f, "SELECT count(*) FROM audit_events WHERE action='model.preparation_store_verify_verified'") != 1 {
		t.Fatal("failed verification wrote success")
	}
	fresh := filepath.Join(t.TempDir(), "fresh")
	if e := os.Mkdir(fresh, 0700); e != nil {
		t.Fatal(e)
	}
	preparationQueued(t, g)
	g.f.s.Identity.Config.ModelPreparation.ArtifactRoot = fresh
	expected(t, g.service.InitializeConfiguredStore(ctx, fresh, os.Geteuid(), os.Getegid(), "Must restore retained active preparation"), "MODEL_PREPARATION_STORE_RESTORE_REQUIRED")
	entries, e := os.ReadDir(fresh)
	if e != nil || len(entries) != 0 {
		t.Fatal("restore refusal changed new directory", e)
	}
}

func TestActualCpuModelPreparationRestrictedRuntimeRoleQueueEventsCancelAndNoDDL(t *testing.T) {
	g := newPreparationScenario(t)
	ctx := context.Background()
	operator := g.f.s.DB
	role := "m10_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := uuid.NewString()
	schema := operator.Config().ConnConfig.RuntimeParams["search_path"]
	if _, e := operator.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD '"+password+"'"); e != nil {
		t.Fatal("isolated runtime role creation failed")
	}
	defer func() { operator.Exec(ctx, "DROP OWNED BY "+role); operator.Exec(ctx, "DROP ROLE "+role) }()
	if _, e := operator.Exec(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role+";GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schema+" TO "+role+";REVOKE INSERT,UPDATE,DELETE ON schema_migrations FROM "+role); e != nil {
		t.Fatal(e)
	}
	config := operator.Config().Copy()
	config.ConnConfig.User = role
	config.ConnConfig.Password = password
	runtime, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	defer runtime.Close()
	if e = database.Check(ctx, runtime, g.f.s.Identity.Config.Server.Profile()); e != nil {
		t.Fatal(e)
	}
	g.service.DB = runtime
	g.f.s.DB = runtime
	g.f.s.Identity.DB = runtime
	g.f.s.Workspaces.DB = runtime
	g.f.s.Nodes.DB = runtime
	g.f.s.Models.DB = runtime
	g.f.s.Datasets.DB = runtime
	p := preparationDispatched(t, g)
	l := preparationClaimed(t, g)
	// Real current-role DML path; hardware/command observations remain explicit synthetic fixtures.
	if _, _, e = g.service.RenewPreparation(ctx, g.node.NodeID, g.node.Credential, l.TaskID, l.LeaseToken); e != nil {
		t.Fatal("restricted credential renewal", e)
	}
	if _, e = g.service.Cancel(ctx, g.f.portal, p.WorkspaceID, p.ID, modelpreparations.ActionRequest{ExpectedRevision: p.Revision, Reason: "Restricted runtime actual cancellation"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e = runtime.Exec(ctx, "CREATE TABLE forbidden_m10_runtime(id integer)"); e == nil {
		t.Fatal("runtime DDL allowed")
	}
	if _, e = runtime.Exec(ctx, "UPDATE schema_migrations SET checksum='forbidden' WHERE version=10"); e == nil {
		t.Fatal("runtime migration mutation allowed")
	}
}
