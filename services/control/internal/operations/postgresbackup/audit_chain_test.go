package postgresbackup

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
)

func TestAuditChainRestoreReadbackExcludesOnlyReceipt(t *testing.T) {
	dsn := os.Getenv("PROJECT_ONE_TEST_DSN")
	if dsn == "" {
		t.Skip("PROJECT_ONE_TEST_DSN is required for real PostgreSQL integration")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || config.Host != "127.0.0.1" || !strings.HasSuffix(config.Database, "_test") {
		t.Fatal("an isolated loopback test database is required")
	}
	ctx := t.Context()
	operator, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close(context.WithoutCancel(ctx))
	config.Database = "audit_restore_" + strings.ReplaceAll(uuid.NewString(), "-", "") + "_test"
	databaseName := pgx.Identifier{config.Database}.Sanitize()
	if _, err := operator.Exec(ctx, "CREATE DATABASE "+databaseName); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := operator.Exec(cleanup, "DROP DATABASE "+databaseName+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	db, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.WithoutCancel(ctx))
	if _, err := db.Exec(ctx, "CREATE EXTENSION vector"); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	insert := func(id string) {
		t.Helper()
		_, err := tx.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,audience,action,outcome,request_id)
		    VALUES($1,clock_timestamp(),'system','fixture.restore','success',$1)`, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	var receiptID string
	for range 2 {
		receiptID = uuid.NewString()
		before, err := readDatabaseContents(ctx, tx, policyFixture(), receiptID)
		if err != nil {
			t.Fatal(err)
		}
		insert(receiptID)
		after, err := readDatabaseContents(ctx, tx, policyFixture(), receiptID)
		if err != nil || after != before {
			t.Fatalf("restore receipt exclusion changed the backup comparison: %v", err)
		}
	}
	insert(uuid.NewString())
	if _, err := readDatabaseContents(ctx, tx, policyFixture(), receiptID); err == nil || err.Error() != "POSTGRES_RESTORE_AUDIT_HEAD_MISMATCH" {
		t.Fatalf("a receipt followed by a later audit event was accepted: %v", err)
	}
}
