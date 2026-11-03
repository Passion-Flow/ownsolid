package storeinitialization

import (
	"context"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	identityfixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"testing"
)

func TestM08SelectedDatasetNamespaceUsesCumulativeCatalog(t *testing.T) {
	s := identityfixture.NewM08(t)
	identityfixture.Bootstrap(t, s)
	ctx := t.Context()
	lease, e := s.DB.Acquire(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Release()
	if _, e = lease.Exec(ctx, "SELECT pg_advisory_lock($1)", database.InstallationLockID); e != nil {
		t.Fatal(e)
	}
	defer lease.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", database.InstallationLockID)
	c := s.Config
	c.Datasets.ArtifactRoot = filepath.Join(t.TempDir(), "dataset")
	if e = os.Mkdir(c.Datasets.ArtifactRoot, 0700); e != nil {
		t.Fatal(e)
	}
	if status, e := Run(ctx, s.DB, lease, c, options(c, "dataset", initialize)); e != nil || status != Verified {
		t.Fatal(status, e)
	}
	before, e := os.ReadFile(filepath.Join(c.Datasets.ArtifactRoot, "namespace.json"))
	if e != nil {
		t.Fatal(e)
	}
	for _, mode := range []string{check, record} {
		if status, e := Run(ctx, s.DB, lease, c, options(c, "dataset", mode)); e != nil || status != Verified {
			t.Fatal(mode, status, e)
		}
	}
	after, e := os.ReadFile(filepath.Join(c.Datasets.ArtifactRoot, "namespace.json"))
	if e != nil || string(before) != string(after) {
		t.Fatal("namespace changed", e)
	}
	var audits int
	if e = s.DB.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='dataset.store_initialize_verified'").Scan(&audits); e != nil || audits != 1 {
		t.Fatal("initialization duplicated audit", audits, e)
	}
	for _, kind := range []string{"training", "preparation"} {
		if _, e := Run(ctx, nil, nil, c, Options{Command: "initialize-" + kind + "-store"}); e == nil || e.Error() != "STORE_INITIALIZATION_PROFILE_DISABLED" {
			t.Fatal("later initializer reached missing owners", kind, e)
		}
	}
}
