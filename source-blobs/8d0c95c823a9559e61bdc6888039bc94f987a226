package sso

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

func TestM11ActualRestrictedDatabaseRoleSupportsSSOLifecycleWithoutSchemaOwnership(t *testing.T) {
	f := sourceFixture(t)
	ctx := context.Background()
	schema := f.s.Identity.DB.Config().ConnConfig.RuntimeParams["search_path"]
	if !strings.HasPrefix(schema, "m01_") || strings.ContainsAny(schema, "; '\"") {
		t.Fatal("isolated generated schema required")
	}
	role := "m11_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := security.Token()
	// Only a generated safe identifier and base64url synthetic password enter DDL.
	if _, e := f.s.Identity.DB.Exec(ctx, "CREATE ROLE "+role+" LOGIN NOINHERIT CONNECTION LIMIT 32 PASSWORD '"+password+"'"); e != nil {
		t.Fatal("isolated runtime role creation failed")
	}
	defer func() {
		if _, e := f.s.Identity.DB.Exec(ctx, "DROP OWNED BY "+role+"; DROP ROLE "+role); e != nil {
			t.Error("isolated runtime role cleanup failed")
		}
	}()
	if _, e := f.s.Identity.DB.Exec(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schema+" TO "+role+"; REVOKE INSERT,UPDATE,DELETE ON schema_migrations FROM "+role); e != nil {
		t.Fatal("isolated runtime grants failed")
	}
	pc := f.s.Identity.DB.Config().Copy()
	pc.ConnConfig.User = role
	pc.ConnConfig.Password = password
	pool, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		t.Fatal("isolated runtime pool failed")
	}
	defer pool.Close()
	accounts := *f.s.Identity
	accounts.DB = pool
	accounts.Sources = nil
	mail := *accounts.Mail
	mail.DB = pool
	accounts.Mail = &mail
	service, e := New(&accounts)
	if e != nil {
		t.Fatal("runtime role could not verify encrypted configuration")
	}
	defer service.Close()
	accounts.Sources = service
	a, e := accounts.Authenticate(ctx, f.local, "admin")
	if e != nil {
		t.Fatal("runtime local authentication failed")
	}
	created, e := service.CreateConnector(ctx, a, CreateConnector{ID: uuid.NewString(), Name: "Runtime-granted connector", Provider: ProviderGitea, Origin: "https://provider.example.invalid", ClientID: uuid.NewString(), ClientSecret: security.Token(), Enabled: true, Reason: "Synthetic runtime grant verification"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal("runtime connector transaction failed")
	}
	if _, e = service.Start(ctx, created.ID, "admin", "", nil, StartInput{Mode: ModeLogin}, uuid.NewString()); e != nil {
		t.Fatal("runtime encrypted flow transaction failed")
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal("runtime session transaction failed")
	}
	defer tx.Rollback(ctx)
	issued, e := accounts.IssueSession(ctx, tx, a.Me.User.ID, a.Epoch, "admin", "", uuid.NewString(), &f.source)
	if e != nil {
		t.Fatal("runtime source session and issuance-journal trigger failed")
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal("runtime source session commit failed")
	}
	if _, e = accounts.Authenticate(ctx, issued.Token, "admin"); e != nil {
		t.Fatal("runtime current source guard failed")
	}
	if _, e = service.UpdateConnector(ctx, a, f.source.ConnectorID, UpdateConnector{Revision: 1, Name: "Runtime disabled source", Enabled: false, Reason: "Runtime source revocation"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal("runtime connector revocation failed")
	}
	_, e = accounts.Authenticate(ctx, issued.Token, "admin")
	requireCode(t, e, "AUTH_SESSION_EXPIRED")
	if _, e = service.Unlink(ctx, a, f.source.BindingID, UnlinkBinding{Revision: 1, Password: f.password, Reason: "Runtime retained binding unlink"}, uuid.NewString(), uuid.NewString()); e != nil {
		t.Fatal("runtime password-confirmed binding unlink failed")
	}
	if e = service.Cleanup(ctx); e != nil {
		t.Fatal("runtime bounded maintenance failed")
	}
	if _, e = pool.Exec(ctx, `UPDATE schema_migrations SET checksum=checksum`); e == nil {
		t.Fatal("runtime role can modify migration metadata")
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE sso_connectors ADD COLUMN forbidden integer`); e == nil {
		t.Fatal("runtime role owns the product schema")
	}
}
