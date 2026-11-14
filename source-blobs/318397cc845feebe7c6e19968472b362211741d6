//go:build cgo && odbc_manager

package damengbackup

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng/migrate"
)

type MaintenanceMode string

const (
	SourceMaintenance       MaintenanceMode = "source"
	FreshRestoreMaintenance MaintenanceMode = "fresh_restore"
)

// NativeCustody owns no kernel or pool. It is valid only during WithNativeCustody
// and retains that Store's exact pinned physical session and original DBMS lock.
type NativeCustody struct {
	native                                   *Native
	kernel                                   *dameng.Store
	connector                                dameng.ConnectionConfiguration
	schema                                   dameng.SchemaConfiguration
	policy                                   SnapshotPolicy
	connection                               *sql.Conn
	query                                    nativeQuery
	transaction                              *sql.Tx
	active                                   atomic.Bool
	mode                                     MaintenanceMode
	lockID, session                          int64
	lockMode                                 string
	serverID, version, endpoint, freshMarker string
	plan                                     []migrate.PlannedStep
	tables                                   []string
	installation                             string
}

func (*NativeCustody) String() string   { return "DAMENG_RECOVERY_CUSTODY_REDACTED" }
func (*NativeCustody) GoString() string { return "DAMENG_RECOVERY_CUSTODY_REDACTED" }

const nativeSessionIdentitySQL = `SELECT SESSID(),CUR_DATABASE(),PERMANENT_MAGIC(),SYS_CONTEXT('USERENV','SESSION_USER'),SYS_CONTEXT('USERENV','CURRENT_SCHEMA'),SESSIONTIMEZONE,SYS_CONTEXT('USERENV','ISDBA'),SYS_CONTEXT('USERENV','NETWORK_PROTOCOL'),(SELECT SVR_VERSION FROM V$INSTANCE) FROM DUAL`
const nativeHeldSQL = `SELECT COUNT(DISTINCT MODE),MIN(MODE),COUNT(DISTINCT OWN_SESS),MIN(OWN_SESS) FROM V$DBMS_LOCKS WHERE ID=? AND OWN_SESS=SESSID()`
const nativeInstallationSQL = `SELECT I.TENANT_ID FROM "INSTALLATION_STATE" I JOIN "TENANTS" T ON T.ID=I.TENANT_ID JOIN "WORKSPACES" W ON W.ID=I.DEFAULT_WORKSPACE_ID AND W.TENANT_ID=T.ID JOIN "USERS" U ON U.ID=I.PRIMARY_ADMIN_USER_ID JOIN "WORKSPACE_MEMBERSHIPS" M ON M.USER_ID=U.ID AND M.WORKSPACE_ID=W.ID AND M.TENANT_ID=T.ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND I.BOOTSTRAPPED_AT IS NOT NULL AND T.STATUS='active' AND W.STATUS='active' AND U.STATUS='active' AND U.IS_PLATFORM_ADMIN=1 AND M.ROLE='owner' AND (SELECT COUNT(*) FROM "INSTALLATION_STATE")=1`

func (c *NativeCustody) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(c.schema.StepSeconds)*time.Second)
}
func (c *NativeCustody) sessionIdentity(ctx context.Context) (int64, string, string, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	var sid, magic int64
	var database, user, schema, zone, dba, protocol, version string
	if c.query.QueryRowContext(ctx, nativeSessionIdentitySQL).Scan(&sid, &database, &magic, &user, &schema, &zone, &dba, &protocol, &version) != nil || sid <= 0 || magic == 0 || !validText(database) || user != c.connector.User || schema != c.connector.Schema || zone != "+00:00" || dba != "FALSE" || protocol != "UNIXSOCKET" || version != c.native.inventory.ServerVersion {
		return 0, "", "", fail("NATIVE_SESSION_IDENTITY_INVALID")
	}
	return sid, fingerprint(struct {
		Database       string
		PermanentMagic int64
	}{database, magic}), version, nil
}
func (c *NativeCustody) held(ctx context.Context) (string, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	var modes, owners int
	var mode sql.NullString
	var session sql.NullInt64
	if c.query.QueryRowContext(ctx, nativeHeldSQL, c.lockID).Scan(&modes, &mode, &owners, &session) != nil || modes != 1 || owners != 1 || !mode.Valid || !validText(mode.String) || !session.Valid || session.Int64 != c.session {
		return "", fail("NATIVE_MAINTENANCE_LOCK_LOST")
	}
	return mode.String, nil
}
func (c *NativeCustody) CheckFence(ctx context.Context) error {
	if c == nil || ctx == nil || !c.active.Load() {
		return fail("NATIVE_CUSTODY_EXPIRED")
	}
	if c.kernel.VerifyConfiguration(ctx, c.connector) != nil {
		return fail("NATIVE_CONNECTOR_BINDING_CHANGED")
	}
	endpoint, e := c.native.EndpointFingerprint()
	if e != nil || endpoint != c.endpoint {
		return fail("NATIVE_ENDPOINT_CHANGED")
	}
	sid, server, version, e := c.sessionIdentity(ctx)
	if e != nil || sid != c.session || server != c.serverID || version != c.version {
		return fail("NATIVE_SESSION_CHANGED")
	}
	mode, e := c.held(ctx)
	if e != nil || mode != c.lockMode {
		return fail("NATIVE_MAINTENANCE_LOCK_LOST")
	}
	return nil
}

// WithNativeCustody acquires the migration owner's lock ID on a kernel-verified
// pinned session. REQUEST always uses exclusive mode6/release_on_commit=false.
// Its actual mode spelling is read back after that exact successful REQUEST and
// must remain unchanged; no guessed view spelling or configurable weaker mode.
func WithNativeCustody(ctx context.Context, n *Native, kernel *dameng.Store, connector dameng.ConnectionConfiguration, schema dameng.SchemaConfiguration, policy SnapshotPolicy, mode MaintenanceMode, operation func(context.Context, *NativeCustody) error) (result error) {
	if ctx == nil || n == nil || kernel == nil || operation == nil || policy.Validate() != nil || schema.Validate() != nil || connector.Validate() != nil || (mode != SourceMaintenance && mode != FreshRestoreMaintenance) {
		return fail("NATIVE_CONTEXT_REQUIRED")
	}
	if n.configuration.Connection.Transport != connector.Transport || n.configuration.Connection.SocketFile != connector.SocketFile || n.configuration.Connection.SocketOwnerUID != connector.SocketOwnerUID || n.configuration.Connection.User != connector.User || n.configuration.Connection.Schema != connector.Schema || n.configuration.Connection.PasswordFile != connector.PasswordFile || schema.Schema != connector.Schema || policy.PageRows > connector.Policy.MaximumRows || policy.LOBChunkSize > connector.Policy.MaximumCellBytes/4 {
		return fail("NATIVE_CONTEXT_POLICY_MISMATCH")
	}
	// Whole-platform recovery requires the independent closed catalog witness;
	// agreement with whatever partial Plan is compiled does not prove closure.
	if migrate.RequireControlServingCatalog() != nil {
		return fail("NATIVE_COMPLETE_CATALOG_REQUIRED")
	}
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	if kernel.VerifyConfiguration(ctx, connector) != nil {
		return fail("NATIVE_CONNECTOR_BINDING_REQUIRED")
	}
	if kernel.ValidateSchemaUse(schema, false, 0) != nil {
		return fail("NATIVE_SCHEMA_POLICY_MISMATCH")
	}
	plan, e := migrate.Plan()
	if e != nil {
		return fail("NATIVE_CATALOG_PLAN_INVALID")
	}
	lockID, e := migrate.SchemaLockID(schema.Schema)
	if e != nil {
		return fail("NATIVE_SCHEMA_LOCK_INVALID")
	}
	tables := []string{"PO_SCHEMA_MIGRATIONS"}
	seen := map[string]bool{"PO_SCHEMA_MIGRATIONS": true}
	for _, s := range plan {
		if s.Kind == "table" {
			if !identifier.MatchString(s.Object) || seen[s.Object] {
				return fail("NATIVE_CATALOG_PLAN_INVALID")
			}
			seen[s.Object] = true
			tables = append(tables, s.Object)
		}
	}
	sort.Strings(tables)
	endpoint, e := n.EndpointFingerprint()
	if e != nil {
		return e
	}
	return kernel.WithSession(ctx, func(ctx context.Context, connection *sql.Conn) (result error) {
		c := &NativeCustody{native: n, kernel: kernel, connector: connector, schema: schema, policy: policy, connection: connection, query: connection, mode: mode, lockID: lockID, plan: plan, tables: tables, endpoint: endpoint, freshMarker: uuid.NewString()}
		c.session, c.serverID, c.version, e = c.sessionIdentity(ctx)
		if e != nil {
			return e
		}
		lockContext, stop := context.WithTimeout(ctx, time.Duration(schema.LockWaitSeconds+schema.StepSeconds)*time.Second)
		var code int
		e = connection.QueryRowContext(lockContext, "SELECT DBMS_LOCK.REQUEST(?,6,?,FALSE) FROM DUAL", lockID, schema.LockWaitSeconds).Scan(&code)
		stop()
		if e != nil || code != 0 {
			return fail("NATIVE_MAINTENANCE_LOCK_UNAVAILABLE")
		}
		defer func() {
			c.active.Store(false)
			if c.transaction != nil {
				if e := c.transaction.Rollback(); e != nil && !errors.Is(e, sql.ErrTxDone) {
					result = errors.Join(result, fail("NATIVE_MAINTENANCE_TRANSACTION_CLOSE_UNCERTAIN"))
				}
				c.transaction = nil
				c.query = connection
			}
			cleanup, stop := context.WithTimeout(context.Background(), time.Duration(schema.CleanupSeconds)*time.Second)
			defer stop()
			var released int
			if connection.QueryRowContext(cleanup, "SELECT DBMS_LOCK.RELEASE(?) FROM DUAL", lockID).Scan(&released) != nil || released != 0 {
				result = errors.Join(result, fail("NATIVE_MAINTENANCE_RELEASE_UNCERTAIN"))
			}
		}()
		c.lockMode, e = c.held(ctx)
		if e != nil {
			return e
		}
		c.active.Store(true)
		var objects int64
		if c.query.QueryRowContext(ctx, "SELECT COUNT(*) FROM ALL_OBJECTS WHERE OWNER=?", schema.Schema).Scan(&objects) != nil {
			return fail("NATIVE_OBJECT_READ_FAILED")
		}
		if objects > 0 {
			if _, e = migrate.CheckCatalogWithin(ctx, connection, schema); e != nil {
				return fail("NATIVE_CATALOG_NOT_READY")
			}
		}
		if mode == SourceMaintenance && objects == 0 {
			return fail("NATIVE_INSTALLED_SOURCE_REQUIRED")
		}
		// The maintenance transaction holds the actual installation row, not a
		// historical DTO. It performs no writes before optional explicit restore
		// invalidation. Native dexp/dimp run on separate authenticated sessions.
		return kernel.WithinConnection(ctx, connection, false, func(ctx context.Context, tx *sql.Tx) error {
			c.transaction = tx
			c.query = tx
			defer func() { c.transaction = nil; c.query = connection }()
			if objects > 0 {
				if e = c.lockInstallation(ctx); e != nil {
					return e
				}
				if e = c.checkQuiescence(ctx); e != nil {
					return e
				}
			}
			if e = c.CheckFence(ctx); e != nil {
				return e
			}
			if e = operation(ctx, c); e != nil {
				return e
			}
			return c.CheckFence(ctx)
		})
	})
}
func (c *NativeCustody) lockInstallation(ctx context.Context) error {
	var id int
	if c.query.QueryRowContext(ctx, `SELECT "ID" FROM "INSTALLATION_STATE" WHERE "ID"=1 FOR UPDATE`).Scan(&id) != nil || id != 1 {
		return fail("NATIVE_INSTALLATION_LOCK_REQUIRED")
	}
	var tenant string
	if c.query.QueryRowContext(ctx, nativeInstallationSQL).Scan(&tenant) != nil {
		return fail("NATIVE_ACTIVE_INSTALLATION_REQUIRED")
	}
	u, e := uuid.Parse(tenant)
	if e != nil || u == uuid.Nil || u.String() != tenant {
		return fail("NATIVE_ACTIVE_INSTALLATION_REQUIRED")
	}
	if c.installation != "" && c.installation != tenant {
		return fail("NATIVE_INSTALLATION_CHANGED")
	}
	c.installation = tenant
	return nil
}

func (c *NativeCustody) checkQuiescence(ctx context.Context) error {
	for _, check := range nativeQuiescenceChecks {
		var count int64
		if c.query.QueryRowContext(ctx, check.sql).Scan(&count) != nil {
			return fail("NATIVE_QUIESCENCE_UNAVAILABLE")
		}
		if count != 0 {
			return fail("NATIVE_" + check.kind + "_NOT_QUIESCENT")
		}
	}
	return nil
}

var nativeQuiescenceChecks = []struct{ kind, sql string }{
	{"NODE_TASKS", `SELECT COUNT(*) FROM "NODE_TASKS" WHERE "STATE" NOT IN ('succeeded','failed','cancelled')`},
	{"COMPONENTS", `SELECT COUNT(*) FROM "MANAGED_COMPONENTS" WHERE "DESIRED_STATE"='running' OR "OBSERVED_STATE" NOT IN ('stopped','not_installed')`},
	{"GPU_LEASES", `SELECT COUNT(*) FROM "GPU_LEASES" WHERE "STATE"<>'released'`},
	{"INFERENCE", `SELECT COUNT(*) FROM "DEPLOYMENTS" WHERE "DESIRED_STATE"='running' OR "OBSERVED_STATE" NOT IN ('stopped','not_installed')`},
	{"GATEWAY_REQUESTS", `SELECT COUNT(*) FROM "GATEWAY_REQUESTS" WHERE "CAPACITY_HELD"=1 OR "STATE"='admitted'`},
	{"TRAINING_JOBS", `SELECT COUNT(*) FROM "TRAINING_JOBS" WHERE "STATE" NOT IN ('succeeded','failed','cancelled')`},
	{"TRAINING_ATTEMPTS", `SELECT COUNT(*) FROM "TRAINING_ATTEMPTS" WHERE "PHYSICAL_STOPPED_AT" IS NULL AND "STATE" IN ('pending','preparing','running','stopping','recovery_required')`},
	{"TRAINING_UPLOADS", `SELECT COUNT(*) FROM "TRAINING_UPLOADS" WHERE "STATE" NOT IN ('accepted','failed')`},
	{"PREPARATIONS", `SELECT COUNT(*) FROM "MODEL_PREPARATIONS" WHERE "STATE" NOT IN ('quality_failed','awaiting_review','approved','rejected','failed','cancelled')`},
	{"PREPARATION_ATTEMPTS", `SELECT COUNT(*) FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "PHYSICAL_STOPPED_AT" IS NULL AND "STATE" IN ('pending','preparing','running','stopping','recovery_required')`},
	{"PREPARATION_UPLOADS", `SELECT COUNT(*) FROM "MODEL_PREPARATION_UPLOADS" WHERE "STATE" NOT IN ('accepted','failed')`},
	{"WORKFLOWS", `SELECT COUNT(*) FROM "WORKFLOW_RUNS" WHERE "STATE" IN ('pending','running') OR "LEASE_ID" IS NOT NULL`},
	{"WORKFLOW_TRACES", `SELECT COUNT(*) FROM "WORKFLOW_NODE_TRACES" WHERE "STATE"='running'`},
	{"MODEL_IMPORTS", `SELECT COUNT(*) FROM "MODEL_IMPORTS" WHERE "STATE" NOT IN ('ready','cancelled')`},
}

func (c *NativeCustody) Inspect(ctx context.Context) (State, error) {
	return c.inspect(ctx, "")
}

func (c *NativeCustody) inspect(ctx context.Context, excludedAudit string) (State, error) {
	if migrate.RequireControlServingCatalog() != nil {
		return State{}, fail("NATIVE_COMPLETE_CATALOG_REQUIRED")
	}
	if e := c.CheckFence(ctx); e != nil {
		return State{}, e
	}
	var objects int64
	if c.query.QueryRowContext(ctx, "SELECT COUNT(*) FROM ALL_OBJECTS WHERE OWNER=?", c.schema.Schema).Scan(&objects) != nil || objects < 0 {
		return State{}, fail("NATIVE_OBJECT_READ_FAILED")
	}
	out := State{ServerID: c.serverID, Version: c.version, Schema: c.schema.Schema, Principal: c.connector.User, Objects: objects, EndpointSHA256: c.endpoint}
	if objects == 0 {
		if c.mode != FreshRestoreMaintenance {
			return State{}, fail("NATIVE_INSTALLED_SOURCE_REQUIRED")
		}
		out.CatalogSHA256 = sha(nil)
		out.RowsSHA256 = sha(nil)
		out.FreshMarker = c.freshMarker
		return out, c.CheckFence(ctx)
	}
	report, e := migrate.CheckCatalogWithin(ctx, c.connection, c.schema)
	if e != nil || !report.FoundationReady || report.AppliedSteps != len(c.plan) || report.DirtyStep != 0 {
		return State{}, fail("NATIVE_CATALOG_NOT_READY")
	}
	if e = c.lockInstallation(ctx); e != nil {
		return State{}, e
	}
	if e = c.checkQuiescence(ctx); e != nil {
		return State{}, e
	}
	grants, e := c.grants(ctx)
	if e != nil {
		return State{}, e
	}
	stream := newRowStream(c.policy)
	for _, name := range c.tables {
		table, e := c.table(ctx, name)
		if e != nil {
			return State{}, e
		}
		if e = readNativeTable(ctx, c.query, table, stream, excludedAudit); e != nil {
			return State{}, e
		}
		if e = c.CheckFence(ctx); e != nil {
			return State{}, e
		}
	}
	out.InstallationID = c.installation
	out.CatalogSHA256 = fingerprint(struct {
		Plan   []migrate.PlannedStep
		Grants []grantRow
	}{c.plan, grants})
	out.RowsSHA256 = hashString(stream.digest)
	return out, c.CheckFence(ctx)
}

type grantRow struct{ Owner, Table, Grantee, Privilege, Grantable string }

func (c *NativeCustody) grants(ctx context.Context) ([]grantRow, error) {
	rows, e := c.query.QueryContext(ctx, "SELECT OWNER,TABLE_NAME,GRANTEE,PRIVILEGE,GRANTABLE FROM ALL_TAB_PRIVS WHERE OWNER=? ORDER BY OWNER,TABLE_NAME,GRANTEE,PRIVILEGE,GRANTABLE", c.schema.Schema)
	if e != nil {
		return nil, fail("NATIVE_GRANTS_READ_FAILED")
	}
	defer rows.Close()
	out := []grantRow{}
	for rows.Next() {
		var g grantRow
		if rows.Scan(&g.Owner, &g.Table, &g.Grantee, &g.Privilege, &g.Grantable) != nil || g.Owner != c.schema.Schema || !identifier.MatchString(g.Table) || !identifier.MatchString(g.Grantee) || (g.Grantable != "YES" && g.Grantable != "NO") {
			return nil, fail("NATIVE_GRANTS_INVALID")
		}
		out = append(out, g)
		if len(out) > c.schema.MaximumMetadataRows {
			return nil, fail("NATIVE_METADATA_LIMIT")
		}
	}
	if rows.Err() != nil {
		return nil, fail("NATIVE_GRANTS_READ_FAILED")
	}
	if rows.Close() != nil {
		return nil, fail("NATIVE_GRANTS_READ_FAILED")
	}
	var columns int
	if c.query.QueryRowContext(ctx, "SELECT COUNT(*) FROM ALL_COL_PRIVS WHERE OWNER=?", c.schema.Schema).Scan(&columns) != nil || columns != 0 {
		return nil, fail("NATIVE_COLUMN_GRANTS_UNSUPPORTED")
	}
	return out, nil
}
func (c *NativeCustody) table(ctx context.Context, name string) (nativeTable, error) {
	out := nativeTable{Name: name}
	rows, e := c.query.QueryContext(ctx, "SELECT COLUMN_NAME,DATA_TYPE,NULLABLE FROM ALL_TAB_COLUMNS WHERE OWNER=? AND TABLE_NAME=? ORDER BY COLUMN_ID", c.schema.Schema, name)
	if e != nil {
		return out, fail("NATIVE_COLUMNS_READ_FAILED")
	}
	for rows.Next() {
		var col nativeColumn
		var nullable string
		if rows.Scan(&col.Name, &col.Type, &nullable) != nil || !identifier.MatchString(col.Name) || (nullable != "Y" && nullable != "N") {
			rows.Close()
			return out, fail("NATIVE_COLUMN_INVALID")
		}
		col.Nullable = nullable == "Y"
		switch col.Type {
		case "TIMESTAMP(6)", "DATETIME(6)":
			col.Type = "TIMESTAMP"
		}
		out.Columns = append(out.Columns, col)
		if len(out.Columns) > c.connector.Policy.MaximumColumns {
			rows.Close()
			return out, fail("NATIVE_METADATA_LIMIT")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(out.Columns) == 0 {
		return out, fail("NATIVE_COLUMNS_READ_FAILED")
	}
	rows, e = c.query.QueryContext(ctx, "SELECT CC.COLUMN_NAME FROM ALL_CONSTRAINTS C JOIN ALL_CONS_COLUMNS CC ON CC.OWNER=C.OWNER AND CC.CONSTRAINT_NAME=C.CONSTRAINT_NAME WHERE C.OWNER=? AND C.TABLE_NAME=? AND C.CONSTRAINT_TYPE='P' ORDER BY CC.POSITION", c.schema.Schema, name)
	if e != nil {
		return out, fail("NATIVE_PRIMARY_KEY_READ_FAILED")
	}
	for rows.Next() {
		var key string
		if rows.Scan(&key) != nil || !identifier.MatchString(key) {
			rows.Close()
			return out, fail("NATIVE_PRIMARY_KEY_INVALID")
		}
		out.Keys = append(out.Keys, key)
	}
	e = rows.Err()
	rows.Close()
	if e != nil || len(out.Keys) == 0 {
		return out, fail("NATIVE_PRIMARY_KEY_REQUIRED")
	}
	return out, nil
}
