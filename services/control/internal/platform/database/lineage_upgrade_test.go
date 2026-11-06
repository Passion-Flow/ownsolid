package database_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
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

// Migration 000034 keeps a theme the user chose and moves seeded rows, which nobody chose,
// to following the operating system.
func TestLineageUpgradeKeepsChosenThemesOnly(t *testing.T) {
	s := fixture.NewAtVersion(t, 33) // before 000034
	fixture.Bootstrap(t, s)
	ctx := t.Context()
	var seeded string
	if e := s.DB.QueryRow(ctx, "SELECT user_id::text FROM user_preferences LIMIT 1").Scan(&seeded); e != nil {
		t.Fatal(e)
	}
	chosen := uuid.NewString()
	if _, e := s.DB.Exec(ctx, "INSERT INTO users(id,email_canonical,email_display,display_name,locale,status) VALUES($1,'chosen@example.invalid','chosen@example.invalid','Chosen','en','active')", chosen); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, "UPDATE user_preferences SET theme='dark',revision=2 WHERE user_id=$1", chosen); e != nil {
		t.Fatal(e)
	}
	if e := database.Migrate(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	for user, want := range map[string]string{seeded: "system", chosen: "dark"} {
		var theme, density string
		var revision int64
		if e := s.DB.QueryRow(ctx, "SELECT theme,density,revision FROM user_preferences WHERE user_id=$1", user).Scan(&theme, &density, &revision); e != nil {
			t.Fatal(e)
		}
		if theme != want || density != "comfortable" || revision != 2 {
			t.Fatal(want, theme, density, revision)
		}
	}
}


// Migration 000035 counts the live nodes that already exist; removed nodes do not count.
func TestLineageUpgradeCountsLiveNodes(t *testing.T) {
	s := fixture.NewAtVersion(t, 34) // before 000035
	fixture.Bootstrap(t, s)
	ctx := t.Context()
	seed := newTaskSeed(t, s)
	seed.node("Live")
	seed.node("Also live")
	removed := seed.node("Removed")
	if _, e := s.DB.Exec(ctx, "UPDATE managed_nodes SET status='removed',credential_hash=NULL WHERE id=$1", removed); e != nil {
		t.Fatal(e)
	}
	if e := database.Migrate(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	var live int
	if e := s.DB.QueryRow(ctx, "SELECT live_nodes FROM tenant_node_counts").Scan(&live); e != nil || live != 2 {
		t.Fatal(live, e)
	}
}

// Migration 000031 numbers the tasks that already exist. They ran under the old rules, so
// they count as delivered, and the first task of the new rules continues the sequence.
func TestLineageUpgradeNumbersExistingTasks(t *testing.T) {
	s := fixture.NewAtVersion(t, 30)
	fixture.Bootstrap(t, s)
	ctx := t.Context()
	seed := newTaskSeed(t, s)
	node, task := seed.node, seed.task
	busy, waiting, idle := node("Busy"), node("Waiting"), node("Idle")
	// Created out of order on purpose: the sequence follows created_at, not insertion.
	last := task(busy, "running", 2, 1)
	first := task(busy, "succeeded", 1, 3)
	middle := task(busy, "failed", 3, 2)
	pending := task(waiting, "pending", 0, 1)
	if e := database.Migrate(ctx, s.DB); e != nil {
		t.Fatal(e)
	}
	for id, want := range map[string][3]int{first: {1, 1, 1}, middle: {2, 3, 3}, last: {3, 2, 2}, pending: {1, 0, 0}} {
		var got [3]int
		if e := s.DB.QueryRow(ctx, "SELECT sequence,acknowledged_attempt,deliveries FROM node_tasks WHERE id=$1", id).Scan(&got[0], &got[1], &got[2]); e != nil || got != want {
			t.Fatal("task after upgrade", got, want, e)
		}
	}
	for id, want := range map[string][2]int{busy: {3, 3}, waiting: {1, 1}, idle: {0, 0}} {
		var got [2]int
		if e := s.DB.QueryRow(ctx, "SELECT task_sequence,task_acknowledged_sequence FROM managed_nodes WHERE id=$1", id).Scan(&got[0], &got[1]); e != nil || got != want {
			t.Fatal("node after upgrade", got, want, e)
		}
	}
	next := task(idle, "pending", 0, 0)
	var sequence int
	if e := s.DB.QueryRow(ctx, "SELECT sequence FROM node_tasks WHERE id=$1", next).Scan(&sequence); e != nil || sequence != 1 {
		t.Fatal("first new task", sequence, e)
	}
}

// A lease that was never acknowledged reached no executing agent, so delivering its task
// again may bind a new stop authority. Once the task is acknowledged, or the attempt has
// moved on, the stop authority is immutable as before (ADR 0011).
func TestStopAuthorityRebindsOnlyWhileTheTaskIsUnacknowledged(t *testing.T) {
	for _, c := range []struct{ table, insert, refusal string }{
		{"training_attempts", `INSERT INTO training_attempts(id,tenant_id,workspace_id,job_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,input_inventory,stop_token_hash) VALUES($1,$2,$3,gen_random_uuid(),$4,$5,1,'preparing',$6,'admin',1,'{}',sha256('first'))`, "TRAINING_ATTEMPT_INPUTS_IMMUTABLE"},
		{"model_preparation_attempts", `INSERT INTO model_preparation_attempts(id,tenant_id,workspace_id,preparation_id,node_id,task_id,attempt_number,state,requested_by,requested_audience,requested_epoch,command_sha256,stop_token_hash) VALUES($1,$2,$3,gen_random_uuid(),$4,$5,1,'preparing',$6,'admin',1,repeat('a',64),sha256('first'))`, "MODEL_PREPARATION_STOP_AUTHORITY_IMMUTABLE"},
	} {
		t.Run(c.table, func(t *testing.T) {
			s := fixture.New(t)
			fixture.Bootstrap(t, s)
			ctx := t.Context()
			seed := newTaskSeed(t, s)
			// The fixture schema is disposable. Without foreign keys the attempt needs no
			// job or preparation graph, and the trigger under test stays in place.
			var keys []string
			rows, e := s.DB.Query(ctx, "SELECT conname FROM pg_constraint WHERE conrelid=$1::regclass AND contype='f'", c.table)
			if e != nil {
				t.Fatal(e)
			}
			if keys, e = pgx.CollectRows(rows, pgx.RowTo[string]); e != nil {
				t.Fatal(e)
			}
			for _, key := range keys {
				if _, e = s.DB.Exec(ctx, "ALTER TABLE "+c.table+" DROP CONSTRAINT "+key); e != nil {
					t.Fatal(e)
				}
			}
			node := seed.node("Stop authority")
			task := seed.task(node, "running", 1, 0)
			attempt := uuid.NewString()
			if _, e = s.DB.Exec(ctx, c.insert, attempt, seed.tenant, seed.workspace, node, task, seed.user); e != nil {
				t.Fatal(e)
			}
			change := func(set string) error {
				_, e := s.DB.Exec(ctx, "UPDATE "+c.table+" SET "+set+" WHERE id=$1", attempt)
				return e
			}
			refused := func(e error) {
				t.Helper()
				if e == nil || !strings.Contains(e.Error(), c.refusal) {
					t.Fatal("stop authority changed", e)
				}
			}
			if e = change("stop_token_hash=sha256('second')"); e != nil {
				t.Fatal("rebind while unacknowledged", e)
			}
			refused(change("stop_token_hash=NULL"))
			refused(change("state='running',stop_token_hash=sha256('third')"))
			if _, e = s.DB.Exec(ctx, "UPDATE node_tasks SET acknowledged_attempt=1 WHERE id=$1", task); e != nil {
				t.Fatal(e)
			}
			refused(change("stop_token_hash=sha256('fourth')"))
		})
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

// taskSeed writes nodes and tasks with plain SQL, so it works on any schema version.
type taskSeed struct {
	t                       *testing.T
	db                      *pgxpool.Pool
	tenant, workspace, user string
}

func newTaskSeed(t *testing.T, s *identity.Service) taskSeed {
	t.Helper()
	seed := taskSeed{t: t, db: s.DB}
	if e := s.DB.QueryRow(t.Context(), "SELECT i.tenant_id::text,i.default_workspace_id::text,(SELECT id::text FROM users LIMIT 1) FROM installation_state i WHERE i.id=1").Scan(&seed.tenant, &seed.workspace, &seed.user); e != nil {
		t.Fatal(e)
	}
	return seed
}

func (s taskSeed) node(name string) string {
	s.t.Helper()
	id := uuid.NewString()
	if _, e := s.db.Exec(s.t.Context(), `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES($1,$2,$3,$4,'active',sha256($5::bytea),'1','{}',clock_timestamp())`, id, s.tenant, s.workspace, name, []byte(id)); e != nil {
		s.t.Fatal(e)
	}
	return id
}

func (s taskSeed) task(node, state string, attempt, age int) string {
	s.t.Helper()
	id := uuid.NewString()
	if _, e := s.db.Exec(s.t.Context(), `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,attempt,lease_token_hash,lease_expires_at,created_at,deadline_at,completed_at)
 VALUES($1,$2,$3,$4,'collect_facts.v1',$5::varchar,$6,$7,$8,CASE WHEN $5::varchar='running' THEN sha256($9::bytea) END,CASE WHEN $5::varchar='running' THEN clock_timestamp()+interval '1 minute' END,clock_timestamp()-make_interval(hours=>$10),clock_timestamp()+interval '1 hour',CASE WHEN $5::varchar IN ('succeeded','failed') THEN clock_timestamp() END)`,
		id, s.tenant, s.workspace, node, state, uuid.NewString(), s.user, attempt, []byte(id), age); e != nil {
		s.t.Fatal(e)
	}
	return id
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
