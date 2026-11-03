//go:build cgo && odbc_manager

package migrate

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

// runtimePrivileges inspects effective system/role authority and actual direct
// grants. It permits a partial exact subset only before an explicit grant pass.
func runtimePrivileges(ctx context.Context, connection *sql.Conn, c dameng.SchemaConfiguration, catalog catalog, complete bool) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
	defer cancel()
	var user, schema string
	var schemaGrants int
	if connection.QueryRowContext(ctx, "SELECT SYS_CONTEXT('USERENV','SESSION_USER'),SYS_CONTEXT('USERENV','CURRENT_SCHEMA'),SF_GET_PARA_VALUE(1,'GRANT_SCHEMA') FROM DUAL").Scan(&user, &schema, &schemaGrants) != nil || user != c.RuntimeUser || schema != c.Schema || user == schema || schemaGrants != 0 {
		return fail("DAMENG_SCHEMA_RUNTIME_IDENTITY_INVALID")
	}
	rows, e := connection.QueryContext(ctx, "SELECT PRIVILEGE FROM SESSION_PRIVS")
	if e != nil {
		return fail("DAMENG_SCHEMA_RUNTIME_PRIVILEGES_UNAVAILABLE")
	}
	canLogin := false
	for rows.Next() {
		var p string
		if rows.Scan(&p) != nil || !allowedRuntimeSystemPrivilege(p) {
			rows.Close()
			return fail("DAMENG_SCHEMA_RUNTIME_EXCESS_PRIVILEGE")
		}
		canLogin = canLogin || p == "CREATE SESSION"
	}
	e = rows.Err()
	rows.Close()
	if e != nil || !canLogin {
		return fail("DAMENG_SCHEMA_RUNTIME_PRIVILEGES_UNAVAILABLE")
	}
	// Disabled roles can later be enabled, so inspect assigned roles as well as
	// current session roles. PUBLIC's inherited dictionary-only SVI is permitted;
	// application objects granted to either role are checked below.
	for _, query := range []string{"SELECT GRANTED_ROLE FROM USER_ROLE_PRIVS", "SELECT ROLE FROM SESSION_ROLES"} {
		rows, e = connection.QueryContext(ctx, query)
		if e != nil {
			return fail("DAMENG_SCHEMA_RUNTIME_PRIVILEGES_UNAVAILABLE")
		}
		for rows.Next() {
			var role string
			if rows.Scan(&role) != nil || !allowedRuntimeRole(role) {
				rows.Close()
				return fail("DAMENG_SCHEMA_RUNTIME_EXCESS_PRIVILEGE")
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return fail("DAMENG_SCHEMA_RUNTIME_PRIVILEGES_UNAVAILABLE")
		}
	}
	var owned int
	if connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM ALL_OBJECTS WHERE OWNER=?", c.RuntimeUser).Scan(&owned) != nil || owned != 0 {
		return fail("DAMENG_SCHEMA_RUNTIME_OWNS_OBJECTS")
	}
	// The account must have no explicit grants outside this closed schema, and no
	// grant option. PUBLIC access to this application schema is forbidden.
	rows, e = connection.QueryContext(ctx, "SELECT OWNER,TABLE_NAME,GRANTEE,PRIVILEGE,GRANTABLE FROM ALL_TAB_PRIVS WHERE GRANTEE=? OR (OWNER=? AND GRANTEE IN ('PUBLIC','SVI'))", c.RuntimeUser, c.Schema)
	if e != nil {
		return fail("DAMENG_SCHEMA_RUNTIME_PRIVILEGES_UNAVAILABLE")
	}
	want := requiredGrants(catalog)
	seen := map[string]bool{}
	for rows.Next() {
		var owner, table, grantee, privilege, grantable string
		if rows.Scan(&owner, &table, &grantee, &privilege, &grantable) != nil || owner != c.Schema || grantee != c.RuntimeUser || grantable != "NO" || !want[table][privilege] || seen[table+":"+privilege] {
			rows.Close()
			return fail("DAMENG_SCHEMA_RUNTIME_EXCESS_PRIVILEGE")
		}
		seen[table+":"+privilege] = true
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return fail("DAMENG_SCHEMA_RUNTIME_PRIVILEGES_UNAVAILABLE")
	}
	// Column-level or delegable system grants cannot hide behind the table list.
	for _, query := range []string{"SELECT COUNT(*) FROM USER_SYS_PRIVS WHERE ADMIN_OPTION='YES'", "SELECT COUNT(*) FROM USER_ROLE_PRIVS WHERE ADMIN_OPTION='YES'"} {
		var count int
		if connection.QueryRowContext(ctx, query).Scan(&count) != nil || count != 0 {
			return fail("DAMENG_SCHEMA_RUNTIME_EXCESS_PRIVILEGE")
		}
	}
	var columnGrants int
	if connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM ALL_COL_PRIVS WHERE GRANTEE=? OR (OWNER=? AND GRANTEE IN ('PUBLIC','SVI'))", c.RuntimeUser, c.Schema).Scan(&columnGrants) != nil || columnGrants != 0 {
		return fail("DAMENG_SCHEMA_RUNTIME_EXCESS_PRIVILEGE")
	}
	if complete {
		for table, privileges := range want {
			for privilege := range privileges {
				if !seen[table+":"+privilege] {
					return fail("DAMENG_SCHEMA_RUNTIME_GRANT_MISSING")
				}
			}
		}
	}
	return nil
}

// CheckRuntime never writes or returns platform-ready. A native foundation
// account still lacks the rest of the business schema and repository graph.
func CheckRuntime(ctx context.Context, kernel *dameng.Store, c dameng.SchemaConfiguration) (Report, error) {
	cat, e := defaultCatalog()
	r := Report{Scope: Scope, TotalSteps: len(cat.steps)}
	if e != nil {
		return r, e
	}
	if ctx == nil {
		return r, fail("DAMENG_SCHEMA_CONTEXT_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return r, fail("DAMENG_SCHEMA_DEADLINE_REQUIRED")
	}
	if kernel.ValidateSchemaUse(c, true, maxSQL(cat)) != nil {
		return r, fail("DAMENG_SCHEMA_CONNECTION_POLICY_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.TotalSeconds)*time.Second)
	defer cancel()
	p, e := kernel.BorrowPool()
	if e != nil {
		return r, fail("DAMENG_SCHEMA_CONNECTION_UNAVAILABLE")
	}
	connection, e := p.Conn(ctx)
	if e != nil {
		return r, fail("DAMENG_SCHEMA_CONNECTION_UNAVAILABLE")
	}
	e = runtimePrivileges(ctx, connection, c, cat, true)
	if e == nil {
		n := &nativeBackend{connection, c, cat}
		e = n.expressionCapabilities(ctx)
		if e == nil {
			s, err := n.inspect(ctx)
			if err == nil {
				r, err = assess(cat, s)
				if err == nil && !r.FoundationReady {
					err = fail("DAMENG_SCHEMA_NOT_READY")
				}
			}
			e = err
		}
	}
	if e == nil && r.FoundationReady {
		r.RuntimeReady = true
	}
	if connection.Close() != nil {
		r.FoundationReady = false
		r.RuntimeReady = false
		e = fail("DAMENG_SCHEMA_SESSION_CLOSE_UNCERTAIN")
	}
	return r, e
}

// GrantRuntime requires TWO actual admitted identities bound to the same original
// socket inode/schema/native manifest. It does not create an account, set a
// password, grant a role, revoke unrelated permissions or retry an uncertain SQL.
func GrantRuntime(ctx context.Context, owner, runtime *dameng.Store, c dameng.SchemaConfiguration) (Report, error) {
	cat, e := defaultCatalog()
	if e != nil {
		return Report{Scope: Scope}, e
	}
	if !owner.SameSchemaTarget(runtime) || runtime.ValidateSchemaUse(c, true, maxSQL(cat)) != nil {
		return Report{Scope: Scope}, fail("DAMENG_SCHEMA_RUNTIME_TARGET_MISMATCH")
	}
	return withOwner(ctx, owner, c, func(ctx context.Context, n *nativeBackend) (report Report, result error) {
		report = Report{Scope: Scope, TotalSteps: len(cat.steps)}
		p, e := runtime.BorrowPool()
		if e != nil {
			return report, fail("DAMENG_SCHEMA_RUNTIME_UNAVAILABLE")
		}
		connection, e := p.Conn(ctx)
		if e != nil {
			return report, fail("DAMENG_SCHEMA_RUNTIME_UNAVAILABLE")
		}
		defer func() {
			if connection.Close() != nil {
				report.FoundationReady = false
				report.RuntimeReady = false
				result = fail("DAMENG_SCHEMA_SESSION_CLOSE_UNCERTAIN")
			}
		}()
		if e = runtimePrivileges(ctx, connection, c, cat, false); e != nil {
			return report, e
		}
		if e = (&nativeBackend{connection, c, cat}).expressionCapabilities(ctx); e != nil {
			return report, e
		}
		report, e = run(ctx, cat, n, Readiness)
		if e != nil {
			return report, e
		}
		want := requiredGrants(cat)
		objects := make([]string, 0, len(want))
		for name := range want {
			objects = append(objects, name)
		}
		sort.Strings(objects)
		for _, name := range objects {
			stepContext, stop := n.bounded(ctx)
			rows, e := n.connection.QueryContext(stepContext, "SELECT PRIVILEGE,GRANTABLE FROM ALL_TAB_PRIVS WHERE OWNER=? AND TABLE_NAME=? AND GRANTEE=?", c.Schema, name, c.RuntimeUser)
			if e != nil {
				stop()
				report.FoundationReady = false
				return report, fail("DAMENG_SCHEMA_GRANT_METADATA_UNAVAILABLE")
			}
			seen := map[string]bool{}
			for rows.Next() {
				var privilege, grantable string
				if rows.Scan(&privilege, &grantable) != nil || !want[name][privilege] || grantable != "NO" || seen[privilege] {
					rows.Close()
					stop()
					report.FoundationReady = false
					return report, fail("DAMENG_SCHEMA_RUNTIME_EXCESS_PRIVILEGE")
				}
				seen[privilege] = true
			}
			e = rows.Err()
			rows.Close()
			stop()
			if e != nil {
				report.FoundationReady = false
				return report, fail("DAMENG_SCHEMA_GRANT_METADATA_UNAVAILABLE")
			}
			var missing []string
			for privilege := range want[name] {
				if !seen[privilege] {
					missing = append(missing, privilege)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				if e = n.exec(ctx, "GRANT "+strings.Join(missing, ",")+" ON "+objectSQL(c.Schema, name)+" TO "+quoted(c.RuntimeUser)); e != nil {
					report.FoundationReady = false
					return report, fail("DAMENG_SCHEMA_GRANT_UNCERTAIN_RECHECK_REQUIRED")
				}
			}
		}
		if e = runtimePrivileges(ctx, connection, c, cat, true); e != nil {
			report.FoundationReady = false
			return report, e
		}
		report.RuntimeReady = report.FoundationReady
		return report, nil
	})
}
