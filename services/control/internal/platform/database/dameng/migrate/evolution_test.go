package migrate

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEvolutionPreservesFullFoundationProjection(t *testing.T) {
	old, e := foundationCatalog()
	if e != nil {
		t.Fatal(e)
	}
	current, e := accountCatalog()
	if e != nil {
		t.Fatal(e)
	}
	if len(old.steps) != 31 || len(old.tables) != 14 || len(current.steps) != 68 || len(current.tables) != 21 {
		t.Fatal("catalog boundary changed")
	}
	for i, want := range old.steps {
		a, _ := json.Marshal(want)
		b, _ := json.Marshal(current.steps[i])
		if string(a) != string(b) {
			t.Fatalf("complete legacy step%d projection changed", i+1)
		}
	}
	if current.steps[31].Object != "JOURNAL_CAPACITY_V2_ADD" || current.steps[32].Object != "JOURNAL_CAPACITY_V1_DROP" {
		t.Fatal("capacity upgrade not first")
	}
	for _, prefix := range []int{31, 32, 33, 45, 49, 53, 68} {
		p := current.project(prefix)
		if prefix == 31 && len(p.tables["SESSIONS"].Columns) != len(old.tables[6].Columns) {
			t.Fatal("future ALTER leaked into old prefix")
		}
		checks := map[string]bool{}
		for _, v := range p.tables[journalName].Constraints {
			checks[v.Name] = true
		}
		if checks["PO_SCHEMA_MIGRATIONS_STEP"] != (prefix < 33) || checks["PO_SCHEMA_MIGRATIONS_STEP_V2"] != (prefix >= 32) {
			t.Fatal("journal capacity intermediate shape missing")
		}
	}
	if !reflect.DeepEqual(current.project(68).tables["SESSIONS"], current.tables[6]) {
		t.Fatal("runtime catalog did not receive latest ALTER shape")
	}
	if !strings.Contains(JournalSQL(), "BETWEEN 1 AND 256") {
		t.Fatal("legacy journal bootstrap changed")
	}
}
func TestEvolutionRejectsAlterDriftAndUnknownDependencies(t *testing.T) {
	for name, mutate := range map[string]func(*schemaFragment){
		"dependency":    func(f *schemaFragment) { f.Requires = []string{"missing_v2"} },
		"before":        func(f *schemaFragment) { f.Changes[0].Before.Columns[0].Name = "WRONG" },
		"no_op":         func(f *schemaFragment) { f.Changes[0].After = f.Changes[0].Before },
		"duplicate":     func(f *schemaFragment) { f.Changes[0].ID = "USERS" },
		"missing_after": func(f *schemaFragment) { f.Changes[0].After = nil },
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := foundationCatalog()
			f := accountLicenseFragment(c)
			mutate(&f)
			if appendFragment(&c, f, map[string]bool{"foundation_v1": true}) == nil {
				t.Fatal("invalid change accepted")
			}
		})
	}
}
func TestEvolutionHashesMetadataAndReadbackContract(t *testing.T) {
	a, _ := foundationCatalog()
	b, _ := foundationCatalog()
	fa, fb := accountLicenseFragment(a), accountLicenseFragment(b)
	// A changed probe with identical DDL must still invalidate later source proof.
	fb.Changes[3].Probe += " "
	if e := appendFragment(&a, fa, map[string]bool{"foundation_v1": true}); e != nil {
		t.Fatal(e)
	}
	if e := appendFragment(&b, fb, map[string]bool{"foundation_v1": true}); e != nil {
		t.Fatal(e)
	}
	if a.steps[34].SHA256 == b.steps[34].SHA256 {
		t.Fatal("probe omitted from extension hash")
	}
}
func TestCompleteTriggerDefinitionRejectsHeaderBypass(t *testing.T) {
	tg := trigger{"GUARD", "USERS", "UPDATE", "BEFORE EACH ROW", `BEGIN IF :NEW."VERSION"<>:OLD."VERSION"+1 THEN RAISE_APPLICATION_ERROR(-20001,'immutable Value'); END IF; END;`}
	good := `CREATE TRIGGER "GUARD" BEFORE UPDATE ON "USERS" FOR EACH ROW ` + tg.Body
	if !triggerDefinitionMatches(good, "APP", tg) || !triggerDefinitionMatches(`CREATE OR REPLACE TRIGGER "APP"."GUARD" BEFORE UPDATE ON "APP"."USERS" FOR EACH ROW `+tg.Body, "APP", tg) {
		t.Fatal("declared trigger refused")
	}
	for _, bad := range []string{
		strings.Replace(good, "FOR EACH ROW", "FOR EACH ROW WHEN (1=0)", 1),
		strings.Replace(good, "UPDATE ON", "UPDATE OF DISPLAY_NAME ON", 1),
		strings.Replace(good, "FOR EACH ROW", "REFERENCING NEW AS X OLD AS Y FOR EACH ROW", 1),
		strings.Replace(good, "immutable Value", "IMMUTABLE VALUE", 1),
		strings.Replace(good, "END IF", "ENDIF", 1),
		good + " ALTER TRIGGER GUARD DISABLE;",
		strings.Replace(good, "BEFORE", "/* erased? */ BEFORE", 1),
	} {
		if triggerDefinitionMatches(bad, "APP", tg) {
			t.Fatal("unaccounted trigger definition accepted")
		}
	}
}
func TestJournalProgressDoesNotEstablishObjectReadiness(t *testing.T) {
	c, _ := defaultCatalog()
	s := snapshot{Journal: true, Present: map[string]bool{}}
	for _, step := range c.steps {
		s.Entries = append(s.Entries, journalEntry{step.Number, Scope, step.SHA256, step.Object, "applied", true, true})
	}
	r, _, e := journalProgress(c, s)
	if e != nil || r.FoundationReady || r.RuntimeReady || r.PlatformReady {
		t.Fatal("journal is not object proof", e)
	}
	if _, e = assess(c, s); FailureCode(e) != "DAMENG_SCHEMA_OBJECT_MISSING" {
		t.Fatal("missing actual objects accepted", e)
	}
}
