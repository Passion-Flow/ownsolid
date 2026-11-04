package databaseaccess

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const Verified = "RUNTIME_ACCESS_VERIFIED"
const NotCommitted = "RUNTIME_ACCESS_NOT_COMMITTED"

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}
type target struct {
	Database, Schema, Server string
	DatabaseOID, SchemaOID   uint32
	Port                     int
}

func readTarget(ctx context.Context, q queryer) (target, error) {
	var t target
	e := q.QueryRow(ctx, `SELECT current_database(),current_schema(),d.oid,n.oid,COALESCE(inet_server_addr()::text,''),COALESCE(inet_server_port(),0) FROM pg_database d,pg_namespace n WHERE d.datname=current_database() AND n.nspname=current_schema()`).Scan(&t.Database, &t.Schema, &t.DatabaseOID, &t.SchemaOID, &t.Server, &t.Port)
	if e != nil {
		return t, errors.New("RUNTIME_ACCESS_TARGET_UNAVAILABLE")
	}
	return t, nil
}
func lockedTarget(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (target, error) {
	if p == nil || lease == nil || input.connection == nil {
		return target{}, errors.New("RUNTIME_ACCESS_CONTEXT_INVALID")
	}
	op := p.Config().ConnConfig
	runtime := input.connection.ConnConfig
	if !roleName.MatchString(runtime.User) || !credentialValid(runtime.Password) || runtime.User == op.User || runtime.Host != op.Host || runtime.Port != op.Port || runtime.Database != op.Database || runtime.RuntimeParams["search_path"] != op.RuntimeParams["search_path"] || input.configuration.Database.MaxOpenConnections < config.MinimumDatabaseConnections || input.configuration.Database.MaxOpenConnections > config.MaximumDatabaseConnections {
		return target{}, errors.New("RUNTIME_ACCESS_CONNECTION_CONTEXT_INVALID")
	}
	var locked, super bool
	e := lease.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND pid=pg_backend_pid() AND granted AND mode='ExclusiveLock' AND classid=0 AND objid=$1::oid AND objsubid=1 AND database=(SELECT oid FROM pg_database WHERE datname=current_database())),(SELECT rolsuper FROM pg_roles WHERE rolname=current_user)`, database.InstallationLockID).Scan(&locked, &super)
	if e != nil || !locked {
		return target{}, errors.New("RUNTIME_ACCESS_INSTALLATION_LOCK_REQUIRED")
	}
	if !super {
		return target{}, errors.New("RUNTIME_ACCESS_PRIVILEGED_OPERATOR_REQUIRED")
	}
	t, e := readTarget(ctx, lease)
	if e != nil {
		return t, e
	}
	poolTarget, e := readTarget(ctx, p)
	if e != nil || poolTarget != t {
		return t, errors.New("RUNTIME_ACCESS_TARGET_MISMATCH")
	}
	return t, nil
}

// inspectRoleIdentity checks the principal itself: attributes, credential and
// memberships. Upgrade uses it before any DDL, when table grants are still
// those of the earlier version.
func inspectRoleIdentity(ctx context.Context, q queryer, t target, input RuntimeInput) (uint32, bool, error) {
	var oid uint32
	var super, inherit, createRole, createDB, login, replication, bypass, unlimitedValidity bool
	var limit int
	var verifier string
	e := q.QueryRow(ctx, `SELECT oid,rolsuper,rolinherit,rolcreaterole,rolcreatedb,rolcanlogin,rolreplication,rolbypassrls,rolconnlimit,rolvaliduntil IS NULL,COALESCE(rolpassword,'') FROM pg_authid WHERE rolname=$1`, input.connection.ConnConfig.User).Scan(&oid, &super, &inherit, &createRole, &createDB, &login, &replication, &bypass, &limit, &unlimitedValidity, &verifier)
	if errors.Is(e, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if e != nil {
		return 0, false, errors.New("RUNTIME_ACCESS_ROLE_UNAVAILABLE")
	}
	if super || inherit || createRole || createDB || !login || replication || bypass || !unlimitedValidity || limit != int(input.configuration.Database.MaxOpenConnections) || !verifyCredential(verifier, input.connection.ConnConfig.Password) {
		return 0, true, errors.New("RUNTIME_ACCESS_ROLE_OR_CREDENTIAL_CONFLICT")
	}
	var forbidden bool
	e = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=$1::oid OR roleid=$1::oid) OR EXISTS(SELECT 1 FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=$1::oid AND deptype='o') OR has_database_privilege($1::oid,current_database(),'CREATE') OR has_schema_privilege($1::oid,$2::oid,'CREATE,USAGE WITH GRANT OPTION') OR NOT has_schema_privilege($1::oid,$2::oid,'USAGE') OR NOT has_database_privilege($1::oid,current_database(),'CONNECT')`, oid, t.SchemaOID).Scan(&forbidden)
	if e != nil || forbidden {
		return 0, true, errors.New("RUNTIME_ACCESS_ELEVATED_OR_SCHEMA_PRIVILEGE_CONFLICT")
	}
	return oid, true, nil
}

func inspectRole(ctx context.Context, q queryer, t target, input RuntimeInput) (bool, error) {
	oid, exists, e := inspectRoleIdentity(ctx, q, t, input)
	if e != nil || !exists {
		return exists, e
	}
	var forbidden bool
	known := map[string]bool{"schema_migrations": true}
	for _, name := range postgreSQLTables() {
		known[name] = true
	}
	seen := map[string]bool{}
	rows, e := q.Query(ctx, `SELECT n.nspname,c.relname,c.relowner,has_table_privilege($1::oid,c.oid,'SELECT'),has_table_privilege($1::oid,c.oid,'INSERT'),has_table_privilege($1::oid,c.oid,'UPDATE'),has_table_privilege($1::oid,c.oid,'DELETE'),has_table_privilege($1::oid,c.oid,'TRUNCATE,REFERENCES,TRIGGER,SELECT WITH GRANT OPTION,INSERT WITH GRANT OPTION,UPDATE WITH GRANT OPTION,DELETE WITH GRANT OPTION') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relkind IN ('r','p','v','m','f') AND n.nspname !~ '^pg_' AND n.nspname<>'information_schema' ORDER BY n.nspname,c.relname`, oid)
	if e != nil {
		return true, errors.New("RUNTIME_ACCESS_TABLE_PRIVILEGES_UNAVAILABLE")
	}
	defer rows.Close()
	for rows.Next() {
		var schema, name string
		var owner uint32
		var selectOK, insertOK, updateOK, deleteOK, extra bool
		if e = rows.Scan(&schema, &name, &owner, &selectOK, &insertOK, &updateOK, &deleteOK, &extra); e != nil {
			return true, errors.New("RUNTIME_ACCESS_TABLE_PRIVILEGES_UNAVAILABLE")
		}
		product := schema == t.Schema && known[name]
		if !product {
			if selectOK || insertOK || updateOK || deleteOK || extra {
				return true, errors.New("RUNTIME_ACCESS_UNRELATED_TABLE_PRIVILEGE_CONFLICT")
			}
			continue
		}
		seen[name] = true
		if owner == oid || !selectOK || extra || name == "schema_migrations" && (insertOK || updateOK || deleteOK) || name != "schema_migrations" && (!insertOK || !updateOK || !deleteOK) {
			return true, errors.New("RUNTIME_ACCESS_PRODUCT_TABLE_PRIVILEGE_CONFLICT")
		}
	}
	if rows.Err() != nil || len(seen) != len(known) {
		return true, errors.New("RUNTIME_ACCESS_TABLE_CATALOG_INCOMPATIBLE")
	}
	rows.Close()
	// CASE protects the sequence-only function from predicate reordering on
	// other relation kinds; an AND clause alone does not order evaluation.
	e = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND CASE WHEN c.relkind='S' THEN has_sequence_privilege($1::oid,c.oid,'USAGE,SELECT,UPDATE') ELSE false END)`, oid).Scan(&forbidden)
	if e != nil || forbidden {
		return true, errors.New("RUNTIME_ACCESS_SEQUENCE_PRIVILEGE_CONFLICT")
	}
	return true, nil
}
func checkRuntimeConnection(ctx context.Context, t target, input RuntimeInput) error {
	pc := input.connection.Copy()
	p, e := pgxpool.NewWithConfig(ctx, pc)
	if e != nil {
		return errors.New("RUNTIME_ACCESS_LOGIN_FAILED")
	}
	defer p.Close()
	actual, e := readTarget(ctx, p)
	if e != nil || actual != t {
		return errors.New("RUNTIME_ACCESS_RUNTIME_TARGET_MISMATCH")
	}
	var user string
	if e = p.QueryRow(ctx, "SELECT current_user").Scan(&user); e != nil || user != pc.ConnConfig.User {
		return errors.New("RUNTIME_ACCESS_LOGIN_IDENTITY_MISMATCH")
	}
	if e = database.Check(ctx, p); e != nil {
		return errors.New("RUNTIME_ACCESS_SCHEMA_INCOMPATIBLE")
	}
	return nil
}

// Check never changes roles, passwords, grants or application data.
func Check(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	t, e := lockedTarget(ctx, p, lease, input)
	if e != nil {
		return "", e
	}
	exists, e := inspectRole(ctx, lease, t, input)
	if e != nil {
		return "", e
	}
	if !exists {
		return NotCommitted, nil
	}
	if e = checkRuntimeConnection(ctx, t, input); e != nil {
		return "", e
	}
	return Verified, nil
}

// Prepare creates only a new role. Existing identities are inspected and
// preserved; missing or conflicting grants are never silently repaired.
func Prepare(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput) (string, error) {
	return prepare(ctx, p, lease, input, nil)
}

func prepare(ctx context.Context, p *pgxpool.Pool, lease *pgxpool.Conn, input RuntimeInput, restored *release.RestoredRuntimeContext) (string, error) {
	t, e := lockedTarget(ctx, p, lease, input)
	if e != nil {
		return "", e
	}
	if restored != nil {
		if e = verifyRestoredTarget(ctx, lease, t, *restored); e != nil {
			return "", e
		}
	}
	status, e := Check(ctx, p, lease, input)
	if e != nil || status == Verified {
		return status, e
	}
	if e = database.Check(ctx, p); e != nil {
		return "", errors.New("RUNTIME_ACCESS_SCHEMA_INCOMPATIBLE")
	}
	if restored == nil {
		var empty bool
		if e = lease.QueryRow(ctx, `SELECT NOT bootstrap_complete AND (SELECT count(*) FROM users)=0 AND (SELECT count(*) FROM tenants)=0 AND (SELECT count(*) FROM workspaces)=0 FROM installation_state WHERE id=1`).Scan(&empty); e != nil || !empty {
			return "", errors.New("RUNTIME_ACCESS_INITIAL_INSTALLATION_REQUIRED")
		}
	}
	var iterationText string
	if e = lease.QueryRow(ctx, "SHOW scram_iterations").Scan(&iterationText); e != nil {
		return "", errors.New("RUNTIME_SCRAM_PARAMETERS_UNSUPPORTED")
	}
	iterations, e := strconv.Atoi(iterationText)
	if e != nil {
		return "", errors.New("RUNTIME_SCRAM_PARAMETERS_UNSUPPORTED")
	}
	verifier, e := newVerifier(input.connection.ConnConfig.Password, iterations)
	if e != nil {
		return "", e
	}
	tx, e := lease.Begin(ctx)
	if e != nil {
		return "", errors.New("RUNTIME_ACCESS_TRANSACTION_UNAVAILABLE")
	}
	defer tx.Rollback(context.Background())
	// These settings affect only this owned transaction and are restored on
	// commit/rollback. Suppress credential-verifier statement/parameter logging.
	if _, e = tx.Exec(ctx, `SELECT set_config('log_statement','none',true),set_config('log_min_error_statement','panic',true),set_config('log_parameter_max_length','0',true),set_config('log_parameter_max_length_on_error','0',true)`); e != nil {
		return "", errors.New("RUNTIME_ACCESS_PRIVATE_TRANSACTION_UNAVAILABLE")
	}
	role := pgx.Identifier{input.connection.ConnConfig.User}.Sanitize()
	statement := "CREATE ROLE " + role + " WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS CONNECTION LIMIT " + strconv.Itoa(int(input.configuration.Database.MaxOpenConnections)) + " PASSWORD '" + verifier + "'"
	if _, e = tx.Exec(ctx, statement); e != nil {
		return "", errors.New("RUNTIME_ACCESS_ROLE_CREATE_FAILED")
	}
	if _, e = tx.Exec(ctx, "GRANT USAGE ON SCHEMA "+pgx.Identifier{t.Schema}.Sanitize()+" TO "+role); e != nil {
		return "", errors.New("RUNTIME_ACCESS_GRANT_FAILED")
	}
	for _, name := range postgreSQLTables() {
		if _, e = tx.Exec(ctx, "GRANT SELECT,INSERT,UPDATE,DELETE ON TABLE "+pgx.Identifier{t.Schema, name}.Sanitize()+" TO "+role); e != nil {
			return "", errors.New("RUNTIME_ACCESS_GRANT_FAILED")
		}
	}
	if _, e = tx.Exec(ctx, "GRANT SELECT ON TABLE "+pgx.Identifier{t.Schema, "schema_migrations"}.Sanitize()+" TO "+role); e != nil {
		return "", errors.New("RUNTIME_ACCESS_GRANT_FAILED")
	}
	if _, e = inspectRole(ctx, tx, t, input); e != nil {
		return "", e
	}
	if restored != nil {
		metadata := restoredAuditMetadata(*restored, input.connection.ConnConfig.User)
		if _, e = tx.Exec(ctx, `INSERT INTO audit_events(id,occurred_at,tenant_id,audience,action,outcome,target_type,target_id,reason,request_id,metadata) VALUES(gen_random_uuid(),clock_timestamp(),$1::uuid,'operator','operator.runtime_access_restored','success','installation',$1::uuid,$2,gen_random_uuid(),$3::jsonb)`, restored.TenantID, restored.Reason, metadata); e != nil {
			return "", errors.New("RUNTIME_ACCESS_RESTORE_AUDIT_FAILED")
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return "", errors.New("RUNTIME_ACCESS_COMMIT_UNCERTAIN")
	}
	return Check(ctx, p, lease, input)
}
