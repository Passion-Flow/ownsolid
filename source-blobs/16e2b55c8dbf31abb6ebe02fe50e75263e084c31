package migrate

import (
	"reflect"
	"strings"
	"testing"
)

func TestCloudInventoryNativeScopeAndRetainedReceipts(t *testing.T) {
	c, err := accountCatalog()
	if err != nil {
		t.Fatal(err)
	}
	before := append([]step(nil), c.steps...)
	f := cloudInventoryFragment(c.project(len(c.steps)).tables)
	if err := appendFragment(&c, f, c.fragments); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, c.steps[:len(before)]) {
		t.Fatal("existing migration checksums changed")
	}
	p := c.project(len(c.steps))
	for _, name := range []string{"AWS_DISCOVERED_RESOURCES", "AWS_IMPORT_RECEIPTS"} {
		table, ok := p.tables[name]
		if !ok {
			t.Fatal("missing cloud responsibility", name)
		}
		scoped := false
		for _, key := range table.Constraints {
			if key.Kind == "R" && key.Reference == "WORKSPACES" {
				scoped = reflect.DeepEqual(key.Columns, []string{"TENANT_ID", "WORKSPACE_ID"}) && key.ReferenceKey == "WORKSPACES_TENANT_ID"
			}
		}
		if !scoped {
			t.Fatal("cross-tenant Workspace reference admitted", name)
		}
		grants := requiredGrants(c)[name]
		if len(grants) != 4 || !grants["SELECT"] || !grants["INSERT"] || !grants["UPDATE"] || !grants["DELETE"] {
			t.Fatal("cloud objects changed runtime privilege boundary")
		}
	}
	resources := p.tables["AWS_DISCOVERED_RESOURCES"]
	for _, column := range resources.Columns {
		if column.Name == "MISSING" && (column.Type != "BIT" || column.Default != "0" || column.Nullable) {
			t.Fatal("missing=false default lost native boolean semantics")
		}
	}
	guard := p.triggers["AWS_RECEIPT_IMMUTABLE"]
	definition := `CREATE TRIGGER "AWS_RECEIPT_IMMUTABLE" BEFORE UPDATE OR DELETE ON "AWS_IMPORT_RECEIPTS" FOR EACH ROW ` + guard.Body
	if !triggerDefinitionMatches(definition, "APP", guard) {
		t.Fatal("native immutable receipt definition refused")
	}
	for _, altered := range []string{
		strings.Replace(definition, "UPDATE OR DELETE", "UPDATE", 1),
		strings.Replace(definition, "FOR EACH ROW", "FOR EACH ROW WHEN (1=0)", 1),
		strings.Replace(definition, "1=1", "1=0", 1),
	} {
		if triggerDefinitionMatches(altered, "APP", guard) {
			t.Fatal("weakened receipt history accepted")
		}
	}
}

func TestCloudInventoryRefusesMissingWorkspaceAuthority(t *testing.T) {
	c, err := accountCatalog()
	if err != nil {
		t.Fatal(err)
	}
	current := c.project(len(c.steps)).tables
	delete(current, "WORKSPACES")
	f := cloudInventoryFragment(current)
	before := append([]step(nil), c.steps...)
	if err := appendFragment(&c, f, c.fragments); err == nil {
		t.Fatal("missing exact Workspace key accepted")
	}
	if !reflect.DeepEqual(before, c.steps) {
		t.Fatal("failed admission changed the migration prefix")
	}
}
