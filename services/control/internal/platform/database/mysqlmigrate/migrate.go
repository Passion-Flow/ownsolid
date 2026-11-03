package mysqlmigrate

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

type Mode string

const (
	Apply     Mode = "apply"
	Reconcile Mode = "reconcile"
	Readiness Mode = "readiness"
	Applying       = "applying"
	Applied        = "applied"
	Verified       = "verified"
)

type Options struct {
	LockWaitSeconds      int `json:"lock_wait_seconds"`
	StepSeconds          int `json:"step_seconds"`
	TotalSeconds         int `json:"total_seconds"`
	CleanupSeconds       int `json:"cleanup_seconds"`
	MaximumSteps         int `json:"maximum_steps"`
	MaximumMetadataBytes int `json:"maximum_metadata_bytes"`
}

func (o Options) Validate() error {
	if o.LockWaitSeconds < 1 || o.LockWaitSeconds > 60 || o.StepSeconds < 1 || o.StepSeconds > 300 || o.TotalSeconds < o.LockWaitSeconds+o.StepSeconds || o.TotalSeconds > 1800 || o.CleanupSeconds < 1 || o.CleanupSeconds > 30 || o.MaximumSteps < 1 || o.MaximumSteps > maximumCatalogSteps || o.MaximumMetadataBytes < 4096 || o.MaximumMetadataBytes > maximumStatementBytes {
		return fail(CodeOptionsInvalid)
	}
	return nil
}

type Event struct {
	Step  int    `json:"step"`
	Stage string `json:"stage"`
}
type Report struct {
	Version          string `json:"version"`
	Scope            string `json:"scope"`
	AppliedSteps     int    `json:"applied_steps"`
	TotalSteps       int    `json:"total_steps"`
	DirtyStep        int    `json:"dirty_step"`
	Ready            bool   `json:"ready"`
	LockReleased     bool   `json:"lock_released"`
	PendingObjectSHA string `json:"pending_object_sha256,omitempty"`
}

func fail(code string) error { return errors.New(code) }

// LockName belongs to the cooperating installation/migration protocol. MySQL
// user locks are server-wide,so the validated actual database scopes this name.
func LockName(database string) (string, error) {
	if !identifier.MatchString(database) {
		return "", fail(CodeDatabaseInvalid)
	}
	return "projectone.installation." + hash(database)[:32], nil
}

func discard(connection *sql.Conn) {
	// Returning ErrBadConn makes database/sql close the underlying session.
	// Conn.Close alone would return it to the pool with an uncertain user lock.
	_ = connection.Raw(func(any) error { return sqldriver.ErrBadConn })
}

func metadata(ctx context.Context, connection *sql.Conn, object string, limit int) (string, error) {
	if !identifier.MatchString(object) {
		return "", invalidCatalog()
	}
	var name, definition string
	if e := connection.QueryRowContext(ctx, "SHOW CREATE TABLE `"+object+"`").Scan(&name, &definition); e != nil {
		return "", mysqlstore.SafeError(e)
	}
	if name != object || len(definition) > limit {
		return "", fail(CodeMetadataInvalid)
	}
	return hash(canonicalDefinition(definition)), nil
}
func tables(ctx context.Context, connection *sql.Conn, limit int) (map[string]bool, error) {
	rows, e := connection.QueryContext(ctx, "SELECT table_name,table_type FROM information_schema.tables WHERE table_schema=DATABASE() ORDER BY table_name LIMIT ?", limit+2)
	if e != nil {
		return nil, mysqlstore.SafeError(e)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name, kind string
		if rows.Scan(&name, &kind) != nil || kind != "BASE TABLE" || !identifier.MatchString(name) || len(out) > limit {
			return nil, fail(CodeUnjournaledObject)
		}
		out[name] = true
	}
	return out, mysqlstore.SafeError(rows.Err())
}

func postcondition(ctx context.Context, connection *sql.Conn, item entry, options Options, names map[string]bool, principal string) (bool, string, error) {
	if item.Kind == createTrigger {
		observed, e := triggerMetadata(ctx, connection, item.Object, principal, options.MaximumMetadataBytes)
		return observed != "" && observed == item.DefinitionSHA256, observed, e
	}
	if item.Kind == installationSeed && item.Object == userPreferencesSeedObject {
		if !names["users"] || !names["user_preferences"] {
			return false, "", nil
		}
		var count int
		if e := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM users u LEFT JOIN user_preferences p ON p.user_id=u.id WHERE p.user_id IS NULL").Scan(&count); e != nil {
			return false, "", mysqlstore.SafeError(e)
		}
		if count == 0 {
			return true, item.DefinitionSHA256, nil
		}
		return false, "", nil
	}
	if item.Kind == installationSeed && item.Object == membershipAuthoritySeedObject {
		if !names["workspace_memberships"] {
			return false, "", nil
		}
		var count int
		if e := connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM workspace_memberships WHERE authority_id IS NULL").Scan(&count); e != nil {
			return false, "", mysqlstore.SafeError(e)
		}
		if count == 0 {
			return true, item.DefinitionSHA256, nil
		}
		return false, "", nil
	}
	if item.Kind == installationSeed {
		var count int
		query := "SELECT COUNT(*) FROM installation_state WHERE id=1"
		if item.Object == licenseStateSeedObject {
			if !names["platform_license_state"] {
				return false, "", nil
			}
			query = "SELECT COUNT(*) FROM platform_license_state WHERE id=1"
		}
		if e := connection.QueryRowContext(ctx, query).Scan(&count); e != nil {
			return false, "", mysqlstore.SafeError(e)
		}
		if count == 1 {
			return true, item.DefinitionSHA256, nil
		}
		return false, "", nil
	}
	if !names[item.Object] {
		return false, "", nil
	}
	h, e := metadata(ctx, connection, item.Object, options.MaximumMetadataBytes)
	if e != nil {
		return false, "", e
	}
	return h == item.DefinitionSHA256, h, nil
}

func latestTableSteps(catalog Catalog, count int) map[string]int {
	latest := map[string]int{}
	for i := 0; i < count; i++ {
		item := catalog.steps[i]
		if item.Kind == createTable || item.Kind == alterTable || item.Kind == createTrigger {
			latest[item.Object] = i
		}
	}
	return latest
}

func inspect(ctx context.Context, connection *sql.Conn, catalog Catalog, options Options, principal string) (Report, error) {
	report := Report{Version: catalog.version, Scope: catalog.scope, TotalSteps: len(catalog.steps)}
	names, e := tables(ctx, connection, options.MaximumSteps)
	if e != nil {
		return report, e
	}
	triggers, e := triggerNames(ctx, connection, options.MaximumSteps)
	if e != nil {
		return report, e
	}
	if !names[journalObject] {
		if len(names) != 0 || len(triggers) != 0 {
			return report, fail(CodeUnjournaledObject)
		}
		return report, nil
	}
	actual, e := metadata(ctx, connection, journalObject, options.MaximumMetadataBytes)
	if e != nil || actual != catalog.journal.DefinitionSHA256 {
		return report, fail(CodeJournalDefinitionInvalid)
	}
	rows, e := connection.QueryContext(ctx, "SELECT step,source_sha256,object_name,object_sha256,state,started_at,applied_at FROM schema_migrations ORDER BY step LIMIT ?", options.MaximumSteps+1)
	if e != nil {
		return report, mysqlstore.SafeError(e)
	}
	var states []string
	for rows.Next() {
		var step int
		var source, object, definition, state string
		var started time.Time
		var applied sql.NullTime
		if rows.Scan(&step, &source, &object, &definition, &state, &started, &applied) != nil || step != len(states)+1 || step > len(catalog.steps) || started.IsZero() || state != Applying && state != Applied || (state == Applied) != applied.Valid || report.DirtyStep != 0 {
			rows.Close()
			return report, fail(CodeJournalInvalid)
		}
		item := catalog.steps[step-1]
		if source != item.SourceSHA256 || object != item.Object || definition != item.DefinitionSHA256 {
			rows.Close()
			return report, fail(CodeChecksumMismatch)
		}
		if state == Applying {
			report.DirtyStep = step
		} else {
			report.AppliedSteps++
		}
		states = append(states, state)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return report, mysqlstore.SafeError(e)
	}
	allowed := map[string]bool{journalObject: true}
	allowedTriggers := map[string]bool{}
	latest := latestTableSteps(catalog, len(states))
	for object, index := range latest {
		if catalog.steps[index].Kind == createTrigger {
			allowedTriggers[object] = true
		} else {
			allowed[object] = true
		}
	}
	for i, state := range states {
		item := catalog.steps[i]
		if item.Kind != installationSeed && latest[item.Object] != i {
			continue
		}
		valid, observed, e := postcondition(ctx, connection, item, options, names, principal)
		if e != nil {
			return report, e
		}
		if state == Applying {
			report.PendingObjectSHA = observed
			if item.Kind == alterTable && (observed == "" || !valid && observed != item.PreviousDefinitionSHA256) || (item.Kind == createTable || item.Kind == createTrigger) && observed != "" && !valid {
				return report, fail(CodeDefinitionDrift)
			}
		} else if !valid {
			return report, fail(CodeDefinitionDrift)
		}
	}
	for name := range names {
		if !allowed[name] {
			return report, fail(CodeUnjournaledObject)
		}
	}
	for name := range triggers {
		if !allowedTriggers[name] {
			return report, fail(CodeUnjournaledObject)
		}
	}
	report.Ready = report.DirtyStep == 0 && report.AppliedSteps == len(catalog.steps)
	return report, nil
}

// Run never retries an uncertain mutation. Reconcile is an explicit operator
// mode on a new exclusive lease,with exact stored source/object postconditions.
// Observer receives only durable stage metadata and can drive real operator
// progress or deterministic cancellation tests; it cannot change SQL or scope.
func Run(ctx context.Context, store *mysqlstore.Store, catalog Catalog, options Options, mode Mode, observer func(Event)) (report Report, result error) {
	if ctx == nil || store == nil || store.Pool() == nil || catalog.version == "" || len(catalog.steps) == 0 || len(catalog.steps) > options.MaximumSteps || mode != Apply && mode != Reconcile && mode != Readiness {
		return report, fail(CodeConfigurationInvalid)
	}
	if e := options.Validate(); e != nil {
		return report, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(options.TotalSeconds)*time.Second)
	defer cancel()
	if observation, e := store.Check(ctx); e != nil || observation.Version != catalog.version {
		return report, fail(CodeServerContextInvalid)
	}
	connection, e := store.Pool().Conn(ctx)
	if e != nil {
		return report, mysqlstore.SafeError(e)
	}
	defer connection.Close()
	var database, version, zone, collation, isolation, principal string
	var autocommit int
	if e = connection.QueryRowContext(ctx, "SELECT DATABASE(),VERSION(),@@session.time_zone,@@session.collation_connection,@@session.transaction_isolation,@@session.autocommit,CURRENT_USER()").Scan(&database, &version, &zone, &collation, &isolation, &autocommit, &principal); e != nil || version != catalog.version || zone != "+00:00" || collation != mysqlstore.ConnectionCollation || isolation != mysqlstore.TransactionIsolation || autocommit != 1 || !authenticatedDefiner.MatchString(principal) {
		discard(connection)
		return report, fail(CodeSessionInvalid)
	}
	if _, e = connection.ExecContext(ctx, "SET SESSION sql_quote_show_create=1"); e != nil {
		return report, mysqlstore.SafeError(e)
	}
	lock, e := LockName(database)
	if e != nil {
		return report, e
	}
	var acquired sql.NullInt64
	if e = connection.QueryRowContext(ctx, "SELECT GET_LOCK(?,?)", lock, options.LockWaitSeconds).Scan(&acquired); e != nil || !acquired.Valid {
		discard(connection)
		return report, fail(CodeLockOutcomeUnknown)
	}
	if acquired.Int64 != 1 {
		return report, fail(CodeLockTimeout)
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), time.Duration(options.CleanupSeconds)*time.Second)
		defer done()
		var released sql.NullInt64
		if e := connection.QueryRowContext(cleanup, "SELECT RELEASE_LOCK(?)", lock).Scan(&released); e != nil || !released.Valid || released.Int64 != 1 {
			discard(connection)
			result = errors.Join(result, fail(CodeLockReleaseUnconfirmed))
		} else {
			report.LockReleased = true
		}
	}()
	report, e = inspect(ctx, connection, catalog, options, principal)
	if e != nil || report.Ready {
		return report, e
	}
	if mode == Readiness {
		return report, fail(CodeSchemaNotReady)
	}
	if report.DirtyStep != 0 && mode != Reconcile {
		return report, fail(CodeReconciliationRequired)
	}
	names, e := tables(ctx, connection, options.MaximumSteps)
	if e != nil {
		return report, e
	}
	if !names[journalObject] {
		if _, e = connection.ExecContext(ctx, catalog.journal.sql); e != nil {
			return report, fail(CodeJournalOutcomeUnknown)
		}
		if actual, e := metadata(ctx, connection, journalObject, options.MaximumMetadataBytes); e != nil || actual != catalog.journal.DefinitionSHA256 {
			return report, fail(CodeJournalDefinitionInvalid)
		}
	}
	for index := report.AppliedSteps; index < len(catalog.steps); index++ {
		item := catalog.steps[index]
		step, done := context.WithTimeout(ctx, time.Duration(options.StepSeconds)*time.Second)
		e := func() error {
			defer done()
			prepared := report.DirtyStep == item.Step
			if !prepared {
				if _, e := connection.ExecContext(step, "INSERT INTO schema_migrations(step,source_sha256,object_name,object_sha256,state,started_at) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6))", item.Step, item.SourceSHA256, item.Object, item.DefinitionSHA256, Applying); e != nil {
					return fail(CodeJournalOutcomeUnknown)
				}
				report.DirtyStep = item.Step
				if observer != nil {
					observer(Event{item.Step, Applying})
				}
			}
			names, e := tables(step, connection, options.MaximumSteps)
			if e != nil {
				return e
			}
			valid, observed, e := postcondition(step, connection, item, options, names, principal)
			if e != nil {
				return e
			}
			if item.Kind == alterTable && observed == "" || observed != "" && !valid && (item.Kind != alterTable || observed != item.PreviousDefinitionSHA256) {
				return fail(CodeDefinitionDrift)
			}
			if valid && !prepared {
				query := ""
				if item.Kind == installationSeed && item.Object == membershipAuthoritySeedObject {
					query = "SELECT COUNT(*) FROM workspace_memberships"
				}
				if item.Kind == installationSeed && item.Object == userPreferencesSeedObject {
					query = "SELECT (SELECT COUNT(*) FROM users)+(SELECT COUNT(*) FROM user_preferences)"
				}
				if query == "" {
					return fail(CodeUnjournaledObject)
				}
				var count int
				if e := connection.QueryRowContext(step, query).Scan(&count); e != nil {
					return mysqlstore.SafeError(e)
				}
				if count != 0 {
					return fail(CodeUnjournaledObject)
				}
			}
			if !valid {
				if _, e := connection.ExecContext(step, item.sql); e != nil {
					if item.Kind == installationSeed && (item.Object == membershipAuthoritySeedObject || item.Object == licenseStateSeedObject || item.Object == userPreferencesSeedObject) {
						return fail(CodeDataOutcomeUnknown)
					}
					return fail(CodeDdlOutcomeUnknown)
				}
				names, e = tables(step, connection, options.MaximumSteps)
				if e != nil {
					return e
				}
				if valid, _, e := postcondition(step, connection, item, options, names, principal); e != nil || !valid {
					return fail(CodeDefinitionDrift)
				}
			}
			if observer != nil {
				observer(Event{item.Step, Verified})
			}
			updated, e := connection.ExecContext(step, "UPDATE schema_migrations SET state=?,applied_at=UTC_TIMESTAMP(6) WHERE step=? AND state=? AND source_sha256=? AND object_sha256=?", Applied, item.Step, Applying, item.SourceSHA256, item.DefinitionSHA256)
			if e != nil {
				return fail(CodeJournalOutcomeUnknown)
			}
			count, e := updated.RowsAffected()
			if e != nil || count != 1 {
				return fail(CodeJournalInvalid)
			}
			if observer != nil {
				observer(Event{item.Step, Applied})
			}
			return nil
		}()
		if e != nil {
			return report, e
		}
		report.AppliedSteps = item.Step
		report.DirtyStep = 0
	}
	return inspect(ctx, connection, catalog, options, principal)
}
