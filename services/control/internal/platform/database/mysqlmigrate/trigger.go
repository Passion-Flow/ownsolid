package mysqlmigrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const createTrigger = "create_trigger"
const triggerDefinitionFormat = "projectone.mysql.trigger-definition.v1"

var authenticatedDefiner = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,31}@[A-Za-z0-9_.:%-]{1,255}$`)

// Definer is inspected separately against authenticated migration context. It is
// never discarded or inferred from the object whose integrity is being checked.
type triggerDefinition struct {
	Format              string   `json:"format"`
	Name                string   `json:"name"`
	Table               string   `json:"table"`
	Timing              string   `json:"timing"`
	Event               string   `json:"event"`
	Orientation         string   `json:"orientation"`
	Order               int      `json:"order"`
	Statement           string   `json:"statement"`
	SQLModes            []string `json:"sql_modes"`
	CharacterSetClient  string   `json:"character_set_client"`
	CollationConnection string   `json:"collation_connection"`
	DatabaseCollation   string   `json:"database_collation"`
}

func canonicalTrigger(raw string) (triggerDefinition, string, error) {
	var d triggerDefinition
	if len(raw) == 0 || len(raw) > maximumStatementBytes || strict.DecodeStrict([]byte(raw), &d) != nil || d.Format != triggerDefinitionFormat || !identifier.MatchString(d.Name) || !identifier.MatchString(d.Table) || d.Name == d.Table || !ownedTriggerPosition(d) || d.Event != "INSERT" && d.Event != "UPDATE" && d.Event != "DELETE" || d.Orientation != "ROW" || !utf8.ValidString(d.Statement) || strings.ContainsAny(d.Statement, "\x00\r") || !strings.HasPrefix(d.Statement, "BEGIN\n") || !strings.HasSuffix(d.Statement, "\nEND") || strings.Contains(d.Statement, "\nEND;") || d.CharacterSetClient != mysqlstore.ConnectionCharset || d.CollationConnection != mysqlstore.ConnectionCollation || d.DatabaseCollation != mysqlstore.ConnectionCollation {
		return d, "", invalidCatalog()
	}
	ownedModes := strings.Split(mysqlstore.SessionSQLModes, ",")
	slices.Sort(ownedModes)
	slices.Sort(d.SQLModes)
	if !slices.Equal(ownedModes, d.SQLModes) {
		return d, "", invalidCatalog()
	}
	encoded, e := json.Marshal(d)
	if e != nil || len(encoded) > maximumStatementBytes {
		return d, "", invalidCatalog()
	}
	return d, string(encoded), nil
}

// Only exact owned first-party patterns extend the original BEFORE/order1
// contract. Candidate support is not a claim that new metadata was measured. Exact source, canonical definition and authenticated definer checks
// still bind every trigger; this never admits an arbitrary ordering exception.
func ownedTriggerPosition(d triggerDefinition) bool {
	switch d.Name {
	case "workflow_run_result_identity":
		return d.Table == "workflow_runs" && d.Event == "UPDATE" && d.Timing == "BEFORE" && d.Order == 2
	case "user_preferences_initialize":
		return d.Table == "users" && d.Event == "INSERT" && d.Timing == "AFTER" && d.Order == 1
	case "workflow_terminal_notification":
		return d.Table == "workflow_runs" && d.Event == "UPDATE" && d.Timing == "AFTER" && d.Order == 1
	}
	if d.Name == "model_preparation_review_update" {
		return d.Table == "model_preparations" && d.Event == "UPDATE" && d.Timing == "BEFORE" && d.Order == 2
	}
	if d.Name == "model_publication_apply" {
		return d.Table == "model_publications" && d.Event == "INSERT" && d.Timing == "AFTER" && d.Order == 1
	}
	return d.Timing == "BEFORE" && d.Order == 1
}

func triggerSource(d triggerDefinition) string {
	return "CREATE TRIGGER " + d.Name + " " + d.Timing + " " + d.Event + " ON " + d.Table + " FOR EACH ROW\n" + d.Statement + ";"
}

func triggerNames(ctx context.Context, connection *sql.Conn, limit int) (map[string]bool, error) {
	rows, e := connection.QueryContext(ctx, "SELECT trigger_name FROM information_schema.triggers WHERE trigger_schema=DATABASE() ORDER BY trigger_name LIMIT ?", limit+1)
	if e != nil {
		return nil, mysqlstore.SafeError(e)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil || !identifier.MatchString(name) || len(names) >= limit || names[name] {
			return nil, fail(CodeUnjournaledObject)
		}
		names[name] = true
	}
	return names, mysqlstore.SafeError(rows.Err())
}

func triggerMetadata(ctx context.Context, connection *sql.Conn, object, principal string, limit int) (string, error) {
	if !identifier.MatchString(object) || !authenticatedDefiner.MatchString(principal) {
		return "", fail(CodeTriggerContextInvalid)
	}
	var d triggerDefinition
	d.Format = triggerDefinitionFormat
	var mode, definer string
	e := connection.QueryRowContext(ctx, "SELECT trigger_name,event_object_table,action_timing,event_manipulation,action_orientation,action_order,action_statement,sql_mode,definer,character_set_client,collation_connection,database_collation FROM information_schema.triggers WHERE trigger_schema=DATABASE() AND trigger_name=?", object).Scan(&d.Name, &d.Table, &d.Timing, &d.Event, &d.Orientation, &d.Order, &d.Statement, &mode, &definer, &d.CharacterSetClient, &d.CollationConnection, &d.DatabaseCollation)
	if errors.Is(e, sql.ErrNoRows) {
		return "", nil
	}
	if e != nil {
		return "", mysqlstore.SafeError(e)
	}
	if definer != principal {
		return "", fail(CodeTriggerContextInvalid)
	}
	d.SQLModes = strings.Split(mode, ",")
	raw, e := json.Marshal(d)
	if e != nil || len(raw) > limit {
		return "", fail(CodeMetadataInvalid)
	}
	_, canonical, e := canonicalTrigger(string(raw))
	if e != nil {
		return "", fail(CodeMetadataInvalid)
	}
	return hash(canonical), nil
}
