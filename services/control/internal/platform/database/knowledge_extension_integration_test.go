package database_test

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"strings"
	"testing"
	"time"
)

// Catalog corruption is explicitly injected only in a newly created disposable
// database, and each mutation rolls back. The retained native fixture's public
// extension and all original data are never altered.
func TestM12CatalogWrongExtensionTypeOperatorAndOwnerRefuseBeforeMigration(t *testing.T) {
	s := fixture.NewM12(t)
	ctx := t.Context()
	name := "m12_vector_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_test"
	c := s.DB.Config().Copy()
	delete(c.ConnConfig.RuntimeParams, "search_path")
	operator, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer operator.Close()
	if _, e = operator.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" TEMPLATE template0"); e != nil {
		t.Fatal(e)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, e := operator.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); e != nil {
			t.Error("exact disposable vector database cleanup", e)
		}
	}()
	c.ConnConfig.Database = name
	c.ConnConfig.RuntimeParams["search_path"] = "m12_vector_scope"
	p, e := pgxpool.NewWithConfig(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	if _, e = p.Exec(ctx, "CREATE SCHEMA m12_vector_scope; CREATE SCHEMA vector_wrong_scope"); e != nil {
		t.Fatal(e)
	}
	if e = database.Migrate(ctx, p, "knowledge"); e == nil || e.Error() != "KNOWLEDGE_APPROVED_VECTOR_EXTENSION_REQUIRED" {
		t.Fatal("missing extension admitted", e)
	}
	var exists bool
	if e = p.QueryRow(ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&exists); e != nil || exists {
		t.Fatal("missing extension changed ledger", e)
	}
	if _, e = p.Exec(ctx, "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC"); e != nil {
		t.Fatal(e)
	}
	role := "m12_vector_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = p.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS"); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if _, e := operator.Exec(context.Background(), "DROP ROLE "+pgx.Identifier{role}.Sanitize()); e != nil {
			t.Error("owned vector fault role cleanup", e)
		}
	}()
	faults := map[string]string{
		"version":             "UPDATE pg_catalog.pg_extension SET extversion='0.8.6' WHERE extname='vector'",
		"schema":              "ALTER EXTENSION vector SET SCHEMA vector_wrong_scope",
		"public_create":       "GRANT CREATE ON SCHEMA public TO PUBLIC",
		"type_owner":          "ALTER TYPE public.vector OWNER TO " + pgx.Identifier{role}.Sanitize(),
		"function_contract":   "ALTER FUNCTION public.vector_norm(public.vector) VOLATILE",
		"operator_membership": "ALTER EXTENSION vector DROP OPERATOR public.<=>(public.vector,public.vector)",
		"type_membership":     "ALTER EXTENSION vector DROP TYPE public.vector",
	}
	for name, sql := range faults {
		t.Run(name, func(t *testing.T) {
			tx, e := p.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback(context.Background())
			if _, e = tx.Exec(ctx, sql); e != nil {
				t.Fatal("explicit catalog fault injection", e)
			}
			if e = database.CheckKnowledgeVectorWithin(ctx, transaction.PostgreSQLTransaction(tx)); e == nil || e.Error() != "KNOWLEDGE_APPROVED_VECTOR_EXTENSION_REQUIRED" {
				t.Fatal("faulted catalog admitted", e)
			}
		})
	}
	if e = database.Migrate(ctx, p, "knowledge"); e != nil {
		t.Fatal("pristine catalog after fault rollback", e)
	}
	if e = database.Check(ctx, p, "knowledge"); e != nil {
		t.Fatal(e)
	}
	t.Log("Missing/wrong-version/schema/PUBLIC-create/type-owner/type-membership/C-function/operator-membership refuse; every fault rolled back in one exact disposable database; fresh74/18 migration then check passed")
}
