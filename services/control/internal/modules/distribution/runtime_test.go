package distribution_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

func TestDistributionActualRuntimeDMLCredentialServesAuthorityWithoutDDL(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	operator := c.s.DB
	role := "m07_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := uuid.NewString()
	schema := operator.Config().ConnConfig.RuntimeParams["search_path"]
	if _, e := operator.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD '"+password+"'"); e != nil {
		t.Fatal("isolated runtime role creation failed")
	}
	defer func() { operator.Exec(ctx, "DROP OWNED BY "+role); operator.Exec(ctx, "DROP ROLE "+role) }()
	if _, e := operator.Exec(ctx, "GRANT USAGE ON SCHEMA "+schema+" TO "+role+"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA "+schema+" TO "+role+"; REVOKE INSERT,UPDATE,DELETE ON schema_migrations FROM "+role); e != nil {
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
	if e = database.Check(ctx, runtime, c.s.Identity.Config.Server.Profile()); e != nil {
		t.Fatal("runtime schema read", e)
	}
	c.s.DB = runtime
	c.s.Identity.DB = runtime
	mail := *c.s.Identity.Mail
	mail.DB = runtime
	c.s.Identity.Mail = &mail
	c.s.Workspaces.DB = runtime
	c.s.Nodes.DB = runtime
	c.s.Deployments.Models.DB = runtime
	a := c.access(t)
	input := a.input()
	if _, e = c.s.Admit(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, input, uuid.NewString()); e != nil {
		t.Fatal("actual runtime admission DML", e)
	}
	if _, e = c.s.Report(ctx, a.gateway.Gateway.ID, *a.gateway.Secret, success(input.RequestID), uuid.NewString()); e != nil {
		t.Fatal("actual runtime receipt DML", e)
	}
	if _, e = c.s.Request(ctx, a.owner, a.alias.WorkspaceID, input.RequestID); e != nil {
		t.Fatal("runtime scoped history", e)
	}
	if _, e = runtime.Exec(ctx, "CREATE TABLE forbidden_runtime_ddl(id integer)"); e == nil {
		t.Fatal("runtime executed DDL")
	}
	if _, e = runtime.Exec(ctx, "UPDATE schema_migrations SET checksum='forbidden' WHERE version=7"); e == nil {
		t.Fatal("runtime changed migration metadata")
	}
	if _, e = c.s.RevokeKey(ctx, c.a, a.alias.WorkspaceID, a.key.Key.ID, "Runtime-granted revoke", uuid.NewString()); e != nil {
		t.Fatal("runtime audited revocation", e)
	}
}
