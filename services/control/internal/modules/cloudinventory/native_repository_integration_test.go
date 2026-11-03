//go:build cgo && odbc_manager && aws_selected_integration

package cloudinventory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/damengserving"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This target test borrows the actual restricted serving owner, including its
// complete native catalog, grants, principal, identity and installation lease.
// It creates only synthetic rows in a separately provisioned disposable schema;
// the migration owner retains DDL and schema cleanup/recovery responsibility.
func TestAWSActualNativeRepositorySQL(t *testing.T) {
	// Protected native runtime input; required, no default, read only by loader.
	runtimeFile := os.Getenv("PROJECT_ONE_AWS_NATIVE_RUNTIME_FILE")
	// Protected schema owner policy reference; required, no literal credentials.
	schemaFile := os.Getenv("PROJECT_ONE_AWS_NATIVE_SCHEMA_FILE")
	// Exact disposable P1_AWS_TEST_ schema; required, compared with typed policy.
	disposable := os.Getenv("PROJECT_ONE_AWS_NATIVE_DISPOSABLE_SCHEMA")
	schema, e := dameng.LoadSchemaConfiguration(schemaFile, "")
	if runtimeFile == "" || e != nil || !strings.HasPrefix(disposable, "P1_AWS_TEST_") || schema.Schema != disposable || schema.RuntimeUser == schema.Schema {
		t.Fatal("explicit isolated native runtime/schema policy required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	owner, e := damengserving.OpenProtected(ctx, runtimeFile, schemaFile, 2)
	if owner != nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(owner.CleanupSeconds())*time.Second)
			defer cancel()
			if e := owner.Close(ctx); e != nil {
				t.Error("native owner cleanup incomplete; retain schema/recovery state", e)
			}
		})
	}
	if e != nil {
		t.Fatal("actual native catalog/runtime-role admission", e)
	}
	store := owner.Repository()
	for _, table := range []string{"TENANTS", "WORKSPACES", "USERS", "AWS_DISCOVERED_RESOURCES", "AWS_IMPORT_RECEIPTS", "AUDIT_EVENTS"} {
		var n int
		if e := store.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM "+table)).Scan(&n); e != nil || n != 0 {
			t.Fatal("empty disposable native scope required", table, e)
		}
	}
	tenant, workspace, actor := uuid.NewString(), uuid.NewString(), uuid.NewString()
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		for _, s := range []transaction.Statement{
			transaction.SQL(transaction.Dameng, `INSERT INTO TENANTS(ID,NAME,STATUS) VALUES(?,?,'active')`, tenant, "Synthetic AWS tenant"),
			transaction.SQL(transaction.Dameng, `INSERT INTO WORKSPACES(ID,TENANT_ID,NAME,STATUS) VALUES(?,?,?,'active')`, workspace, tenant, "Synthetic AWS Workspace"),
			transaction.SQL(transaction.Dameng, `INSERT INTO USERS(ID,EMAIL_CANONICAL,EMAIL_DISPLAY,DISPLAY_NAME,LOCALE,STATUS,IS_PLATFORM_ADMIN) VALUES(?,?,?,?,'en','active',1)`, actor, "aws@example.invalid", "aws@example.invalid", "Synthetic AWS actor"),
		} {
			if _, e := q.Exec(ctx, s); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal("actual native synthetic seed", e)
	}
	assertActualAWSRepositorySQL(t, store, tenant, workspace, actor)
	if e = owner.Verify(ctx); e != nil {
		t.Fatal("actual native post-operation catalog/role/identity check", e)
	}
}
