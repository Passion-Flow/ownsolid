//go:build cgo && odbc_manager

package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	googleuuid "github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
)

type nativeBackend struct {
	connection    *sql.Conn
	configuration dameng.SchemaConfiguration
	catalog       catalog
}

func (n *nativeBackend) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(n.configuration.StepSeconds)*time.Second)
}
func maxSQL(c catalog) int {
	maximum := len(c.journal.ddl())
	for _, probe := range expressionCapabilities() {
		maximum = max(maximum, len(probe.query))
	}
	for _, s := range c.steps {
		maximum = max(maximum, len(s.SQL))
		if change, ok := c.evolution[s.Number]; ok {
			maximum = max(maximum, len(change.Probe))
		}
	}
	return max(2048, maximum)
}

// Run uses one pinned native connection. A logical pool slot never substitutes
// for Store ownership: callers must retain/close kernel on success AND failure.
func Run(ctx context.Context, kernel *dameng.Store, configuration dameng.SchemaConfiguration, mode Mode) (report Report, result error) {
	if mode != Apply && mode != Readiness && mode != Reconcile && mode != Retry {
		return Report{Scope: Scope}, fail("DAMENG_SCHEMA_MODE_INVALID")
	}
	return withOwner(ctx, kernel, configuration, func(ctx context.Context, n *nativeBackend) (Report, error) { return run(ctx, n.catalog, n, mode) })
}
func withOwner(ctx context.Context, kernel *dameng.Store, configuration dameng.SchemaConfiguration, operation func(context.Context, *nativeBackend) (Report, error)) (report Report, result error) {
	c, e := defaultCatalog()
	report = Report{Scope: Scope, TotalSteps: len(c.steps)}
	if e != nil {
		return report, e
	}
	if ctx == nil {
		return report, fail("DAMENG_SCHEMA_CONTEXT_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return report, fail("DAMENG_SCHEMA_DEADLINE_REQUIRED")
	}
	if configuration.MaximumMetadataRows < len(c.steps) {
		return report, fail("DAMENG_SCHEMA_METADATA_ROW_BUDGET_REQUIRED")
	}
	if e = kernel.ValidateSchemaUse(configuration, false, maxSQL(c)); e != nil {
		return report, fail("DAMENG_SCHEMA_CONNECTION_POLICY_MISMATCH")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(configuration.TotalSeconds)*time.Second)
	defer cancel()
	pool, e := kernel.BorrowPool()
	if e != nil {
		return report, fail("DAMENG_SCHEMA_CONNECTION_UNAVAILABLE")
	}
	connection, e := pool.Conn(ctx)
	if e != nil {
		return report, fail("DAMENG_SCHEMA_CONNECTION_UNAVAILABLE")
	}
	n := &nativeBackend{connection, configuration, c}
	locked := false
	defer func() {
		if result != nil {
			report.FoundationReady = false
			report.RuntimeReady = false
		}
		if locked {
			cleanup, stop := context.WithTimeout(context.Background(), time.Duration(configuration.CleanupSeconds)*time.Second)
			var code int
			e := connection.QueryRowContext(cleanup, "SELECT DBMS_LOCK.RELEASE(?) FROM DUAL", lockID(configuration.Schema)).Scan(&code)
			stop()
			if e == nil && code == 0 {
				report.LockReleased = true
			} else {
				report.FoundationReady = false
				report.RuntimeReady = false
				result = fail("DAMENG_SCHEMA_LOCK_RELEASE_UNCERTAIN")
			}
		}
		if connection.Close() != nil {
			report.FoundationReady = false
			report.RuntimeReady = false
			result = fail("DAMENG_SCHEMA_SESSION_CLOSE_UNCERTAIN")
		}
	}()
	if e = n.ownerSession(ctx); e != nil {
		return report, e
	}
	if e = n.expressionCapabilities(ctx); e != nil {
		return report, e
	}
	if e = n.metadataCapabilities(ctx); e != nil {
		return report, e
	}
	lockContext, stop := context.WithTimeout(ctx, time.Duration(configuration.LockWaitSeconds+configuration.StepSeconds)*time.Second)
	var code int
	e = connection.QueryRowContext(lockContext, "SELECT DBMS_LOCK.REQUEST(?,6,?,FALSE) FROM DUAL", lockID(configuration.Schema), configuration.LockWaitSeconds).Scan(&code)
	stop()
	if e != nil || code != 0 {
		return report, fail("DAMENG_SCHEMA_LOCK_UNAVAILABLE")
	}
	locked = true
	return operation(ctx, n)
}
func (n *nativeBackend) ownerSession(ctx context.Context) error {
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	var user, schema string
	var autocommit, caseSensitive int
	if n.connection.QueryRowContext(ctx, "SELECT SYS_CONTEXT('USERENV','SESSION_USER'),SYS_CONTEXT('USERENV','CURRENT_SCHEMA'),SF_GET_PARA_VALUE(1,'DDL_AUTO_COMMIT'),CASE_SENSITIVE() FROM DUAL").Scan(&user, &schema, &autocommit, &caseSensitive) != nil || user != n.configuration.Schema || schema != n.configuration.Schema || autocommit != 1 || caseSensitive != 1 {
		return fail("DAMENG_SCHEMA_OWNER_OR_SERVER_PROFILE_INVALID")
	}
	return nil
}

func (n *nativeBackend) expressionCapabilities(ctx context.Context) error {
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	return checkExpressionCapabilities(ctx, func(ctx context.Context, query string) (sql.NullString, error) {
		var value sql.NullString
		rows, e := n.connection.QueryContext(ctx, query)
		if e != nil {
			return value, e
		}
		defer rows.Close()
		if !rows.Next() || rows.Scan(&value) != nil || rows.Next() || rows.Err() != nil {
			return sql.NullString{}, fail("DAMENG_SCHEMA_EXPRESSION_CAPABILITY_UNAVAILABLE")
		}
		if e = rows.Close(); e != nil {
			return sql.NullString{}, e
		}
		return value, nil
	})
}

// Probe the exact dictionary projection before even journal bootstrap. Missing
// view/version capabilities are an implementation/target compatibility boundary,
// never permission to create first and waive readback afterward.
func (n *nativeBackend) metadataCapabilities(ctx context.Context) error {
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	for _, query := range []string{
		"SELECT OBJECT_NAME,OBJECT_TYPE FROM ALL_OBJECTS WHERE 1=0",
		"SELECT ID,TABLEID,TYPE$,VALID,INDEXID,FINDEXID,FACTION,TRIGID FROM SYS.SYSCONS WHERE 1=0",
		"SELECT NAME,ID,SCHID,TYPE$,SUBTYPE$ FROM SYS.SYSOBJECTS WHERE 1=0",
		"SELECT COLUMN_NAME,DATA_TYPE,CHAR_LENGTH,CHAR_USED,DATA_LENGTH,DATA_SCALE,NULLABLE,DATA_DEFAULT,VIRTUAL_COLUMN,IDENTITY_COLUMN FROM ALL_TAB_COLUMNS WHERE 1=0",
		"SELECT CONSTRAINT_NAME,CONSTRAINT_TYPE,SEARCH_CONDITION,R_OWNER,R_CONSTRAINT_NAME,DELETE_RULE,STATUS,DEFERRABLE,VALIDATED,INDEX_NAME FROM ALL_CONSTRAINTS WHERE 1=0",
		"SELECT COLUMN_NAME FROM ALL_CONS_COLUMNS WHERE 1=0",
		"SELECT TRIGGER_NAME,TABLE_NAME,TRIGGERING_EVENT,TRIGGER_TYPE,TRIGGER_BODY,STATUS FROM ALL_TRIGGERS WHERE 1=0",
		"SELECT DBMS_METADATA.GET_DDL('TRIGGER',TRIGGER_NAME,OWNER) FROM ALL_TRIGGERS WHERE 1=0",
		"SELECT INDEX_NAME,TABLE_NAME,UNIQUENESS,STATUS FROM ALL_INDEXES WHERE 1=0",
		"SELECT COLUMN_NAME,DESCEND FROM ALL_IND_COLUMNS WHERE 1=0",
	} {
		rows, e := n.connection.QueryContext(ctx, query)
		if e != nil {
			return fail("DAMENG_SCHEMA_METADATA_CAPABILITY_UNAVAILABLE")
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			return fail("DAMENG_SCHEMA_METADATA_CAPABILITY_UNAVAILABLE")
		}
		if rows.Close() != nil {
			return fail("DAMENG_SCHEMA_METADATA_CAPABILITY_UNAVAILABLE")
		}
	}
	return nil
}
func (n *nativeBackend) exec(ctx context.Context, query string, args ...any) error {
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	if _, e := n.connection.ExecContext(ctx, query, args...); e != nil {
		return fail("DAMENG_SCHEMA_MUTATION_UNCERTAIN")
	}
	return nil
}
func (n *nativeBackend) change(ctx context.Context, query string, args ...any) error {
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	r, e := n.connection.ExecContext(ctx, query, args...)
	if e != nil {
		return fail("DAMENG_SCHEMA_JOURNAL_WRITE_UNCERTAIN")
	}
	count, e := r.RowsAffected()
	if e != nil || count != 1 {
		return fail("DAMENG_SCHEMA_JOURNAL_WRITE_UNCERTAIN")
	}
	return nil
}
func (n *nativeBackend) createJournal(ctx context.Context) error {
	return n.exec(ctx, n.catalog.journal.ddl())
}
func (n *nativeBackend) execute(ctx context.Context, s step) error {
	if x, ok := n.catalog.evolution[s.Number]; ok && x.UUIDParameters == 1 {
		return n.exec(ctx, s.SQL, googleuuid.NewString())
	}
	return n.exec(ctx, s.SQL)
}
func (n *nativeBackend) start(ctx context.Context, s step) error {
	return n.change(ctx, `INSERT INTO "PO_SCHEMA_MIGRATIONS" ("STEP_NO","SCOPE","SOURCE_SHA256","OBJECT_NAME","STATE") VALUES (?,?,?,?,'applying')`, s.Number, Scope, s.SHA256, s.Object)
}
func (n *nativeBackend) complete(ctx context.Context, s step) error {
	return n.change(ctx, `UPDATE "PO_SCHEMA_MIGRATIONS" SET "STATE"='applied',"APPLIED_AT"=CURRENT_TIMESTAMP(6) WHERE "STEP_NO"=? AND "SCOPE"=? AND "SOURCE_SHA256"=? AND "OBJECT_NAME"=? AND "STATE"='applying' AND "APPLIED_AT" IS NULL`, s.Number, Scope, s.SHA256, s.Object)
}

// Journal timestamps are wall-clock observations, not the ordering authority.
// A server clock correction must not strand an otherwise verified applied step.
// assess still validates the state, contiguous sequence, source and object proof.
func readJournalRows(rows *sql.Rows) ([]journalEntry, error) {
	var entries []journalEntry
	for rows.Next() {
		var entry journalEntry
		var started time.Time
		var applied sql.NullTime
		if rows.Scan(&entry.Step, &entry.Scope, &entry.Source, &entry.Object, &entry.State, &started, &applied) != nil || len(entries) >= maximumCatalogSteps {
			return nil, fail("DAMENG_SCHEMA_JOURNAL_INVALID")
		}
		entry.Started = !started.IsZero()
		entry.Completed = applied.Valid
		if applied.Valid && applied.Time.IsZero() {
			return nil, fail("DAMENG_SCHEMA_JOURNAL_INVALID")
		}
		entries = append(entries, entry)
	}
	if rows.Err() != nil {
		return nil, fail("DAMENG_SCHEMA_JOURNAL_INVALID")
	}
	return entries, nil
}

func nativeType(t string) string {
	t = strings.ToUpper(t)
	switch t {
	case "INT":
		return "INTEGER"
	case "VARCHAR2":
		return "VARCHAR"
	}
	return t
}
func (n *nativeBackend) verifyTable(ctx context.Context, t table) (map[string]bool, error) {
	implicit := map[string]bool{}
	rows, e := n.connection.QueryContext(ctx, "SELECT COLUMN_NAME,DATA_TYPE,CHAR_LENGTH,CHAR_USED,DATA_LENGTH,DATA_SCALE,NULLABLE,DATA_DEFAULT,VIRTUAL_COLUMN,IDENTITY_COLUMN FROM ALL_TAB_COLUMNS WHERE OWNER=? AND TABLE_NAME=? ORDER BY COLUMN_ID", n.configuration.Schema, t.Name)
	if e != nil {
		return nil, fail("DAMENG_SCHEMA_COLUMN_METADATA_UNAVAILABLE")
	}
	i := 0
	for rows.Next() {
		var name, kind, nullable, virtual, identity string
		var charUsed, def sql.NullString
		var chars, length, scale sql.NullInt64
		if i >= len(t.Columns) || rows.Scan(&name, &kind, &chars, &charUsed, &length, &scale, &nullable, &def, &virtual, &identity) != nil {
			rows.Close()
			return nil, fail("DAMENG_SCHEMA_COLUMN_DRIFT")
		}
		c := t.Columns[i]
		i++
		wantNullable := "N"
		if c.Nullable {
			wantNullable = "Y"
		}
		wantVirtual := "NO"
		expectedDefault := c.Default
		if c.Virtual != "" {
			wantVirtual = "YES"
			expectedDefault = c.Virtual
		}
		widthOK := true
		if c.Characters {
			widthOK = chars.Valid && chars.Int64 == int64(c.Width) && charUsed.String == "C"
		} else if c.Type == "CHAR" || c.Type == "VARBINARY" {
			widthOK = length.Valid && length.Int64 == int64(c.Width)
		} else if c.Type == "TIMESTAMP" {
			widthOK = scale.Valid && scale.Int64 == int64(c.Width)
		}
		actualType := nativeType(kind)
		typeOK := actualType == c.Type || c.Type == "TIMESTAMP" && actualType == typeName(c)
		if name != c.Name || !typeOK || !widthOK || nullable != wantNullable || virtual != wantVirtual || identity != "NO" || len(def.String) > n.configuration.MaximumMetadataBytes || expression(def.String) != expression(expectedDefault) {
			rows.Close()
			return nil, fail("DAMENG_SCHEMA_COLUMN_DRIFT")
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, fail("DAMENG_SCHEMA_COLUMN_METADATA_UNAVAILABLE")
	}
	if i != len(t.Columns) {
		return nil, fail("DAMENG_SCHEMA_COLUMN_DRIFT")
	}
	rows, e = n.connection.QueryContext(ctx, "SELECT CONSTRAINT_NAME,CONSTRAINT_TYPE,SEARCH_CONDITION,R_OWNER,R_CONSTRAINT_NAME,DELETE_RULE,STATUS,DEFERRABLE,VALIDATED,INDEX_NAME FROM ALL_CONSTRAINTS WHERE OWNER=? AND TABLE_NAME=? ORDER BY CONSTRAINT_NAME", n.configuration.Schema, t.Name)
	if e != nil {
		return nil, fail("DAMENG_SCHEMA_CONSTRAINT_METADATA_UNAVAILABLE")
	}
	expected := map[string]constraint{}
	for _, c := range t.Constraints {
		expected[c.Name] = c
	}
	seen := map[string]bool{}
	// Rows are eagerly materialized by the native driver, but close the logical
	// result before issuing secondary metadata queries on this pinned session.
	var columns []constraint
	for rows.Next() {
		var name, kind, status, deferrable, validated string
		var condition, owner, reference, rule, indexName sql.NullString
		if rows.Scan(&name, &kind, &condition, &owner, &reference, &rule, &status, &deferrable, &validated, &indexName) != nil || seen[name] || status != "ENABLED" || deferrable != "NOT DEFERRABLE" || validated != "VALIDATED" || len(condition.String) > n.configuration.MaximumMetadataBytes {
			rows.Close()
			return nil, fail("DAMENG_SCHEMA_CONSTRAINT_DRIFT")
		}
		want, ok := expected[name]
		if !ok {
			allowed := false
			if kind == "C" {
				for _, c := range t.Columns {
					if !c.Nullable && expression(condition.String) == expression(quoted(c.Name)+" IS NOT NULL") {
						allowed = true
					}
				}
			}
			if !allowed {
				rows.Close()
				return nil, fail("DAMENG_SCHEMA_CONSTRAINT_DRIFT")
			}
			continue
		}
		seen[name] = true
		if kind != want.Kind || kind == "C" && expression(condition.String) != expression(want.Check) || kind == "R" && (owner.String != n.configuration.Schema || reference.String != want.ReferenceKey || !deleteRuleMatches(n.catalog.deleteRules[name], rule.String)) {
			rows.Close()
			return nil, fail("DAMENG_SCHEMA_CONSTRAINT_DRIFT")
		}
		if kind == "P" || kind == "U" {
			if !identifier.MatchString(indexName.String) {
				rows.Close()
				return nil, fail("DAMENG_SCHEMA_CONSTRAINT_DRIFT")
			}
			implicit[indexName.String] = true
		}
		if kind != "C" {
			columns = append(columns, want)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, fail("DAMENG_SCHEMA_CONSTRAINT_METADATA_UNAVAILABLE")
	}
	if len(seen) != len(expected) {
		return nil, fail("DAMENG_SCHEMA_CONSTRAINT_DRIFT")
	}
	for _, c := range columns {
		rows, e = n.connection.QueryContext(ctx, "SELECT COLUMN_NAME FROM ALL_CONS_COLUMNS WHERE OWNER=? AND CONSTRAINT_NAME=? AND TABLE_NAME=? ORDER BY POSITION", n.configuration.Schema, c.Name, t.Name)
		if e != nil {
			return nil, fail("DAMENG_SCHEMA_CONSTRAINT_METADATA_UNAVAILABLE")
		}
		i := 0
		for rows.Next() {
			var name string
			if i >= len(c.Columns) || rows.Scan(&name) != nil || name != c.Columns[i] {
				rows.Close()
				return nil, fail("DAMENG_SCHEMA_CONSTRAINT_DRIFT")
			}
			i++
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, fail("DAMENG_SCHEMA_CONSTRAINT_METADATA_UNAVAILABLE")
		}
		if i != len(c.Columns) {
			return nil, fail("DAMENG_SCHEMA_CONSTRAINT_DRIFT")
		}
	}
	return implicit, nil
}
func (n *nativeBackend) verifyIndexes(ctx context.Context, present map[string]bool, implicit map[string]bool) error {
	expected := map[string]index{}
	for _, s := range n.catalog.steps {
		if s.Index != nil {
			expected[s.Object] = *s.Index
		}
	}
	rows, e := n.connection.QueryContext(ctx, "SELECT INDEX_NAME,TABLE_NAME,UNIQUENESS,STATUS FROM ALL_INDEXES WHERE OWNER=? ORDER BY INDEX_NAME", n.configuration.Schema)
	if e != nil {
		return fail("DAMENG_SCHEMA_INDEX_METADATA_UNAVAILABLE")
	}
	var found []index
	for rows.Next() {
		var name, table, uniqueness, status string
		if rows.Scan(&name, &table, &uniqueness, &status) != nil || status != "VALID" {
			rows.Close()
			return fail("DAMENG_SCHEMA_INDEX_DRIFT")
		}
		if implicit[name] {
			continue
		}
		want, ok := expected[name]
		if !ok || want.Table != table || want.Unique != (uniqueness == "UNIQUE") || uniqueness != "UNIQUE" && uniqueness != "NONUNIQUE" {
			rows.Close()
			return fail("DAMENG_SCHEMA_INDEX_DRIFT")
		}
		found = append(found, want)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return fail("DAMENG_SCHEMA_INDEX_DRIFT")
	}
	for _, want := range found {
		rows, e = n.connection.QueryContext(ctx, "SELECT COLUMN_NAME,DESCEND FROM ALL_IND_COLUMNS WHERE INDEX_OWNER=? AND INDEX_NAME=? AND TABLE_OWNER=? AND TABLE_NAME=? ORDER BY COLUMN_POSITION", n.configuration.Schema, want.Name, n.configuration.Schema, want.Table)
		if e != nil {
			return fail("DAMENG_SCHEMA_INDEX_METADATA_UNAVAILABLE")
		}
		i := 0
		for rows.Next() {
			var name, order string
			if i >= len(want.Columns) || rows.Scan(&name, &order) != nil || name != want.Columns[i] || order != "ASC" && order != "DESC" || want.Descending[i] != (order == "DESC") {
				rows.Close()
				return fail("DAMENG_SCHEMA_INDEX_DRIFT")
			}
			i++
		}
		e = rows.Err()
		rows.Close()
		if e != nil || i != len(want.Columns) {
			return fail("DAMENG_SCHEMA_INDEX_DRIFT")
		}
		present[want.Name] = true
	}
	return nil
}

// Native identifiers enter only after typed validation. No caller-provided SQL
// or generic dialect substitution is accepted by this operator.
func objectSQL(schema, object string) string {
	return fmt.Sprintf("%s.%s", quoted(schema), quoted(object))
}
