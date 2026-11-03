//go:build repository_integration

package databaseaccess

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// This read-only control uses the synthetic fixture's ordinary database account,
// which has no mysql-schema SELECT. It creates no account, grant or business row.
func TestActualMySQLRuntimePrivilegeVisibilityRequired(t *testing.T) {
	if os.Getenv("PROJECT_ONE_REPOSITORY_ENGINE") != "mysql" {
		t.Skip("explicit isolated MySQL fixture required")
	}
	configuration, err := mysqlstore.Load(os.Getenv("PROJECT_ONE_REPOSITORY_MYSQL_CONFIGURATION"), "")
	if err != nil {
		t.Fatal("actual protected fixture configuration unavailable")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	kernel, _, err := mysqlstore.Open(ctx, configuration)
	if err != nil {
		t.Fatal("actual TLS fixture unavailable")
	}
	defer func() {
		if kernel.Close() != nil {
			t.Error("actual fixture pool close failed")
		}
	}()
	connection, err := kernel.Pool().Conn(ctx)
	if err != nil {
		t.Fatal("actual fixture connection unavailable")
	}
	closed := false
	defer func() {
		if !closed && connection.Close() != nil {
			t.Error("actual fixture connection close failed")
		}
	}()
	if err = mysqlRequirePrivilegeVisibility(ctx, connection); err == nil || err.Error() != "MYSQL_RUNTIME_PRIVILEGE_VISIBILITY_REQUIRED" {
		t.Fatal("incomplete visibility did not return the exact refusal")
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	if err = mysqlRequirePrivilegeVisibility(canceled, connection); err == nil || err.Error() != "MYSQL_RUNTIME_PRIVILEGE_VISIBILITY_REQUIRED" {
		t.Fatal("canceled visibility read did not fail closed")
	}
	if connection.Close() != nil {
		t.Fatal("actual fixture connection close failed")
	}
	closed = true
	if err = mysqlRequirePrivilegeVisibility(ctx, connection); err == nil || err.Error() != "MYSQL_RUNTIME_PRIVILEGE_VISIBILITY_REQUIRED" {
		t.Fatal("closed visibility connection did not fail closed")
	}
	t.Log("ACTUAL_MYSQL_NARROW_OPERATOR_VISIBILITY_REFUSED")
}
