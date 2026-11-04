package database_test

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

// Upgrading from any earlier prefix of the lineage must produce exactly the
// catalog of a fresh installation, and keep the installation's real data.
func TestLineageUpgradeFromEveryPrefixMatchesFreshCatalog(t *testing.T) {
	fresh := fixture.New(t)
	want := lineageCatalog(t, fresh.DB)
	for _, version := range []int{1, 4, 6, 8, 12, 14, 16, 18, 22, database.SchemaVersion - 1} {
		s := fixture.NewAtVersion(t, version)
		if e := database.Migrate(t.Context(), s.DB); e != nil {
			t.Fatal(version, e)
		}
		if e := database.Check(t.Context(), s.DB); e != nil {
			t.Fatal(version, e)
		}
		got := lineageCatalog(t, s.DB)
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatal("upgraded catalog differs from fresh", version)
		}
	}
}

// The delivered account22 prefix is the oldest installed schema in the field.
// Its bootstrap identity must survive the upgrade unchanged.
func TestLineageUpgradeKeepsBootstrappedInstallation(t *testing.T) {
	s := fixture.NewAtVersion(t, 22)
	fixture.Bootstrap(t, s)
	before := installationIdentity(t, s.DB)
	if e := database.Migrate(t.Context(), s.DB); e != nil {
		t.Fatal(e)
	}
	if after := installationIdentity(t, s.DB); after != before {
		t.Fatal("installation identity changed", before, after)
	}
	var entries int
	if e := s.DB.QueryRow(t.Context(), "SELECT count(*) FROM schema_migrations").Scan(&entries); e != nil || entries != database.SchemaVersion {
		t.Fatal(entries, e)
	}
}

func TestLineageRejectsForeignLedger(t *testing.T) {
	s := fixture.NewAtVersion(t, 4)
	if _, e := s.DB.Exec(t.Context(), "UPDATE schema_migrations SET checksum=repeat('0',64) WHERE version=2"); e != nil {
		t.Fatal(e)
	}
	if e := database.Migrate(t.Context(), s.DB); e == nil || e.Error() != "MIGRATION_CHECKSUM_MISMATCH" {
		t.Fatal(e)
	}
}

func installationIdentity(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	var out string
	if e := p.QueryRow(t.Context(), `SELECT i.tenant_id::text||'|'||i.default_workspace_id::text||'|'||u.email_canonical||'|'||(SELECT count(*) FROM users)::text FROM installation_state i JOIN users u ON u.id=i.primary_admin_user_id WHERE i.id=1 AND i.bootstrap_complete`).Scan(&out); e != nil {
		t.Fatal(e)
	}
	return out
}

func lineageCatalog(t *testing.T, p *pgxpool.Pool) []string {
	t.Helper()
	query := `WITH definitions AS (
 SELECT 'column' kind,c.relname owner,a.attnum::text identity,concat_ws(':',a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull::text,COALESCE(pg_get_expr(d.adbin,d.adrelid),'')) body
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum
 WHERE n.nspname=current_schema() AND c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped
 UNION ALL SELECT 'constraint',c.relname,k.conname,pg_get_constraintdef(k.oid) FROM pg_constraint k JOIN pg_class c ON c.oid=k.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema()
 UNION ALL SELECT 'index',c.relname,x.relname,pg_get_indexdef(i.indexrelid) FROM pg_index i JOIN pg_class c ON c.oid=i.indrelid JOIN pg_class x ON x.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema()
 UNION ALL SELECT 'trigger',c.relname,g.tgname,pg_get_triggerdef(g.oid) FROM pg_trigger g JOIN pg_class c ON c.oid=g.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=current_schema() AND NOT g.tgisinternal
 UNION ALL SELECT 'function',p.proname,pg_get_function_identity_arguments(p.oid),pg_get_functiondef(p.oid) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=current_schema()
 ) SELECT kind,owner,identity,replace(body,current_schema(),'OWNED_SCHEMA') FROM definitions ORDER BY 1,2,3`
	rows, e := p.Query(t.Context(), query)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var kind, owner, id, body string
		if e = rows.Scan(&kind, &owner, &id, &body); e != nil {
			t.Fatal(e)
		}
		out = append(out, strings.Join([]string{kind, owner, id, body}, "\t"))
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return out
}
