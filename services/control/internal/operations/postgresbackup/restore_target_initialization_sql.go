package postgresbackup

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This extends, and never weakens, the existing native fresh-target predicate.
// It is a fixed PostgreSQL18.6 target profile, not permission to ignore objects.
// Scope/Engine must independently exclude non-cooperating privileged writers.
const initializationPristineExtras = `NOT EXISTS(SELECT 1 FROM pg_catalog.pg_largeobject_metadata) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_largeobject) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_foreign_data_wrapper) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_foreign_server) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_user_mapping) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_default_acl) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_transform) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_db_role_setting WHERE setdatabase=(SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database())) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_collation c JOIN pg_catalog.pg_namespace n ON n.oid=c.collnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_conversion c JOIN pg_catalog.pg_namespace n ON n.oid=c.connamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_operator c JOIN pg_catalog.pg_namespace n ON n.oid=c.oprnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_opclass c JOIN pg_catalog.pg_namespace n ON n.oid=c.opcnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_opfamily c JOIN pg_catalog.pg_namespace n ON n.oid=c.opfnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_ts_config c JOIN pg_catalog.pg_namespace n ON n.oid=c.cfgnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_ts_dict c JOIN pg_catalog.pg_namespace n ON n.oid=c.dictnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_ts_parser c JOIN pg_catalog.pg_namespace n ON n.oid=c.prsnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_ts_template c JOIN pg_catalog.pg_namespace n ON n.oid=c.tmplnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_statistic_ext c JOIN pg_catalog.pg_namespace n ON n.oid=c.stxnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema') AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_language WHERE lanname NOT IN ('internal','c','sql','plpgsql')) AND EXISTS(SELECT 1 FROM pg_catalog.pg_namespace n WHERE n.nspname='public' AND n.nspowner='pg_database_owner'::pg_catalog.regrole AND n.nspacl @> ARRAY['pg_database_owner=UC/pg_database_owner','=U/pg_database_owner']::pg_catalog.aclitem[] AND n.nspacl <@ ARRAY['pg_database_owner=UC/pg_database_owner','=U/pg_database_owner']::pg_catalog.aclitem[])`

const initializationTargetIdentitySQL = `SELECT (SELECT system_identifier::text FROM pg_catalog.pg_control_system()),d.oid,d.datname,CURRENT_USER,pg_catalog.pg_backend_pid(),pg_catalog.current_setting('server_version_num')::integer,COALESCE(pg_catalog.shobj_description(d.oid,'pg_database'),''),NOT d.datistemplate AND d.datallowconn AND d.datconnlimit=-1 AND d.datacl IS NULL,(SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname=CURRENT_USER),d.datdba=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=CURRENT_USER),NOT pg_catalog.pg_is_in_recovery() AND pg_catalog.current_setting('default_transaction_read_only')='off',pg_catalog.current_setting('transaction_read_only')='on',EXISTS(SELECT 1 FROM pg_catalog.pg_locks WHERE locktype='advisory' AND pid=pg_catalog.pg_backend_pid() AND classid=0::oid AND objid=$1::bigint::oid AND objsubid=1 AND mode='ExclusiveLock' AND granted AND database=d.oid),NOT EXISTS(SELECT 1 FROM pg_catalog.pg_stat_activity WHERE datid=d.oid AND backend_type='client backend' AND pid<>pg_catalog.pg_backend_pid()) FROM pg_catalog.pg_database d WHERE d.datname=pg_catalog.current_database()`

type targetInitializationQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type targetInitializationTransaction interface {
	targetInitializationQuery
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}
type targetInitializationSession interface {
	targetInitializationQuery
	begin(context.Context, bool) (targetInitializationTransaction, error)
}

// The adapter only borrows the original selected lease. It opens no pool,
// connection, service, role or independent transaction authority.
type selectedTargetInitializationSession struct{ lease *pgxpool.Conn }

func (s selectedTargetInitializationSession) QueryRow(ctx context.Context, q string, args ...any) pgx.Row {
	return s.lease.QueryRow(ctx, q, args...)
}
func (s selectedTargetInitializationSession) begin(ctx context.Context, readonly bool) (targetInitializationTransaction, error) {
	mode := pgx.ReadWrite
	if readonly {
		mode = pgx.ReadOnly
	}
	return s.lease.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: mode})
}

func initializationPredicateSHA256() string {
	return digest([]byte(initializationTargetIdentitySQL + "\n" + emptyPostgresTarget + "\n" + initializationPristineExtras + "\n"))
}

func readTargetInitializationIdentity(ctx context.Context, q targetInitializationQuery, r release.PostgreSQLRestoreTargetInitializationRequest, readonly bool) (release.PostgreSQLRestoreTargetInitializationIdentity, error) {
	bad := errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_IDENTITY_REFUSED")
	var t release.PostgreSQLRestoreTargetInitializationIdentity
	var ordinary, super, owner, primary, actualReadonly, held, exclusive bool
	if ctx == nil || q == nil || !r.ValidForInspection() {
		return t, bad
	}
	if q.QueryRow(ctx, initializationTargetIdentitySQL, database.InstallationLockID).Scan(&t.SystemIdentifier, &t.DatabaseOID, &t.Database, &t.Operator, &t.BackendPID, &t.ServerVersion, &t.Marker, &ordinary, &super, &owner, &primary, &actualReadonly, &held, &exclusive) != nil || !ordinary || !super || !owner || !primary || !held || !exclusive || actualReadonly != readonly || t.SystemIdentifier == "" || t.DatabaseOID == 0 || t.BackendPID < 1 || t.ServerVersion != 180006 || t.Database != r.TargetDatabase || t.Operator != r.TargetOperator || r.IdentityPinned() && (t.SystemIdentifier != r.ExpectedSystemIdentifier || t.DatabaseOID != r.ExpectedDatabaseOID) {
		return release.PostgreSQLRestoreTargetInitializationIdentity{}, bad
	}
	var pristine bool
	if q.QueryRow(ctx, "SELECT ("+emptyPostgresTarget+") AND ("+initializationPristineExtras+")").Scan(&pristine) != nil || !pristine {
		return release.PostgreSQLRestoreTargetInitializationIdentity{}, errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_PRISTINE_REQUIRED")
	}
	return t, nil
}

func initializationComment(r release.PostgreSQLRestoreTargetInitializationRequest) (string, error) {
	if !r.Valid() {
		return "", errors.New("POSTGRES_RESTORE_TARGET_INITIALIZATION_PIN_REQUIRED")
	}
	// Both identifiers have already passed fixed contract validation. The only
	// string literal is the fixed prefix plus a lowercase hex32 scope identifier.
	return "COMMENT ON DATABASE " + pgx.Identifier{r.TargetDatabase}.Sanitize() + " IS '" + r.Marker() + "'", nil
}
