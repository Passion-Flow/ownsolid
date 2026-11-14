package mysqlmigrate

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

// installationSession verifies the caller's already exclusively held session.
// It never acquires, releases or transfers the installation lock.
func installationSession(ctx context.Context, connection *sql.Conn, catalog Catalog, options Options) (string, error) {
	if ctx == nil || connection == nil || catalog.version == "" || len(catalog.steps) == 0 || len(catalog.steps) > options.MaximumSteps {
		return "", fail(CodeConfigurationInvalid)
	}
	if e := options.Validate(); e != nil {
		return "", e
	}
	if _, ok := ctx.Deadline(); !ok {
		return "", fail(CodeOptionsInvalid)
	}
	var database, version, zone, charset, collation, isolation, principal, modes string
	var autocommit int
	if e := connection.QueryRowContext(ctx, "SELECT DATABASE(),VERSION(),@@session.time_zone,@@session.character_set_connection,@@session.collation_connection,@@session.transaction_isolation,@@session.autocommit,CURRENT_USER(),@@session.sql_mode").Scan(&database, &version, &zone, &charset, &collation, &isolation, &autocommit, &principal, &modes); e != nil {
		return "", mysqlstore.SafeError(e)
	}
	if version != catalog.version || zone != "+00:00" || charset != mysqlstore.ConnectionCharset || collation != mysqlstore.ConnectionCollation || isolation != mysqlstore.TransactionIsolation || autocommit != 1 || !authenticatedDefiner.MatchString(principal) {
		return "", fail(CodeSessionInvalid)
	}
	actualModes := map[string]bool{}
	for _, mode := range strings.Split(modes, ",") {
		actualModes[mode] = true
	}
	for _, mode := range strings.Split(mysqlstore.SessionSQLModes, ",") {
		if !actualModes[mode] {
			return "", fail(CodeSessionInvalid)
		}
	}
	var status, cipher string
	if e := connection.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&status, &cipher); e != nil || status != "Ssl_cipher" || cipher == "" {
		return "", fail(CodeSessionInvalid)
	}
	lock, e := LockName(database)
	if e != nil {
		return "", e
	}
	var owns sql.NullBool
	if e = connection.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?)=CONNECTION_ID()", lock).Scan(&owns); e != nil || !owns.Valid || !owns.Bool {
		return "", fail(CodeLockOutcomeUnknown)
	}
	return principal, nil
}

// VerifyInstallationLock proves only the caller-owned current session/TLS/lock.
// Fresh-target migration/restore still needs its own actual schema/data proof.
func VerifyInstallationLock(ctx context.Context, connection *sql.Conn, catalog Catalog, options Options) error {
	_, e := installationSession(ctx, connection, catalog, options)
	return e
}

// VerifyInstallationSession performs the strict operator metadata inspection on
// the exact caller-owned locked connection. Trigger definer/DDL checks are the
// unchanged migration checks; a runtime DML identity is not an operator identity.
func VerifyInstallationSession(ctx context.Context, connection *sql.Conn, catalog Catalog, options Options) (Report, error) {
	principal, e := installationSession(ctx, connection, catalog, options)
	if e != nil {
		return Report{}, e
	}
	if _, e = connection.ExecContext(ctx, "SET SESSION sql_quote_show_create=1"); e != nil {
		return Report{}, mysqlstore.SafeError(e)
	}
	report, e := inspect(ctx, connection, catalog, options, principal)
	if e == nil && !report.Ready {
		e = fail(CodeSchemaNotReady)
	}
	return report, e
}

func runtimeLedgerEntry(catalog Catalog, index, step int, source, object, definition, state string, started time.Time, applied sql.NullTime) error {
	if index >= len(catalog.steps) || step != index+1 || started.IsZero() || !applied.Valid || applied.Time.Before(started) || state != Applied {
		return fail(CodeJournalInvalid)
	}
	item := catalog.steps[index]
	if source != item.SourceSHA256 || object != item.Object || definition != item.DefinitionSHA256 {
		return fail(CodeChecksumMismatch)
	}
	return nil
}

// VerifyRuntimeLedger verifies the complete active source/checksum/applied ledger
// using the real DML identity and its held installation session. It does not
// inspect object/trigger metadata and grants no DDL permission or migration power.
func VerifyRuntimeLedger(ctx context.Context, connection *sql.Conn, catalog Catalog, options Options) (Report, error) {
	if _, e := installationSession(ctx, connection, catalog, options); e != nil {
		return Report{}, e
	}
	report := Report{Version: catalog.version, Scope: catalog.scope, TotalSteps: len(catalog.steps)}
	rows, e := connection.QueryContext(ctx, "SELECT step,source_sha256,object_name,object_sha256,state,started_at,applied_at FROM schema_migrations ORDER BY step LIMIT ?", len(catalog.steps)+1)
	if e != nil {
		return report, mysqlstore.SafeError(e)
	}
	defer rows.Close()
	for rows.Next() {
		var step int
		var source, object, definition, state string
		var started time.Time
		var applied sql.NullTime
		if rows.Scan(&step, &source, &object, &definition, &state, &started, &applied) != nil {
			return report, fail(CodeJournalInvalid)
		}
		if e = runtimeLedgerEntry(catalog, report.AppliedSteps, step, source, object, definition, state, started, applied); e != nil {
			return report, e
		}
		report.AppliedSteps++
	}
	if e = rows.Err(); e != nil {
		return report, mysqlstore.SafeError(e)
	}
	if report.AppliedSteps != len(catalog.steps) {
		return report, fail(CodeSchemaNotReady)
	}
	report.Ready = true
	return report, nil
}
