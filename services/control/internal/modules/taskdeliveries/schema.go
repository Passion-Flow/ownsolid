package taskdeliveries

import (
	"context"
	_ "embed"
	"strings"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// These sources retain the single owner contract. PostgreSQL migration25 is
// active; MySQL activation remains pending its measured catalog. Runtime only
// verifies installed guards; it never creates or upgrades schema.
//
//go:embed schema/postgresql.sql
var postgresqlTables string

//go:embed schema/postgresql-guards.sql
var postgresqlGuards string

//go:embed schema/mysql.sql
var mysqlTables string

//go:embed schema/mysql-guards.sql
var mysqlGuards string

type expectedGuard struct{ table, name, operation, body string }

func expectedGuards(d transaction.Dialect) ([]expectedGuard, error) {
	if d == transaction.PostgreSQL {
		out := []expectedGuard{}
		for _, v := range [][2]string{{"task_event_outbox", "task_event_outbox_guard"}, {"task_event_receipts", "task_event_receipt_guard"}, {"task_event_dead_letters", "task_event_dead_letter_guard"}} {
			prefix := "CREATE FUNCTION " + v[1] + "() RETURNS trigger LANGUAGE plpgsql AS $$"
			_, tail, ok := strings.Cut(postgresqlGuards, prefix)
			if !ok {
				return nil, fail("TASK_BUS_SCHEMA_REJECTED")
			}
			body, _, ok := strings.Cut(tail, "$$;")
			if !ok {
				return nil, fail("TASK_BUS_SCHEMA_REJECTED")
			}
			out = append(out, expectedGuard{table: v[0], name: v[1], body: body})
		}
		return out, nil
	}
	if d != transaction.MySQL {
		return nil, fail("TASK_BUS_SCHEMA_REJECTED")
	}
	out := []expectedGuard{}
	for _, v := range [][2]string{{"task_event_outbox", "task_event_outbox"}, {"task_event_receipts", "task_event_receipt"}, {"task_event_dead_letters", "task_event_dead_letter"}} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			name := v[1] + "_" + strings.ToLower(op)
			prefix := "CREATE TRIGGER " + name + " BEFORE " + op + " ON " + v[0] + " FOR EACH ROW\n"
			_, tail, ok := strings.Cut(mysqlGuards, prefix)
			if !ok {
				return nil, fail("TASK_BUS_SCHEMA_REJECTED")
			}
			body, _, ok := strings.Cut(tail, "$$")
			if !ok {
				return nil, fail("TASK_BUS_SCHEMA_REJECTED")
			}
			out = append(out, expectedGuard{table: v[0], name: name, operation: op, body: body})
		}
	}
	return out, nil
}
func normalizedGuard(body string) string { return strings.Join(strings.Fields(body), " ") }
func verifySchemaWithin(ctx context.Context, q transaction.Executor) error {
	guards, err := expectedGuards(q.Dialect())
	if err != nil {
		return err
	}
	var count int
	err = q.QueryRow(ctx, statement(q, `SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE NOT t.tgisinternal AND n.nspname=current_schema() AND c.relname IN('task_event_outbox','task_event_receipts','task_event_dead_letters')`,
		`SELECT count(*) FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND event_object_table IN('task_event_outbox','task_event_receipts','task_event_dead_letters')`)).Scan(&count)
	if err != nil {
		return err
	}
	if count != len(guards) {
		return fail("TASK_BUS_SCHEMA_REJECTED")
	}
	for _, g := range guards {
		var body string
		if q.Dialect() == transaction.PostgreSQL {
			var enabled, language string
			var kind int
			var definer bool
			err = q.QueryRow(ctx, statement(q, `SELECT p.prosrc,t.tgenabled,t.tgtype,p.prosecdef,l.lanname FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace pn ON pn.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE NOT t.tgisinternal AND n.nspname=current_schema() AND pn.nspname=n.nspname AND c.relname=$1 AND t.tgname=$2 AND p.proname=$2 AND t.tgqual IS NULL`, "", g.table, g.name)).Scan(&body, &enabled, &kind, &definer, &language)
			if err != nil {
				return err
			}
			if enabled != "O" || kind != 31 || definer || language != "plpgsql" {
				return fail("TASK_BUS_SCHEMA_REJECTED")
			}
		} else {
			var timing, operation, orientation, mode string
			err = q.QueryRow(ctx, statement(q, "", `SELECT action_statement,action_timing,event_manipulation,action_orientation,sql_mode FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND event_object_table=? AND trigger_name=?`, g.table, g.name)).Scan(&body, &timing, &operation, &orientation, &mode)
			if err != nil {
				return err
			}
			if timing != "BEFORE" || operation != g.operation || orientation != "ROW" || !strictTriggerMode(mode) {
				return fail("TASK_BUS_SCHEMA_REJECTED")
			}
		}
		if normalizedGuard(body) != normalizedGuard(g.body) {
			return fail("TASK_BUS_SCHEMA_REJECTED")
		}
	}
	return nil
}
func strictTriggerMode(mode string) bool {
	for _, v := range strings.Split(mode, ",") {
		if v == "STRICT_TRANS_TABLES" || v == "STRICT_ALL_TABLES" {
			return true
		}
	}
	return false
}
