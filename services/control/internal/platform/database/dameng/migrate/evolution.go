package migrate

import (
	"encoding/json"
	"reflect"
	"regexp"
	"slices"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const CatalogVersion = 2

type trigger struct{ Name, Table, Event, Timing, Body string }
type schemaChange struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	SQL            string            `json:"sql"`
	Before         *table            `json:"before,omitempty"`
	After          *table            `json:"after,omitempty"`
	Index          *index            `json:"index,omitempty"`
	Trigger        *trigger          `json:"trigger,omitempty"`
	Probe          string            `json:"probe,omitempty"`
	ProbeTables    []string          `json:"probe_tables,omitempty"`
	BeforeCount    int               `json:"before_count,omitempty"`
	AfterCount     int               `json:"after_count,omitempty"`
	UUIDParameters int               `json:"uuid_parameters,omitempty"`
	DeleteRules    map[string]string `json:"delete_rules,omitempty"`
}
type schemaFragment struct {
	FormatVersion int            `json:"format_version"`
	ID            string         `json:"id"`
	Requires      []string       `json:"requires"`
	Changes       []schemaChange `json:"changes"`
}
type projection struct {
	tables   map[string]table
	triggers map[string]trigger
}

var fragmentID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// ValidateFragment validates a source submission against the exact current
// catalog. It does not register it, read a path, execute SQL or mutate runtime
// state. Accepted fragments must be compiled into defaultCatalog by its owner.
func ValidateFragment(raw []byte) error {
	var f schemaFragment
	if len(raw) == 0 || len(raw) > 1<<20 || wire.StrictJSON(raw, &f) != nil {
		return fail("DAMENG_SCHEMA_FRAGMENT_INVALID")
	}
	c, e := defaultCatalog()
	if e != nil {
		return e
	}
	return appendFragment(&c, f, c.fragments)
}
func accountCatalog() (catalog, error) {
	c, e := foundationCatalog()
	if e != nil {
		return c, e
	}
	c.evolution = map[int]schemaChange{}
	c.deleteRules = map[string]string{}
	c.fragments = map[string]bool{"foundation_v1": true}
	f := accountLicenseFragment(c)
	if e = appendFragment(&c, f, c.fragments); e != nil {
		return catalog{}, e
	}
	return c, nil
}
func defaultCatalog() (catalog, error) {
	c, e := accountCatalog()
	if e != nil {
		return c, e
	}
	// This order is append-only. Deferred ingestion foreign keys are registered
	// only after both destination owners and their exact scoped keys exist.
	for _, fragment := range []func(map[string]table) schemaFragment{
		accountMailFragment,
		artifactStoresFragment,
		nodesComponentsFragment,
		datasetsFragment,
		modelsFragment,
		applicationsFragment,
		artifactStoresTargetsFragment,
		knowledgeFragment,
		middlewareFragment,
		mongoDatasetsFragment,
		taskDeliveriesFragment,
		trainingFragment,
		modelPreparationsFragment,
		registryFragment,
		privateclustersFragment,
		cloudInventoryFragment,
		inferenceCutoversFragment,
	} {
		if e = appendFragment(&c, fragment(c.project(len(c.steps)).tables), c.fragments); e != nil {
			return catalog{}, e
		}
	}
	return c, nil
}
func cloneTable(t table) table {
	// Every field outside these slices is a value/string. Keep nil versus empty
	// slices intact because the exact metadata representation enters step hashes.
	// Repeated projections must not serialize the entire table through JSON.
	v := t
	v.Columns = slices.Clone(t.Columns)
	v.Constraints = slices.Clone(t.Constraints)
	for i := range v.Constraints {
		v.Constraints[i].Columns = slices.Clone(t.Constraints[i].Columns)
	}
	return v
}
func (c catalog) project(prefix int) projection {
	p := projection{map[string]table{journalName: cloneTable(c.journal)}, map[string]trigger{}}
	for _, s := range c.steps[:prefix] {
		if change, ok := c.evolution[s.Number]; ok {
			if change.After != nil {
				p.tables[change.After.Name] = cloneTable(*change.After)
			}
			if change.Trigger != nil {
				p.triggers[change.Trigger.Name] = *change.Trigger
			}
			continue
		}
		if s.Table != nil {
			p.tables[s.Table.Name] = cloneTable(*s.Table)
		}
	}
	return p
}
func appendFragment(c *catalog, f schemaFragment, known map[string]bool) error {
	if f.FormatVersion != 1 || !fragmentID.MatchString(f.ID) || known[f.ID] || len(f.Changes) == 0 || len(c.steps)+len(f.Changes) > maximumCatalogSteps {
		return fail("DAMENG_SCHEMA_FRAGMENT_INVALID")
	}
	for _, id := range f.Requires {
		if !known[id] {
			return fail("DAMENG_SCHEMA_FRAGMENT_DEPENDENCY_REQUIRED")
		}
	}
	if c.evolution == nil {
		c.evolution = map[int]schemaChange{}
	}
	if c.deleteRules == nil {
		c.deleteRules = map[string]string{}
	}
	objects := map[string]bool{}
	for _, s := range c.steps {
		objects[s.Object] = true
	}
	for _, change := range f.Changes {
		if !identifier.MatchString(change.ID) || objects[change.ID] || strings.TrimSpace(change.SQL) == "" || len(change.SQL) > 65536 || strings.ContainsRune(change.SQL, 0) {
			return fail("DAMENG_SCHEMA_FRAGMENT_INVALID")
		}
		p := c.project(len(c.steps))
		base, exists := table{}, false
		if change.After != nil {
			base, exists = p.tables[change.After.Name]
		}
		switch change.Kind {
		case "table":
			if change.After == nil || change.Before != nil || exists || change.ID != change.After.Name {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
		case "alter":
			if change.After == nil || change.Before == nil || !exists || change.Before.Name != change.After.Name || !reflect.DeepEqual(base, *change.Before) || reflect.DeepEqual(*change.Before, *change.After) {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
		case "index":
			if change.Index == nil || change.ID != change.Index.Name || len(change.Index.Columns) == 0 || len(change.Index.Columns) != len(change.Index.Descending) {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
			t, ok := p.tables[change.Index.Table]
			if !ok {
				return fail("DAMENG_SCHEMA_FRAGMENT_DEPENDENCY_REQUIRED")
			}
			for _, col := range change.Index.Columns {
				if !hasColumn(t, col) {
					return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
				}
			}
		case "trigger":
			if change.Trigger == nil || change.ID != change.Trigger.Name || change.Trigger.Body == "" || (change.Trigger.Timing != "BEFORE EACH ROW" && change.Trigger.Timing != "AFTER EACH ROW") {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
			if _, ok := p.tables[change.Trigger.Table]; !ok {
				return fail("DAMENG_SCHEMA_FRAGMENT_DEPENDENCY_REQUIRED")
			}
		case "seed", "backfill":
			if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(change.Probe)), "SELECT ") || len(change.ProbeTables) == 0 || change.UUIDParameters < 0 || change.UUIDParameters > 1 || change.Kind == "seed" && (change.BeforeCount != 0 || change.AfterCount != 1) || change.Kind == "backfill" && (change.BeforeCount != 0 || change.AfterCount != 0 || change.UUIDParameters != 0) {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
			for _, name := range change.ProbeTables {
				if _, ok := p.tables[name]; !ok {
					return fail("DAMENG_SCHEMA_FRAGMENT_DEPENDENCY_REQUIRED")
				}
			}
		default:
			return fail("DAMENG_SCHEMA_FRAGMENT_KIND_INVALID")
		}
		if change.Kind != "table" && change.Kind != "alter" && (change.Before != nil || change.After != nil) || change.Kind != "index" && change.Index != nil || change.Kind != "trigger" && change.Trigger != nil || change.Kind != "seed" && change.Kind != "backfill" && (change.Probe != "" || change.UUIDParameters != 0) {
			return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
		}
		if change.After != nil {
			if e := validateShape(*change.After, p.tables, change.DeleteRules); e != nil {
				return e
			}
		}
		for name, rule := range change.DeleteRules {
			if old, ok := c.deleteRules[name]; ok && old != rule {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
			c.deleteRules[name] = rule
		}
		number := len(c.steps) + 1
		// A separate versioned projection leaves the complete legacy step struct
		// and its original JSON serialization unchanged for steps1..31.
		bound := struct {
			Version, Number int
			Scope, Fragment string
			Change          schemaChange
		}{CatalogVersion, number, Scope, f.ID, change}
		raw, e := json.Marshal(bound)
		if e != nil {
			return fail("DAMENG_SCHEMA_FRAGMENT_INVALID")
		}
		c.steps = append(c.steps, step{Number: number, Object: change.ID, Kind: change.Kind, SQL: change.SQL, SHA256: digest(string(raw)), Index: change.Index})
		c.evolution[number] = change
		objects[change.ID] = true
	}
	p := c.project(len(c.steps))
	c.tables = nil
	// Preserve deterministic existing/new table order; journal is not runtime DML.
	seen := map[string]bool{}
	for _, s := range c.steps {
		var name string
		if s.Table != nil {
			name = s.Table.Name
		}
		if x, ok := c.evolution[s.Number]; ok && x.Kind == "table" {
			name = x.After.Name
		}
		if name != "" && !seen[name] {
			c.tables = append(c.tables, p.tables[name])
			seen[name] = true
		}
	}
	known[f.ID] = true
	return nil
}
func hasColumn(t table, name string) bool {
	for _, c := range t.Columns {
		if c.Name == name {
			return true
		}
	}
	return false
}
func validateShape(t table, previous map[string]table, rules map[string]string) error {
	if !identifier.MatchString(t.Name) || len(t.Columns) == 0 {
		return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
	}
	cols := map[string]bool{}
	cons := map[string]bool{}
	for _, c := range t.Columns {
		if !identifier.MatchString(c.Name) || cols[c.Name] || c.Width < 0 {
			return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
		}
		cols[c.Name] = true
		switch c.Type {
		case "INTEGER", "SMALLINT", "BIGINT", "BIT", "CHAR", "VARCHAR", "VARBINARY", "TIMESTAMP", "CLOB", "BLOB":
		default:
			return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
		}
		if c.Characters && c.Type != "VARCHAR" && c.Type != "CHAR" || c.Virtual != "" && c.Default != "" {
			return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
		}
	}
	for _, c := range t.Constraints {
		if !identifier.MatchString(c.Name) || cons[c.Name] {
			return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
		}
		cons[c.Name] = true
		for _, col := range c.Columns {
			if !cols[col] {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
		}
		switch c.Kind {
		case "C":
			if c.Check == "" {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
		case "P", "U":
			if len(c.Columns) == 0 {
				return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
			}
		case "R":
			ref, ok := previous[c.Reference]
			if c.Reference == t.Name {
				ref = t
				ok = true
			}
			found := false
			for _, key := range ref.Constraints {
				if key.Name == c.ReferenceKey && (key.Kind == "P" || key.Kind == "U") && len(key.Columns) == len(c.Columns) {
					found = true
				}
			}
			if !ok || !found {
				return fail("DAMENG_SCHEMA_FRAGMENT_DEPENDENCY_REQUIRED")
			}
		default:
			return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
		}
	}
	for name, rule := range rules {
		if !cons[name] || (rule != "CASCADE" && rule != "NO ACTION" && rule != "RESTRICT") {
			return fail("DAMENG_SCHEMA_FRAGMENT_SHAPE_INVALID")
		}
	}
	return nil
}

// Extension DDL is generated solely from explicitly authored native definitions.
// The legacy ddl()/referenceColumns() functions above remain byte-for-byte stable.
func extensionTableSQL(t table, known map[string]table, rules map[string]string) string {
	parts := []string{}
	for _, c := range t.Columns {
		parts = append(parts, c.declaration())
	}
	for _, c := range t.Constraints {
		body := ""
		switch c.Kind {
		case "P":
			body = "PRIMARY KEY (" + names(c.Columns) + ")"
		case "U":
			body = "UNIQUE (" + names(c.Columns) + ")"
		case "C":
			body = "CHECK (" + c.Check + ")"
		case "R":
			ref := known[c.Reference]
			if ref.Name == t.Name || c.Reference == t.Name {
				ref = t
			}
			var keys []string
			for _, key := range ref.Constraints {
				if key.Name == c.ReferenceKey {
					keys = key.Columns
				}
			}
			body = "FOREIGN KEY (" + names(c.Columns) + ") REFERENCES " + quoted(c.Reference) + " (" + names(keys) + ")"
			if rule := rules[c.Name]; rule != "" {
				body += " ON DELETE " + rule
			}
		}
		parts = append(parts, "CONSTRAINT "+quoted(c.Name)+" "+body)
	}
	return "CREATE TABLE " + quoted(t.Name) + " (\n  " + strings.Join(parts, ",\n  ") + "\n)"
}
