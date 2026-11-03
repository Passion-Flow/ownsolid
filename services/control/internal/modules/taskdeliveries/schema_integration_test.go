//go:build repository_integration && task_events_schema_integration

package taskdeliveries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func TestActualTaskEventsPostgreSQLSchema(t *testing.T) {
	f := repositoryfixture.New(t)
	if f.PostgreSQL == nil {
		t.Fatal("TASK_EVENTS_REAL_POSTGRES_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	journal := func() string {
		t.Helper()
		var value string
		if e := f.PostgreSQL.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(j) ORDER BY version)::text FROM schema_migrations j WHERE version<=24`).Scan(&value); e != nil {
			t.Fatal(e)
		}
		return value
	}
	original := journal()
	verify := func() error { return f.Store.Within(ctx, true, verifySchemaWithin) }
	down, e := migrations.Files.ReadFile("postgresql/000025_task_events.down.sql")
	if e != nil {
		t.Fatal(e)
	}
	if !t.Run("catalog_and_guards", func(t *testing.T) {
		up, e := migrations.Files.ReadFile("postgresql/000025_task_events.up.sql")
		if e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(up)
		var count, latest, tables int
		var hash string
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*),max(version) FROM schema_migrations`).Scan(&count, &latest); e != nil || count != 25 || latest != 25 {
			t.Fatal(count, latest, e)
		}
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=25`).Scan(&hash); e != nil || hash != hex.EncodeToString(sum[:]) {
			t.Fatal("actual migration checksum", e)
		}
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname=current_schema() AND tablename IN('task_event_outbox','task_event_receipts','task_event_dead_letters')`).Scan(&tables); e != nil || tables != 3 {
			t.Fatal(tables, e)
		}
		if e = verify(); e != nil {
			t.Fatal("actual owner guard reader", e)
		}
		if e = database.Migrate(ctx, f.PostgreSQL); e != nil || journal() != original {
			t.Fatal("replay modified historical journal", e)
		}
	}) {
		return
	}
	if !t.Run("guard_replacement_refused", func(t *testing.T) {
		tx, e := f.PostgreSQL.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(context.Background())
		if _, e = tx.Exec(ctx, `CREATE OR REPLACE FUNCTION task_event_outbox_guard() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$`); e != nil {
			t.Fatal(e)
		}
		q := transaction.PostgreSQLTransaction(tx)
		if e = verifySchemaWithin(ctx, q); e == nil || e.Error() != "TASK_BUS_SCHEMA_REJECTED" {
			t.Fatal("changed guard admitted", e)
		}
		if e = tx.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
		if e = verify(); e != nil {
			t.Fatal("original guard did not recover", e)
		}
	}) {
		return
	}
	if !t.Run("empty_rollback_reapply", func(t *testing.T) {
		tx, e := f.PostgreSQL.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(context.Background())
		if _, e = tx.Exec(ctx, string(down)); e != nil {
			t.Fatal(e)
		}
		if tag, e := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=25`); e != nil || tag.RowsAffected() != 1 {
			t.Fatal(e)
		}
		if e = tx.Commit(ctx); e != nil {
			t.Fatal(e)
		}
		if e = database.Migrate(ctx, f.PostgreSQL); e != nil || journal() != original {
			t.Fatal("actual reapply", e)
		}
		if e = database.Check(ctx, f.PostgreSQL); e != nil {
			t.Fatal(e)
		}
		if e = verify(); e != nil {
			t.Fatal(e)
		}
	}) {
		return
	}
	var event taskbus.Event
	var raw []byte
	if !t.Run("canonical_identity_and_forgery_refusal", func(t *testing.T) {
		node, task := uuid.NewString(), uuid.NewString()
		if _, e = f.PostgreSQL.Exec(ctx, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at) VALUES($1,$2,$3,'Synthetic schema node','active',$4,'1.4','{}',clock_timestamp())`, node, f.Tenant, f.Workspace, make([]byte, 32)); e != nil {
			t.Fatal(e)
		}
		var taskTime, now time.Time
		if e = f.PostgreSQL.QueryRow(ctx, `INSERT INTO node_tasks(id,tenant_id,workspace_id,node_id,operation,state,idempotency_key,requested_by,deadline_at) VALUES($1,$2,$3,$4,'collect_facts.v1','pending',$5,$6,clock_timestamp()+interval '5 minutes') RETURNING created_at`, task, f.Tenant, f.Workspace, node, uuid.NewString(), f.User).Scan(&taskTime); e != nil {
			t.Fatal(e)
		}
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); e != nil {
			t.Fatal(e)
		}
		event = taskbus.Event{Schema: taskbus.EventSchema, ID: uuid.NewString(), NamespaceID: uuid.NewString(), TenantID: f.Tenant, WorkspaceID: f.Workspace, NodeID: node, TaskID: task, Kind: taskbus.NodeTaskQueued, Operation: "collect_facts.v1", TaskCreatedAt: taskTime.UTC(), CreatedAt: now.UTC()}
		raw, e = event.Canonical()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.PostgreSQL.Exec(ctx, `INSERT INTO task_event_outbox(id,tenant_id,workspace_id,node_id,task_id,namespace_id,event_kind,operation,event_bytes,logical_sha256,delivery_id,generation,state,broker_identity,consumer_name,not_before,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,1,'prepared',$12,'SchemaReceiver',$13,$14,$13,$13)`, event.ID, f.Tenant, f.Workspace, node, task, event.NamespaceID, event.Kind, event.Operation, raw, digest(raw), uuid.NewString(), strings.Repeat("a", 64), now, now.Add(time.Minute)); e != nil {
			t.Fatal("actual canonical event insert", e)
		}
		for _, query := range []string{`UPDATE task_event_outbox SET namespace_id='11111111-1111-4111-8111-111111111111' WHERE id=$1`, `UPDATE task_event_outbox SET event_bytes='{}'::bytea WHERE id=$1`, `INSERT INTO task_event_receipts(id,event_id,tenant_id,workspace_id,node_id,task_id,logical_sha256,delivery_id,generation,consumer_name,broker_identity,broker_sequence,consumer_sequence,outcome,task_state,created_at) SELECT id,id,tenant_id,workspace_id,node_id,task_id,logical_sha256,delivery_id,generation,consumer_name,broker_identity,1,1,'received','pending',created_at FROM task_event_outbox WHERE id=$1`} {
			_, e = f.PostgreSQL.Exec(ctx, query, event.ID)
			var denied *pgconn.PgError
			if !errors.As(e, &denied) || denied.Code != "P0001" {
				t.Fatal("actual immutable/receipt guard refusal required", e)
			}
		}
		var saved []byte
		var receipts int
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT event_bytes FROM task_event_outbox WHERE id=$1`, event.ID).Scan(&saved); e != nil || string(saved) != string(raw) {
			t.Fatal("guard refusal changed canonical bytes", e)
		}
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM task_event_receipts`).Scan(&receipts); e != nil || receipts != 0 {
			t.Fatal("forged receipt escaped", e)
		}
	}) {
		return
	}
	t.Run("populated_rollback_refused", func(t *testing.T) {
		tx, e := f.PostgreSQL.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback(context.Background())
		_, e = tx.Exec(ctx, string(down))
		var denied *pgconn.PgError
		if !errors.As(e, &denied) || denied.Message != "TASK_EVENT_ROLLBACK_REQUIRES_EMPTY_STATE" {
			t.Fatal("populated down did not fail closed", e)
		}
		if e = tx.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
		var count int
		if e = f.PostgreSQL.QueryRow(ctx, `SELECT count(*) FROM task_event_outbox WHERE id=$1`, event.ID).Scan(&count); e != nil || count != 1 || journal() != original {
			t.Fatal("refused down lost state", e)
		}
		if e = verify(); e != nil {
			t.Fatal("refused down lost guards", e)
		}
	})
}
