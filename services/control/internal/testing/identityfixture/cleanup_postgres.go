package identityfixture

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maximumFixtureCleanupTables = 512

// disposeOwnedPostgresTables is private to the two fixture constructors that
// already created or attested their exact synthetic namespace. One table per
// autocommit transaction bounds relation locks without changing cluster policy.
func disposeOwnedPostgresTables(ctx context.Context, pool *pgxpool.Pool, schema string) (int, error) {
	if ctx == nil || pool == nil || !isolatedSchema.MatchString(schema) && !nativeSourceSchema.MatchString(schema) {
		return 0, errors.New("OWNED_FIXTURE_SCHEMA_CLEANUP_CONTEXT_INVALID")
	}
	c := pool.Config().ConnConfig
	if c.Host != "127.0.0.1" || c.Port < 1024 || !nativeSQLName.MatchString(c.Database) || !strings.HasSuffix(c.Database, "_test") || !nativeSQLName.MatchString(c.User) || c.RuntimeParams["search_path"] != schema {
		return 0, errors.New("OWNED_FIXTURE_SCHEMA_CLEANUP_CONTEXT_INVALID")
	}
	rows, e := pool.Query(ctx, "SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relkind IN ('r','p') AND NOT c.relispartition ORDER BY c.relname", schema)
	if e != nil {
		return 0, errors.New("OWNED_FIXTURE_TABLE_INVENTORY_FAILED")
	}
	var names []string
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil || !nativeSQLName.MatchString(name) || len(names) >= maximumFixtureCleanupTables {
			rows.Close()
			return 0, errors.New("OWNED_FIXTURE_TABLE_INVENTORY_INVALID")
		}
		names = append(names, name)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return 0, errors.New("OWNED_FIXTURE_TABLE_INVENTORY_FAILED")
	}
	for index, name := range names {
		if _, e = pool.Exec(ctx, "DROP TABLE "+pgx.Identifier{schema, name}.Sanitize()+" CASCADE"); e != nil {
			return index, errors.New("OWNED_FIXTURE_TABLE_DISPOSAL_FAILED")
		}
	}
	return len(names), nil
}
