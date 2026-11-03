package storeinitialization

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func fixture(t *testing.T, boot bool) (*identity.Service, *pgxpool.Conn, config.Config) {
	t.Helper()
	s := identityfixture.New(t)
	if boot {
		identityfixture.Bootstrap(t, s)
	}
	lease, e := s.DB.Acquire(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var locked bool
	if e = lease.QueryRow(context.Background(), "SELECT pg_try_advisory_lock($1)", database.InstallationLockID).Scan(&locked); e != nil || !locked {
		t.Fatal("test lock", e)
	}
	t.Cleanup(func() {
		lease.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
		lease.Release()
	})
	c := s.Config
	c.Datasets.ArtifactRoot = filepath.Join(t.TempDir(), "dataset")
	c.Training.ArtifactRoot = filepath.Join(t.TempDir(), "training")
	c.ModelPreparation.ArtifactRoot = filepath.Join(t.TempDir(), "preparation")
	for _, p := range []string{c.Datasets.ArtifactRoot, c.Training.ArtifactRoot, c.ModelPreparation.ArtifactRoot} {
		if e = os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	return s, lease, c
}
func options(c config.Config, kind, mode string) Options {
	d, _ := describe(c, "initialize-"+kind+"-store")
	command := "initialize-" + kind + "-store"
	if mode != initialize {
		command = mode + "-" + kind + "-store-initialization"
	}
	return Options{Command: command, Root: d.root, UID: os.Geteuid(), GID: os.Getegid(), ConfirmEmpty: true, Reason: "Isolated synthetic namespace initialization"}
}
func TestActualThreeStoreInitializationAuditAndReadonlyRecovery(t *testing.T) {
	for _, kind := range []string{"dataset", "training", "preparation"} {
		for _, recoverBoundary := range []bool{false, true} {
			t.Run(kind+map[bool]string{true: "-audit-absent", false: "-initialize"}[recoverBoundary], func(t *testing.T) {
				s, lease, c := fixture(t, true)
				ctx := context.Background()
				o := options(c, kind, initialize)
				if recoverBoundary {
					var tenant string
					if e := s.DB.QueryRow(ctx, "SELECT tenant_id::text FROM installation_state WHERE id=1").Scan(&tenant); e != nil {
						t.Fatal(e)
					}
					var e error
					switch kind {
					case "dataset":
						e = datasets.InitializeStore(o.Root, tenant, o.UID, o.GID)
					case "training":
						e = training.InitializeStore(o.Root, tenant, o.UID, o.GID)
					case "preparation":
						e = modelpreparations.InitializeStore(o.Root, tenant, o.UID, o.GID)
					}
					if e != nil {
						t.Fatal(e)
					}
					if status, e := Run(ctx, s.DB, lease, c, options(c, kind, check)); e != nil || status != NotRecorded {
						t.Fatal("missing audit fabricated", status, e)
					}
					if status, e := Run(ctx, s.DB, lease, c, options(c, kind, record)); e != nil || status != Verified {
						t.Fatal("explicit audit recovery", status, e)
					}
				} else {
					if status, e := Run(ctx, s.DB, lease, c, o); e != nil || status != Verified {
						t.Fatal(status, e)
					}
				}
				marker := filepath.Join(o.Root, "namespace.json")
				before, e := os.ReadFile(marker)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = Run(ctx, s.DB, lease, c, o); e == nil {
					t.Fatal("namespace initialization replayed")
				}
				for _, mode := range []string{check, record, check} {
					if status, e := Run(ctx, s.DB, lease, c, options(c, kind, mode)); e != nil || status != Verified {
						t.Fatal(mode, status, e)
					}
				}
				after, _ := os.ReadFile(marker)
				if string(before) != string(after) {
					t.Fatal("namespace changed")
				}
				d, _ := describe(c, o.Command)
				var audits int
				if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action=$1", d.action).Scan(&audits); e != nil || audits != 1 {
					t.Fatal("audit duplicated", audits, e)
				}
				if _, e = s.DB.Exec(ctx, "UPDATE audit_events SET metadata='{}'::jsonb WHERE action=$1", d.action); e != nil {
					t.Fatal(e)
				}
				if _, e = Run(ctx, s.DB, lease, c, options(c, kind, record)); e == nil || e.Error() != "STORE_INITIALIZATION_AUDIT_CONFLICT" {
					t.Fatal("conflicting audit replaced", e)
				}
			})
		}
	}
}
func TestStoreInitializationRequiresContextAndPreservesEmptyMountOnRefusal(t *testing.T) {
	s, lease, c := fixture(t, true)
	ctx := context.Background()
	base := options(c, "dataset", initialize)
	for _, mutate := range []func(*Options){func(o *Options) { o.Root = filepath.Dir(o.Root) }, func(o *Options) { o.ConfirmEmpty = false }, func(o *Options) { o.Reason = "" }, func(o *Options) { o.UID = -1 }} {
		o := base
		mutate(&o)
		if _, e := Run(ctx, s.DB, lease, c, o); e == nil {
			t.Fatal("invalid initialization accepted")
		}
		entries, e := os.ReadDir(base.Root)
		if e != nil || len(entries) != 0 {
			t.Fatal("refusal mutated mount", e)
		}
	}
	if _, e := Run(ctx, s.DB, nil, c, base); e == nil {
		t.Fatal("missing lease accepted")
	}
	other := identityfixture.New(t)
	if _, e := Run(ctx, other.DB, lease, c, base); e == nil || e.Error() != "STORE_INITIALIZATION_TARGET_MISMATCH" {
		t.Fatal("foreign target accepted", e)
	}
	var tenant, workspace, user string
	s.DB.QueryRow(ctx, "SELECT tenant_id::text,default_workspace_id::text,primary_admin_user_id::text FROM installation_state WHERE id=1").Scan(&tenant, &workspace, &user)
	if _, e := s.DB.Exec(ctx, `INSERT INTO datasets(id,tenant_id,workspace_id,name,name_key,origin,license,rights_asserted,created_by,creation_key,creation_fingerprint) VALUES($1,$2,$3,'Synthetic dataset','synthetic','Synthetic isolated source','Synthetic test rights',true,$4,$5,$6)`, uuid.NewString(), tenant, workspace, user, uuid.NewString(), make([]byte, 32)); e != nil {
		t.Fatal(e)
	}
	if _, e := Run(ctx, s.DB, lease, c, base); e == nil || e.Error() != "STORE_INITIALIZATION_RESTORE_REQUIRED" {
		t.Fatal("retained history ignored", e)
	}
	entries, _ := os.ReadDir(base.Root)
	if len(entries) != 0 {
		t.Fatal("retained history created replacement namespace")
	}
	lease.Exec(ctx, "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
	if _, e := Run(ctx, s.DB, lease, c, base); e == nil || e.Error() != "STORE_INITIALIZATION_LOCK_REQUIRED" {
		t.Fatal("unheld lease accepted", e)
	}
}
func TestStoreInitializationRequiresBootstrappedInstallation(t *testing.T) {
	s, lease, c := fixture(t, false)
	if _, e := Run(context.Background(), s.DB, lease, c, options(c, "dataset", initialize)); e == nil || e.Error() != "STORE_INITIALIZATION_INSTALLATION_REQUIRED" {
		t.Fatal(e)
	}
}

func TestActualAuditSQLTimeoutPreservesNamespaceForExplicitRecovery(t *testing.T) {
	s, lease, c := fixture(t, true)
	ctx := context.Background()
	o := options(c, "dataset", initialize)
	tx, e := s.DB.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, "LOCK TABLE audit_events IN ACCESS EXCLUSIVE MODE"); e != nil {
		t.Fatal(e)
	}
	bounded, stop := context.WithTimeout(ctx, 500*time.Millisecond)
	_, e = Run(bounded, s.DB, lease, c, o)
	stop()
	if e == nil || e.Error() != "STORE_INITIALIZATION_AUDIT_UNAVAILABLE" {
		t.Fatal("blocked audit did not fail", e)
	}
	before, e := os.ReadFile(filepath.Join(o.Root, "namespace.json"))
	if e != nil {
		t.Fatal("audit failure removed namespace", e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	if status, e := Run(ctx, s.DB, lease, c, options(c, "dataset", check)); e != nil || status != NotRecorded {
		t.Fatal("missing audit became success", status, e)
	}
	if status, e := Run(ctx, s.DB, lease, c, options(c, "dataset", record)); e != nil || status != Verified {
		t.Fatal("explicit recovery", status, e)
	}
	after, e := os.ReadFile(filepath.Join(o.Root, "namespace.json"))
	if e != nil || string(before) != string(after) {
		t.Fatal("recovery regenerated namespace", e)
	}
}
