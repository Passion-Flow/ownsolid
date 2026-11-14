// Package migrate owns a native DM schema slice. It never rewrites vendor SQL
// or promotes foundation readiness into whole-platform serving readiness.
package migrate

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const Scope = "dameng_account_workspace_foundation_v1"
const journalName = "PO_SCHEMA_MIGRATIONS"
const maximumCatalogSteps = 4096

var identifier = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,62}$`)

type column struct {
	Name, Type           string
	Width                int
	Characters, Nullable bool
	Default, Virtual     string
}
type constraint struct {
	Name, Kind                     string
	Columns                        []string
	Reference, ReferenceKey, Check string
}
type table struct {
	Name        string
	Columns     []column
	Constraints []constraint
}
type index struct {
	Name, Table string
	Unique      bool
	Columns     []string
	Descending  []bool
}
type step struct {
	Number                    int
	Object, Kind, SQL, SHA256 string
	Table                     *table
	Index                     *index
}
type catalog struct {
	journal     table
	steps       []step
	tables      []table
	evolution   map[int]schemaChange
	deleteRules map[string]string
	fragments   map[string]bool
}

type schemaError struct{ code string }

func (e *schemaError) Error() string { return e.code }
func fail(code string) error         { return &schemaError{code} }
func FailureCode(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := err.(*schemaError); ok {
		return e.code
	}
	return "DAMENG_SCHEMA_OPERATION_FAILED"
}
func digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func quoted(s string) string { return `"` + s + `"` }
func names(values []string) string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = quoted(v)
	}
	return strings.Join(out, ",")
}
func (c column) declaration() string {
	t := c.Type
	if c.Width > 0 {
		unit := ""
		if c.Characters {
			unit = " CHAR"
		}
		t += fmt.Sprintf("(%d%s)", c.Width, unit)
	}
	out := quoted(c.Name) + " " + t
	if c.Virtual != "" {
		return out + " GENERATED ALWAYS AS (" + c.Virtual + ") VIRTUAL"
	}
	if c.Default != "" {
		out += " DEFAULT " + c.Default
	}
	if !c.Nullable {
		out += " NOT NULL"
	}
	return out
}
func (t table) ddl() string {
	parts := make([]string, 0, len(t.Columns)+len(t.Constraints))
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
		case "R":
			body = "FOREIGN KEY (" + names(c.Columns) + ") REFERENCES " + quoted(c.Reference) + " (" + names(referenceColumns(c.Reference, c.ReferenceKey)) + ")"
		case "C":
			body = "CHECK (" + c.Check + ")"
		}
		parts = append(parts, "CONSTRAINT "+quoted(c.Name)+" "+body)
	}
	return "CREATE TABLE " + quoted(t.Name) + " (\n  " + strings.Join(parts, ",\n  ") + "\n)"
}
func (i index) ddl() string {
	parts := make([]string, len(i.Columns))
	for n, c := range i.Columns {
		parts[n] = quoted(c)
		if i.Descending[n] {
			parts[n] += " DESC"
		}
	}
	u := ""
	if i.Unique {
		u = "UNIQUE "
	}
	return "CREATE " + u + "INDEX " + quoted(i.Name) + " ON " + quoted(i.Table) + " (" + strings.Join(parts, ",") + ")"
}
func referenceColumns(t, key string) []string {
	for _, v := range foundationTables() {
		if v.Name == t {
			for _, c := range v.Constraints {
				if c.Name == key {
					return c.Columns
				}
			}
		}
	}
	return nil
}
func foundationCatalog() (catalog, error) {
	out := catalog{journal: journalTable(), tables: foundationTables()}
	objects := map[string]bool{journalName: true}
	add := func(s step) {
		s.Number = len(out.steps) + 1
		raw, _ := json.Marshal(s)
		s.SHA256 = digest(string(raw))
		out.steps = append(out.steps, s)
	}
	for n := range out.tables {
		t := out.tables[n]
		if !identifier.MatchString(t.Name) || objects[t.Name] || len(t.Columns) == 0 {
			return catalog{}, fail("DAMENG_SCHEMA_CATALOG_INVALID")
		}
		objects[t.Name] = true
		seen := map[string]bool{}
		for _, c := range t.Columns {
			if !identifier.MatchString(c.Name) || seen[c.Name] {
				return catalog{}, fail("DAMENG_SCHEMA_CATALOG_INVALID")
			}
			seen[c.Name] = true
		}
		for _, c := range t.Constraints {
			if !identifier.MatchString(c.Name) || c.Kind == "R" && len(referenceColumns(c.Reference, c.ReferenceKey)) != len(c.Columns) {
				return catalog{}, fail("DAMENG_SCHEMA_CATALOG_INVALID")
			}
		}
		add(step{Object: t.Name, Kind: "table", SQL: t.ddl(), Table: &t})
	}
	for _, i := range foundationIndexes() {
		v := i
		if !identifier.MatchString(v.Name) || objects[v.Name] || len(v.Columns) != len(v.Descending) {
			return catalog{}, fail("DAMENG_SCHEMA_CATALOG_INVALID")
		}
		objects[v.Name] = true
		add(step{Object: v.Name, Kind: "index", SQL: v.ddl(), Index: &v})
	}
	add(step{Object: "INSTALLATION_SEED", Kind: "seed", SQL: `INSERT INTO "INSTALLATION_STATE" ("ID") VALUES (1)`})
	if len(out.steps) > maximumCatalogSteps {
		return catalog{}, fail("DAMENG_SCHEMA_CATALOG_INVALID")
	}
	return out, nil
}

// Plan returns the exact native statements for review/export, not an editable
// runtime catalog. Callers cannot inject replacement SQL into migration Run.
type PlannedStep struct {
	Number int    `json:"number"`
	Object string `json:"object"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	SQL    string `json:"sql"`
}

// JournalSQL is exported for offline review. Bootstrap can only adopt an empty
// journal with the exact verified definition; it cannot journal its own CREATE.
func JournalSQL() string { return journalTable().ddl() }

func Plan() ([]PlannedStep, error) {
	c, e := defaultCatalog()
	if e != nil {
		return nil, e
	}
	out := []PlannedStep{}
	for _, s := range c.steps {
		out = append(out, PlannedStep{s.Number, s.Object, s.Kind, s.SHA256, s.SQL})
	}
	return out, nil
}
func lockID(schema string) int64 {
	h := sha256.Sum256([]byte("projectone.dameng.installation." + schema))
	return int64(binary.BigEndian.Uint32(h[:4]) & 0x3fffffff)
}

// expression normalizes metadata whitespace/case outside literals only. It is
// never applied to executed SQL and does not equate different predicates.
func expression(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '\'' {
			out.WriteByte(b)
			for i++; i < len(s); i++ {
				out.WriteByte(s[i])
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i++
						out.WriteByte(s[i])
						continue
					}
					break
				}
			}
			continue
		}
		if b == '"' {
			for i++; i < len(s) && s[i] != '"'; i++ {
				out.WriteByte(s[i])
			}
			continue
		}
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b >= 'a' && b <= 'z' {
			b -= 32
		}
		out.WriteByte(b)
	}
	return out.String()
}
