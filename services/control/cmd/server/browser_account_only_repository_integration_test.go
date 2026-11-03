//go:build repository_integration && fullserve_browser_integration

package main

import (
	"context"
	"github.com/jackc/pgx/v5"
	"go.yaml.in/yaml/v3"
	"os/exec"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"strings"
	"testing"
	"time"
)

// Replace only this newly allocated empty disposable fixture before any bootstrap
// or runtime role exists. The actual native migrate command creates the account schema.
func liveAccountOnlySchema(t *testing.T, x *serverCompositionStory, input liveAccountBrowserInput) {
	t.Helper()
	profile := input.ApplicationProfile
	if profile == "" {
		profile = "account"
	}
	x.c.Server.ApplicationProfile = &profile
	scope := x.f.PostgreSQL.Config().ConnConfig.RuntimeParams["search_path"]
	if !strings.HasPrefix(scope, "m20_repository_") || len(scope) != 47 {
		t.Fatal("ACCOUNT_EXACT_FRESH_SCHEMA_REQUIRED")
	}
	if e := x.d.Close(); e != nil {
		t.Fatal(e)
	}
	rows, e := x.f.PostgreSQL.Query(x.ctx, "SELECT tablename FROM pg_tables WHERE schemaname=$1 ORDER BY tablename", scope)
	if e != nil {
		t.Fatal(e)
	}
	var names []string
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		names = append(names, name)
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	for _, name := range names {
		if _, e = x.f.PostgreSQL.Exec(x.ctx, "DROP TABLE "+pgx.Identifier{scope, name}.Sanitize()+" CASCADE"); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = x.f.PostgreSQL.Exec(x.ctx, "DROP SCHEMA "+pgx.Identifier{scope}.Sanitize()+" CASCADE; CREATE SCHEMA "+pgx.Identifier{scope}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	raw, e := yaml.Marshal(x.c)
	if e != nil {
		t.Fatal(e)
	}
	configuration := x.private(t, "account-migration.yaml", raw)
	ctx, cancel := context.WithTimeout(x.ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, input.ControlFile, "migrate", "--config", configuration)
	command.Env = liveCLIEnvironment()
	output, e := command.CombinedOutput()
	if e != nil || strings.TrimSpace(string(output)) != "MIGRATION_COMPLETE" {
		t.Fatal("ACTUAL_NATIVE_ACCOUNT_MIGRATION_FAILED")
	}
	if x.d, e = openSelectedDatabase(x.ctx, x.c, "serve"); e != nil {
		t.Fatal(e)
	}
	old := x.owner
	x.owner = x.d.identity(x.c)
	x.owner.Passwords = old.Passwords
	x.owner.HMACKey = old.HMACKey
	x.mail.DB = x.d.postgres
	x.mail.Config = x.c
	x.owner.Mail = x.mail
	x.h = identityhttp.New(x.owner)
	if e = x.h.InitializationError(); e != nil {
		t.Fatal(e)
	}
	var tables int
	if e = x.f.PostgreSQL.QueryRow(x.ctx, "SELECT count(*) FROM pg_tables WHERE schemaname=$1", scope).Scan(&tables); e != nil || tables != map[string]int{"account": 16, "workspaces": 17}[profile] {
		t.Fatal("ACCOUNT_SCHEMA_MUST_HAVE_EXACT_16_TABLES")
	}
	if e = x.d.Verify(x.ctx); e != nil {
		t.Fatal(e)
	}
	t.Log("ACCOUNT_ONLY_COLD_MIGRATION minimal_profile=true native_migrate=true no_vector_type_no_future_tables=true")
}
