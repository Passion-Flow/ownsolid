package database

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// A privileged operator may inspect/migrate a catalog on its held session.
// Serving must separately select the runtime-only entrypoint below. Non-super
// callers always require complete business DML, SELECT-only ledger and no
// persistent DDL/ownership/membership authority, including after interrupted DDL.
const currentPrivilegesSQL = `WITH r AS (
 SELECT * FROM pg_roles WHERE rolname=current_user
), objects AS (
 SELECT c.oid,c.relname,c.relowner,n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE c.relkind IN ('r','p','v','m','f') AND NOT c.relispartition AND n.nspname !~ '^pg_' AND n.nspname<>'information_schema'
)
SELECT CASE WHEN r.rolsuper THEN $1::boolean ELSE
 NOT r.rolinherit AND NOT r.rolcreatedb AND NOT r.rolcreaterole AND r.rolcanlogin AND NOT r.rolreplication AND NOT r.rolbypassrls AND r.rolconnlimit BETWEEN $2 AND $3 AND r.rolvaliduntil IS NULL
 AND NOT EXISTS(SELECT 1 FROM pg_auth_members m WHERE m.member=r.oid OR m.roleid=r.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_shdepend d WHERE d.refclassid='pg_authid'::regclass AND d.refobjid=r.oid AND d.deptype='o')
 AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND CASE WHEN c.relkind='S' THEN has_sequence_privilege(r.oid,c.oid,'USAGE,SELECT,UPDATE') ELSE false END)
 AND has_database_privilege(r.oid,current_database(),'CONNECT') AND NOT has_database_privilege(r.oid,current_database(),'CREATE')
 AND has_schema_privilege(r.oid,current_schema(),'USAGE') AND NOT has_schema_privilege(r.oid,current_schema(),'CREATE,USAGE WITH GRANT OPTION')
 AND (SELECT count(*) FROM objects WHERE nspname=current_schema())=$4
 AND NOT EXISTS(SELECT 1 FROM objects o WHERE CASE WHEN o.nspname=current_schema() THEN
  o.relowner=r.oid OR NOT has_table_privilege(r.oid,o.oid,'SELECT')
  OR has_table_privilege(r.oid,o.oid,'TRUNCATE,REFERENCES,TRIGGER,SELECT WITH GRANT OPTION,INSERT WITH GRANT OPTION,UPDATE WITH GRANT OPTION,DELETE WITH GRANT OPTION')
  OR CASE WHEN o.relname='schema_migrations' THEN has_table_privilege(r.oid,o.oid,'INSERT,UPDATE,DELETE')
    ELSE NOT has_table_privilege(r.oid,o.oid,'INSERT') OR NOT has_table_privilege(r.oid,o.oid,'UPDATE') OR NOT has_table_privilege(r.oid,o.oid,'DELETE') END
 ELSE has_table_privilege(r.oid,o.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') END)
 END FROM r`

func checkCurrentPrivileges(ctx context.Context, q transaction.Executor, allowOperator bool) error {
	var valid bool
	if err := q.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, currentPrivilegesSQL, allowOperator, config.MinimumDatabaseConnections, config.MaximumRoleConnections, TableCount)).Scan(&valid); err != nil {
		return queryFailed(err, "DATABASE_RUNTIME_PRIVILEGES_INCOMPATIBLE")
	}
	if !valid {
		return errors.New("DATABASE_RUNTIME_PRIVILEGES_INCOMPATIBLE")
	}
	return nil
}

// CheckRuntimeWithin is the full serving admission/readiness
// predicate. The caller supplies its original selected store/session; superuser
// operator credentials never become a serving principal through this entrypoint.
func CheckRuntimeWithin(ctx context.Context, q transaction.Executor) error {
	if err := CheckWithin(ctx, q); err != nil {
		return err
	}
	return checkCurrentPrivileges(ctx, q, false)
}
