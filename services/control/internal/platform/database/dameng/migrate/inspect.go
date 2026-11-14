//go:build cgo && odbc_manager

package migrate

import (
	"context"
)

func deleteRuleMatches(want, actual string) bool {
	if want == "CASCADE" {
		return actual == "CASCADE"
	}
	return actual == "NO ACTION" || actual == "RESTRICT"
}
func shapeDrift(e error) bool {
	switch FailureCode(e) {
	case "DAMENG_SCHEMA_COLUMN_DRIFT", "DAMENG_SCHEMA_CONSTRAINT_DRIFT":
		return true
	}
	return false
}

func (n *nativeBackend) inspect(ctx context.Context) (snapshot, error) {
	s := snapshot{Present: map[string]bool{}}
	ctx, cancel := n.bounded(ctx)
	defer cancel()
	rows, e := n.connection.QueryContext(ctx, "SELECT OBJECT_NAME,OBJECT_TYPE FROM ALL_OBJECTS WHERE OWNER=? AND OBJECT_TYPE IN ('TABLE','VIEW','MATERIALIZED VIEW','TRIGGER','SEQUENCE','SYNONYM','PROCEDURE','FUNCTION','PACKAGE') ORDER BY OBJECT_NAME", n.configuration.Schema)
	if e != nil {
		return s, fail("DAMENG_SCHEMA_METADATA_UNAVAILABLE")
	}
	objects := map[string]string{}
	for rows.Next() {
		var name, kind string
		if rows.Scan(&name, &kind) != nil || objects[name] != "" || (kind != "TABLE" && kind != "TRIGGER") {
			rows.Close()
			return s, fail("DAMENG_SCHEMA_UNJOURNALED_OBJECT")
		}
		objects[name] = kind
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, fail("DAMENG_SCHEMA_METADATA_UNAVAILABLE")
	}
	s.Journal = objects[journalName] == "TABLE"
	if !s.Journal {
		if len(objects) > 0 {
			return s, fail("DAMENG_SCHEMA_UNJOURNALED_OBJECT")
		}
		return s, nil
	}
	rows, e = n.connection.QueryContext(ctx, `SELECT "STEP_NO","SCOPE","SOURCE_SHA256","OBJECT_NAME","STATE","STARTED_AT","APPLIED_AT" FROM "PO_SCHEMA_MIGRATIONS" ORDER BY "STEP_NO"`)
	if e != nil {
		return s, fail("DAMENG_SCHEMA_JOURNAL_INVALID")
	}
	s.Entries, e = readJournalRows(rows)
	rows.Close()
	if e != nil {
		return s, e
	}
	r, _, e := journalProgress(n.catalog, s)
	if e != nil {
		return s, e
	}
	p := n.catalog.project(r.AppliedSteps)
	implicit := map[string]bool{}
	verified := map[string]bool{}
	merge := func(keys map[string]bool) {
		for key := range keys {
			implicit[key] = true
		}
	}
	if r.DirtyStep != 0 {
		dirty := n.catalog.steps[r.DirtyStep-1]
		if x, ok := n.catalog.evolution[dirty.Number]; ok {
			switch x.Kind {
			case "alter":
				if objects[x.After.Name] != "TABLE" {
					return s, fail("DAMENG_SCHEMA_OBJECT_MISSING")
				}
				keys, afterError := n.verifyTable(ctx, *x.After)
				if afterError == nil {
					p.tables[x.After.Name] = *x.After
					s.Present[dirty.Object] = true
				} else {
					// Never query again after a statement/driver failure. Only a fully
					// consumed, structurally different result permits before readback.
					if !shapeDrift(afterError) {
						return s, afterError
					}
					keys, e = n.verifyTable(ctx, *x.Before)
					if e != nil {
						return s, e
					}
				}
				merge(keys)
				verified[x.After.Name] = true
			case "table":
				if objects[x.After.Name] == "TABLE" {
					p.tables[x.After.Name] = *x.After
				}
			case "trigger":
				if objects[x.Trigger.Name] == "TRIGGER" {
					p.triggers[x.Trigger.Name] = *x.Trigger
				}
			}
		} else if dirty.Table != nil && objects[dirty.Object] == "TABLE" {
			p.tables[dirty.Object] = *dirty.Table
		}
	}
	for name, t := range p.tables {
		if objects[name] != "TABLE" {
			return s, fail("DAMENG_SCHEMA_OBJECT_MISSING")
		}
		if !verified[name] {
			keys, e := n.verifyTable(ctx, t)
			if e != nil {
				return s, e
			}
			merge(keys)
		}
		if name != journalName {
			s.Present[name] = true
		}
	}
	internal, e := n.internalFKTriggers(ctx, p.tables, objects)
	if e != nil {
		return s, e
	}
	for name, kind := range objects {
		if kind == "TABLE" {
			if _, ok := p.tables[name]; !ok {
				return s, fail("DAMENG_SCHEMA_UNJOURNALED_OBJECT")
			}
		} else {
			if _, ok := p.triggers[name]; !ok && !internal[name] {
				return s, fail("DAMENG_SCHEMA_UNJOURNALED_OBJECT")
			}
		}
	}
	for name, t := range p.triggers {
		if objects[name] != "TRIGGER" {
			return s, fail("DAMENG_SCHEMA_OBJECT_MISSING")
		}
		if e = n.verifyTrigger(ctx, t); e != nil {
			return s, e
		}
		s.Present[name] = true
	}
	if e = n.verifyIndexes(ctx, s.Present, implicit); e != nil {
		return s, e
	}
	if objects["INSTALLATION_STATE"] == "TABLE" {
		var count int
		if n.connection.QueryRowContext(ctx, `SELECT COUNT(*) FROM "INSTALLATION_STATE" WHERE "ID"=1`).Scan(&count) != nil || count > 1 {
			return s, fail("DAMENG_SCHEMA_SEED_INVALID")
		}
		s.Present["INSTALLATION_SEED"] = count == 1
	}
	for _, step := range n.catalog.steps {
		x, ok := n.catalog.evolution[step.Number]
		if !ok {
			continue
		}
		if x.Kind == "alter" && step.Number <= r.AppliedSteps {
			s.Present[step.Object] = true
		}
		if x.Kind != "seed" && x.Kind != "backfill" {
			continue
		}
		available := true
		for _, name := range x.ProbeTables {
			if objects[name] != "TABLE" {
				available = false
			}
		}
		if !available {
			continue
		}
		// A completed no-op backfill is evidence only for its own journal entry.
		if x.Kind == "backfill" && step.Number > len(s.Entries) {
			continue
		}
		var count int
		if n.connection.QueryRowContext(ctx, x.Probe).Scan(&count) != nil || count < 0 {
			return s, fail("DAMENG_SCHEMA_SEED_INVALID")
		}
		if x.Kind == "seed" && count != x.BeforeCount && count != x.AfterCount {
			return s, fail("DAMENG_SCHEMA_SEED_INVALID")
		}
		s.Present[step.Object] = count == x.AfterCount
	}
	return s, nil
}

func (n *nativeBackend) verifyTrigger(ctx context.Context, t trigger) error {
	var name, table, event, timing, body, status string
	if e := n.connection.QueryRowContext(ctx, "SELECT TRIGGER_NAME,TABLE_NAME,TRIGGERING_EVENT,TRIGGER_TYPE,TRIGGER_BODY,STATUS FROM ALL_TRIGGERS WHERE OWNER=? AND TRIGGER_NAME=?", n.configuration.Schema, t.Name).Scan(&name, &table, &event, &timing, &body, &status); e != nil {
		return fail("DAMENG_SCHEMA_TRIGGER_METADATA_UNAVAILABLE")
	}
	if name != t.Name || table != t.Table || event != t.Event || timing != t.Timing || status != "ENABLED" || len(body) > n.configuration.MaximumMetadataBytes || expression(body) != expression(t.Body) {
		return fail("DAMENG_SCHEMA_TRIGGER_DRIFT")
	}
	var definition string
	if e := n.connection.QueryRowContext(ctx, "SELECT DBMS_METADATA.GET_DDL('TRIGGER',?,?) FROM DUAL", t.Name, n.configuration.Schema).Scan(&definition); e != nil {
		return fail("DAMENG_SCHEMA_TRIGGER_DEFINITION_UNAVAILABLE")
	}
	if len(definition) > n.configuration.MaximumMetadataBytes || !triggerDefinitionMatches(definition, n.configuration.Schema, t) {
		return fail("DAMENG_SCHEMA_TRIGGER_DRIFT")
	}
	return nil
}
