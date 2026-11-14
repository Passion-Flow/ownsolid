package main

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestMySQLPrivilegedCommandsPreserveSelectedActualOwnerRouting(t *testing.T) {
	c := config.Config{}
	c.Database.Engine = config.MySQLDatabaseEngine
	c.Database.MySQLConfigurationFile = "/nonexistent/private/mysql.json"
	for _, command := range []string{"prepare-restored-runtime-access", "check-restored-runtime-access", "reconcile-restored-runtime-access", "resume-restored-runtime-access", "backup-mysql", "reconcile-mysql-backup", "restore-mysql", "reconcile-mysql-restore", "initialize-configured-model-store", "check-configured-model-store-initialization", "record-configured-model-store-initialization", "initialize-dataset-store", "check-training-store-initialization", "record-preparation-store-initialization", "migrate-with-runtime-context", "prepare-runtime-access", "check-runtime-access", "reconcile-runtime-access", "resume-runtime-access", "prepare-mysql-bootstrap-packet", "apply-bootstrap-packet", "check-bootstrap-packet", "migrate", "check-schema", "verify-mysql-installation", "reconcile-mysql-migrations", "serve", "bootstrap-admin", "bootstrap-admin-file", "reconcile-bootstrap-file", "resume-bootstrap-file", "invalidate-restored-auth", "reconcile-inference-removal"} {
		if e := validateSelectedCommand(c, command); e != nil {
			t.Fatal("supported source selection refused", command, e)
		}
	}
	c.Database.Engine = config.PostgreSQLDatabaseEngine
	c.Database.MySQLConfigurationFile = ""
	for _, command := range []string{"backup-mysql", "reconcile-mysql-backup", "restore-mysql", "reconcile-mysql-restore", "check-restored-runtime-access", "reconcile-restored-runtime-access", "resume-restored-runtime-access"} {
		if validateSelectedCommand(c, command) == nil {
			t.Fatal("MySQL-only actual owner selected on PostgreSQL", command)
		}
	}
	if validateSelectedCommand(c, "check-configured-model-store-initialization") == nil || validateSelectedCommand(c, "record-configured-model-store-initialization") == nil || validateSelectedCommand(c, "verify-mysql-installation") == nil || validateSelectedCommand(c, "reconcile-mysql-migrations") == nil || validateSelectedCommand(c, "reconcile-runtime-access") == nil || validateSelectedCommand(c, "resume-runtime-access") == nil || validateSelectedCommand(c, "prepare-mysql-bootstrap-packet") == nil {
		t.Fatal("MySQL operator accepted on PG")
	}
}

func TestCompleteSchemaCapabilityCannotBeSelectedByConfiguration(t *testing.T) {
	for _, steps := range []int{0, 149, 193, 194, 202, 203, 204, 264, 266} {
		if requireCompleteMySQLSchema(steps) == nil {
			t.Fatal("incomplete/foreign schema accepted", steps)
		}
	}
	if e := requireCompleteMySQLSchema(265); e != nil {
		t.Fatal(e)
	}
}

func TestRealBootstrapStateAndConfiguredFeaturesRemainDistinct(t *testing.T) {
	null := sql.NullString{}
	tenant := sql.NullString{String: "synthetic-tenant", Valid: true}
	active := sql.NullString{String: "active", Valid: true}
	if validInstallationState(false, null, null) != nil || validInstallationState(true, tenant, active) != nil {
		t.Fatal("valid declared state refused")
	}
	for _, c := range []struct {
		complete       bool
		tenant, status sql.NullString
	}{{false, tenant, null}, {false, null, active}, {true, null, null}, {true, tenant, sql.NullString{String: "disabled", Valid: true}}, {true, tenant, null}} {
		if validInstallationState(c.complete, c.tenant, c.status) == nil {
			t.Fatal("malformed state accepted")
		}
	}
	c := config.Config{}
	if e := validateUnbootstrappedFeatures(c); e != nil {
		t.Fatal(e)
	}
	c.Knowledge.Enabled = true
	if e := validateUnbootstrappedFeatures(c); e == nil || e.Error() != "BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES" {
		t.Fatal("configured processor silently disabled", e)
	}
}

func TestBootstrapAuthorityGraphCannotRetainPartialState(t *testing.T) {
	null := sql.NullString{}
	id := sql.NullString{String: "synthetic-authority", Valid: true}
	at := sql.NullTime{Time: time.Now(), Valid: true}
	if validInstallationAuthorities(false, null, null, sql.NullTime{}, false) != nil || validInstallationAuthorities(true, id, id, at, true) != nil {
		t.Fatal("valid authority graph refused")
	}
	for _, c := range []struct {
		complete         bool
		workspace, admin sql.NullString
		at               sql.NullTime
		graph            bool
	}{{false, id, null, sql.NullTime{}, false}, {false, null, id, sql.NullTime{}, false}, {false, null, null, at, false}, {false, null, null, sql.NullTime{}, true}, {true, null, id, at, true}, {true, id, null, at, true}, {true, id, id, sql.NullTime{}, true}, {true, id, id, at, false}} {
		if validInstallationAuthorities(c.complete, c.workspace, c.admin, c.at, c.graph) == nil {
			t.Fatal("partial/dangling bootstrap graph accepted")
		}
	}
}
