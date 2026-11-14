//go:build repository_integration

package sso

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestActualSSOCatalogAdministrativeRoleRevocation(t *testing.T) {
	f := catalogRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "admin", nil)
	if page, e := f.s.ListConnectors(f.ctx, a, 1, ""); e != nil || len(page.Items) != 1 {
		t.Fatal("actual Admin before role change", e)
	}
	// Deliberately do not bump the epoch in this isolated privileged fault. The
	// current database role itself must fence an already authenticated Admin.
	f.exec(t, f.f.Statement(`UPDATE users SET is_platform_admin=false WHERE id=$1`, `UPDATE users SET is_platform_admin=false WHERE id=?`, f.f.User))
	page, e := f.s.ListConnectors(f.ctx, a, 1, "")
	if e == nil || e.Error() != "AUTH_SESSION_EXPIRED" || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatal("stale Admin flag exposed connector page", e)
	}
}

func TestActualSSOCatalogInstallationScopeReplacement(t *testing.T) {
	f := catalogRepositoryFixture(t)
	a := catalogAuth(t, f, f.f.User, "admin", nil)
	origin := f.accounts.Config.SSO.Providers[0].Origin
	catalogSeedConnector(t, f, f.f.Tenant, origin, "Old installation sibling", true, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))
	old, e := f.s.ListConnectors(f.ctx, a, 1, "")
	if e != nil || old.NextCursor == nil {
		t.Fatal("actual old installation cursor", e)
	}
	tenant, workspace := uuid.NewString(), uuid.NewString()
	f.exec(t, f.f.Statement(`INSERT INTO tenants(id,name,status) VALUES($1,'Replacement catalog tenant','active')`, `INSERT INTO tenants(id,name,status) VALUES(?,'Replacement catalog tenant','active')`, tenant))
	f.exec(t, f.f.Statement(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Replacement catalog Workspace','active')`, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Replacement catalog Workspace','active')`, workspace, tenant))
	connector := catalogSeedConnector(t, f, tenant, origin, "Replacement synthetic connector", true, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC))
	// This exact synthetic operator fault is not a public scope override or a
	// customer cutover procedure. All rows remain inside the disposable fixture.
	f.exec(t, f.f.Statement(`UPDATE installation_state SET tenant_id=$1,default_workspace_id=$2 WHERE id=1`, `UPDATE installation_state SET tenant_id=?,default_workspace_id=? WHERE id=1`, tenant, workspace))
	page, e := f.s.ListConnectors(f.ctx, a, 1, *old.NextCursor)
	if e == nil || e.Error() != "VALIDATION_FAILED" || len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatal("old installation cursor survived scope replacement", e)
	}
	page, e = f.s.ListConnectors(f.ctx, a, 1, "")
	if e != nil || len(page.Items) != 1 || page.Items[0].ID != connector || page.NextCursor != nil {
		t.Fatal("fresh Admin read trusted stale client scope", e)
	}
	items, e := f.s.PublicConnectors(f.ctx)
	if e != nil || len(items) != 1 || items[0].ID != connector {
		t.Fatal("public discovery trusted the previous installation", e)
	}
}
