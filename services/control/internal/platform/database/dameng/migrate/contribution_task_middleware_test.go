package migrate

import (
	"reflect"
	"strings"
	"testing"
)

func TestTaskMiddlewareNativeFragments(t *testing.T) {
	c, e := foundationCatalog()
	if e != nil { t.Fatal(e) }
	known := map[string]bool{"foundation_v1": true}
	if e = appendFragment(&c, nodesComponentsFragment(c.project(len(c.steps)).tables), known); e != nil { t.Fatal(e) }
	known["nodes_components_v2"] = true
	for _, f := range []schemaFragment{taskDeliveriesFragment(c.project(len(c.steps)).tables), middlewareFragment(c.project(len(c.steps)).tables)} {
		if e = appendFragment(&c, f, known); e != nil { t.Fatal(f.ID, e) }
		known[f.ID] = true
	}
	p := c.project(len(c.steps))
	for _, name := range []string{"TASK_EVENT_OUTBOX", "TASK_EVENT_RECEIPTS", "TASK_EVENT_DEAD_LETTERS", "MIDDLEWARE_INSTANCES", "MIDDLEWARE_OPERATIONS"} { if _, ok := p.tables[name]; !ok { t.Fatal("missing actual table", name) } }
	conditional := 0
	for _, column := range p.tables["TASK_EVENT_DEAD_LETTERS"].Columns {
		if strings.HasPrefix(column.Name, "COORDINATE_") {
			conditional++
			if !column.Nullable || !strings.Contains(column.Virtual, `WHEN "BROKER_SEQUENCE" IS NOT NULL`) || !strings.Contains(column.Virtual, "ELSE NULL END") { t.Fatal("partial NULL coordinate key admits wrong uniqueness semantics") }
		}
	}
	if conditional != 3 { t.Fatal("all coordinate columns must become NULL together") }
	if len(TaskDeliveryNativeGuards()) != 9 { t.Fatal("incomplete native task guard set") }
}

func TestTaskDeliveryNativeTriggerDefinitionCannotBeWeakened(t *testing.T) {
	for _, guard := range TaskDeliveryNativeGuards() {
		definition := "CREATE TRIGGER "+quoted(guard.Name)+" BEFORE "+guard.Event+" ON "+quoted(guard.Table)+" FOR EACH ROW "+guard.Body
		if !TaskDeliveryNativeGuardMatches(definition, "SYNTHETIC", guard) { t.Fatal("own exact definition rejected", guard.Name) }
		if TaskDeliveryNativeGuardMatches(strings.Replace(definition, "FOR EACH ROW", "FOR EACH ROW WHEN (1=0)", 1), "SYNTHETIC", guard) || TaskDeliveryNativeGuardMatches(definition+" SELECT 1 FROM DUAL", "SYNTHETIC", guard) { t.Fatal("disabled or appended definition accepted", guard.Name) }
	}
	a, b := TaskDeliveryNativeGuards(), TaskDeliveryNativeGuards()
	a[0].Body = "BEGIN NULL; END;"
	if reflect.DeepEqual(a, b) || TaskDeliveryNativeGuardMatches("BEGIN NULL; END;", "SYNTHETIC", a[0]) { t.Fatal("caller changed schema authority") }
}
