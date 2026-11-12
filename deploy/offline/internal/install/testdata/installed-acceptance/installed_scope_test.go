package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type combinedScope struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Application string `json:"application"`
}
type combinedScopeState struct {
	PrimaryWorkspace   string   `json:"primary_workspace"`
	PrimaryTenant      string   `json:"primary_tenant"`
	Members            []string `json:"members"`
	ForeignTenant      string   `json:"foreign_tenant"`
	ForeignWorkspace   string   `json:"foreign_workspace"`
	ForeignApplication string   `json:"foreign_application"`
	Marker             string   `json:"marker"`
}

func combinedFixtureDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw, err := os.ReadFile("/srv/ownsolid/operator/operator-dsn")
	if err != nil {
		t.Fatal("Installed operator file unavailable")
	}
	configuration, err := pgxpool.ParseConfig(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal("Installed database configuration refused")
	}
	if configuration.ConnConfig.RuntimeParams == nil {
		configuration.ConnConfig.RuntimeParams = map[string]string{}
	}
	configuration.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(t.Context(), configuration)
	if err != nil {
		t.Fatal("Installed database configuration refused")
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Fatal("Installed database unavailable")
	}
	return pool
}

func TestTemporaryInstalledScopePreparation(t *testing.T) {
	var provenance struct {
		Revision string `json:"build_revision"`
		Modified string `json:"build_modified"`
	}
	raw, err := os.ReadFile("/evidence/installed-provenance.json")
	if err != nil || json.Unmarshal(raw, &provenance) != nil || provenance.Revision == "" || provenance.Modified != "false" || provenance.Revision != os.Getenv("COMBINED_SOURCE_SHA") {
		t.Fatal("Installed source provenance differs from the admitted candidate")
	}
	pool := combinedFixtureDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	passwords, err := security.NewPasswords("/bundle/password-blocklist.txt")
	if err != nil {
		t.Fatal("Password policy unavailable")
	}
	type person struct {
		ID        string        `json:"-"`
		PHC       string        `json:"-"`
		Email     string        `json:"email"`
		Password  string        `json:"password"`
		Workspace combinedScope `json:"workspace"`
	}
	var first combinedScope
	var tenant, admin string
	if err := pool.QueryRow(ctx, "SELECT id,tenant_id,name FROM workspaces ORDER BY created_at,id LIMIT 1").Scan(&first.ID, &tenant, &first.Name); err != nil {
		t.Fatal("Installed workspace unavailable")
	}
	if err := pool.QueryRow(ctx, "SELECT id FROM users WHERE email_canonical='admin@ownsolid.test'").Scan(&admin); err != nil {
		t.Fatal("Installed administrator unavailable")
	}
	first.Application = "First workspace application"
	second := combinedScope{uuid.NewString(), "Second member workspace", "Second workspace application"}
	state := combinedScopeState{PrimaryWorkspace: first.ID, PrimaryTenant: tenant, ForeignTenant: uuid.NewString(), ForeignWorkspace: uuid.NewString(), Marker: "Foreign-tenant-application-" + uuid.NewString()}
	people := make([]person, 2)
	for i := range people {
		p := &people[i]
		p.ID = uuid.NewString()
		p.Email = fmt.Sprintf("scope-member-%d-%s@example.invalid", i, uuid.NewString())
		p.Password = "Fixture-Portal-Scope-" + uuid.NewString()
		p.Workspace = second
		if i == 0 {
			p.Workspace = first
		}
		p.PHC, err = passwords.Hash(ctx, p.Password)
		if err != nil {
			t.Fatal("Fixture password hashing refused")
		}
		state.Members = append(state.Members, p.ID)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("Scope fixture transaction unavailable")
	}
	defer tx.Rollback(ctx)
	exec := func(query string, args ...any) {
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			t.Fatal("Scope fixture SQL refused", err)
		}
	}
	exec("INSERT INTO tenants(id,name,status) VALUES($1,'Synthetic foreign tenant','active')", state.ForeignTenant)
	exec("INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,$3,'active'),($4,$5,'Synthetic foreign scope','active')", second.ID, tenant, second.Name, state.ForeignWorkspace, state.ForeignTenant)
	for _, p := range people {
		exec("INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,email_verified_at) VALUES($1,$2,$2,'Scope member','en','active',clock_timestamp())", p.ID, p.Email)
		exec("INSERT INTO password_credentials(user_id,password_phc) VALUES($1,$2)", p.ID, p.PHC)
		exec("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", p.ID, tenant, second.ID)
		if p.ID == people[0].ID {
			exec("INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role) VALUES($1,$2,$3,'member')", p.ID, tenant, first.ID)
		}
	}
	graph := []byte(`{"schema_version":1,"nodes":[],"edges":[]}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(graph))
	for _, item := range []struct{ tenant, workspace, name string }{{tenant, first.ID, first.Application}, {tenant, second.ID, second.Application}, {state.ForeignTenant, state.ForeignWorkspace, state.Marker}} {
		workflow, version := uuid.NewString(), uuid.NewString()
		if item.tenant == state.ForeignTenant {
			state.ForeignApplication = workflow
		}
		exec(`INSERT INTO workflows(id,tenant_id,workspace_id,name,name_key,enabled,execution_authority,draft_graph,draft_sha256,created_by) VALUES($1,$2,$3,$4::text,lower($4::text),true,$5,$6::jsonb,$7,$8)`, workflow, item.tenant, item.workspace, item.name, uuid.NewString(), graph, digest, admin)
		exec(`INSERT INTO workflow_versions(id,tenant_id,workspace_id,workflow_id,version_number,draft_revision,graph,graph_sha256,published_by) SELECT $1,tenant_id,workspace_id,id,1,revision,draft_graph,draft_sha256,created_by FROM workflows WHERE id=$2`, version, workflow)
		exec("UPDATE workflows SET current_version_id=$1 WHERE id=$2", version, workflow)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal("Scope fixture commit refused")
	}
	write := func(path string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("/evidence/person.json", map[string]any{"people": people, "second": second, "foreign": map[string]string{"workspace": state.ForeignWorkspace, "marker": state.Marker}})
	write("/evidence/scope-state.json", state)
	write("/evidence/schema-table-set-proof.json", combinedSchemaProof(t, pool))
	fmt.Println("INSTALLED_PORTAL_SCOPE_FIXTURE_READY")
}

func combinedSchemaProof(t *testing.T, pool *pgxpool.Pool) map[string]any {
	t.Helper()
	tx, err := pool.BeginTx(t.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(t.Context()))
	if err := database.CheckWithin(t.Context(), transaction.PostgreSQLTransaction(tx)); err != nil {
		t.Fatal("Installed lineage, catalog or grants differ from this source", err)
	}
	var raw string
	if err := tx.QueryRow(t.Context(), database.CatalogSQL).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Count  int                 `json:"table_count"`
		Tables [][]json.RawMessage `json:"tables"`
	}
	if json.Unmarshal([]byte(raw), &catalog) != nil || catalog.Count != database.TableCount {
		t.Fatal("Source catalog table count differs")
	}
	names, parents := []string{}, map[string]bool{}
	for _, row := range catalog.Tables {
		var name, kind string
		if len(row) < 2 || json.Unmarshal(row[0], &name) != nil || json.Unmarshal(row[1], &kind) != nil {
			t.Fatal("Invalid source catalog row")
		}
		if kind == "r" || kind == "p" {
			names = append(names, name)
		}
		if kind == "p" {
			parents[name] = true
		}
	}
	rows, err := tx.Query(t.Context(), `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p') AND NOT c.relispartition ORDER BY c.relname COLLATE "C"`)
	if err != nil {
		t.Fatal(err)
	}
	actual := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	sort.Strings(names)
	if len(names) != database.TableCount || strings.Join(names, "\n") != strings.Join(actual, "\n") {
		t.Fatal("Non-partition table names differ from the verified source catalog")
	}
	rows, err = tx.Query(t.Context(), `SELECT c.relname,p.relname,pg_get_expr(c.relpartbound,c.oid),c.relowner=p.relowner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_inherits i ON i.inhrelid=c.oid JOIN pg_class p ON p.oid=i.inhparent WHERE n.nspname='public' AND c.relispartition AND c.relkind='r' ORDER BY c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	type partition struct {
		Name   string `json:"name"`
		Parent string `json:"parent"`
		Bound  string `json:"bound"`
	}
	partitions := []partition{}
	byParent := map[string]map[string]string{}
	for rows.Next() {
		var item partition
		var sameOwner bool
		if err := rows.Scan(&item.Name, &item.Parent, &item.Bound, &sameOwner); err != nil || !sameOwner || !parents[item.Parent] {
			t.Fatal("Foreign or incorrectly owned partition", err)
		}
		partitions = append(partitions, item)
		if byParent[item.Parent] == nil {
			byParent[item.Parent] = map[string]string{}
		}
		byParent[item.Parent][item.Name] = item.Bound
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	upperMonth := regexp.MustCompile(`TO \('([0-9]{4}-[0-9]{2})-01 00:00:00`)
	for parent := range parents {
		children := byParent[parent]
		bound := children[parent+"_p000000"]
		match := upperMonth.FindStringSubmatch(bound)
		if len(children) != 4 || len(match) != 2 || !strings.Contains(bound, "MINVALUE") {
			t.Fatal("Fresh monthly partition layout differs", parent)
		}
		first, err := time.Parse("2006-01", match[1])
		if err != nil {
			t.Fatal(err)
		}
		for offset := range 3 {
			month := first.AddDate(0, offset, 0)
			value := children[parent+"_p"+month.Format("200601")]
			if !strings.Contains(value, "FROM ('"+month.Format("2006-01-02")) || !strings.Contains(value, "TO ('"+month.AddDate(0, 1, 0).Format("2006-01-02")) {
				t.Fatal("Monthly partition name or bounds differ", parent)
			}
		}
	}
	var version, migrations int
	if err := tx.QueryRow(t.Context(), "SELECT max(version),count(*) FROM schema_migrations").Scan(&version, &migrations); err != nil || version != database.SchemaVersion || migrations != database.SchemaVersion {
		t.Fatal("Installed migration count differs from this source", err)
	}
	rows, err = tx.Query(t.Context(), "SELECT version,checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	type migration struct {
		Version  int    `json:"version"`
		Checksum string `json:"checksum"`
	}
	ledger := []migration{}
	for rows.Next() {
		var item migration
		if err := rows.Scan(&item.Version, &item.Checksum); err != nil {
			t.Fatal(err)
		}
		ledger = append(ledger, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var site, cell string
	if err := tx.QueryRow(t.Context(), "SELECT site_id,id FROM cells WHERE is_local").Scan(&site, &cell); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal("Schema proof transaction did not complete", err)
	}
	return map[string]any{"source": os.Getenv("COMBINED_SOURCE_SHA"), "schema_version": version,
		"table_count": catalog.Count, "physical_public_table_count": len(names) + len(partitions),
		"catalog_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(raw))), "non_partition_table_names": names,
		"partition_children": partitions, "exact_non_partition_set_verified": true,
		"exact_partition_children_verified": true, "exact_migration_ledger_verified": true,
		"migration_ledger": ledger, "local_cell": map[string]string{"site_id": site, "cell_id": cell}}
}

func TestTemporaryInstalledScopeProof(t *testing.T) {
	pool := combinedFixtureDB(t)
	var state combinedScopeState
	raw, err := os.ReadFile("/evidence/scope-state.json")
	if err != nil || json.Unmarshal(raw, &state) != nil {
		t.Fatal("Scope fixture identity unavailable")
	}
	var proof struct {
		Refused []string `json:"refused_request_ids"`
	}
	raw, err = os.ReadFile("/evidence/scope-proof.json")
	if err != nil || json.Unmarshal(raw, &proof) != nil || len(proof.Refused) != 2 {
		t.Fatal("Two browser refusal correlations required")
	}
	for _, id := range proof.Refused {
		if uuid.Validate(id) != nil {
			t.Fatal("Invalid browser request UUID")
		}
	}
	var canary, memberships, success int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM workflows w JOIN workflow_versions v ON v.id=w.current_version_id AND v.workflow_id=w.id WHERE w.id=$1 AND w.tenant_id=$2 AND w.workspace_id=$3 AND w.name=$4 AND w.enabled AND v.version_number=1`, state.ForeignApplication, state.ForeignTenant, state.ForeignWorkspace, state.Marker).Scan(&canary); err != nil || canary != 1 {
		t.Fatal("Foreign projection changed", canary, err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM workspace_memberships WHERE user_id=ANY($1::uuid[]) AND (tenant_id=$2 OR workspace_id=$3)", state.Members, state.ForeignTenant, state.ForeignWorkspace).Scan(&memberships); err != nil || memberships != 0 {
		t.Fatal("Foreign membership created", memberships, err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM audit_events WHERE outcome IN ('success','accepted') AND (request_id=ANY($1::uuid[]) OR (actor_user_id=ANY($2::uuid[]) AND (tenant_id=$3 OR workspace_id=$4)))", proof.Refused, state.Members, state.ForeignTenant, state.ForeignWorkspace).Scan(&success); err != nil || success != 0 {
		t.Fatal("Refused request recorded success", success, err)
	}
	fmt.Printf("INSTALLED_SCOPE_DB_PROOF foreign_projection=%d foreign_memberships=%d refused_success_audits=%d request_ids=%v\n", canary, memberships, success, proof.Refused)
}
