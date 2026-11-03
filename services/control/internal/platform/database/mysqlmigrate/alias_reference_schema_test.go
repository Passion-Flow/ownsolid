package mysqlmigrate

import (
	"bytes"
	"testing"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLAliasReferenceSchemaPreservesImmutable137Prefix(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 147 {
		t.Fatal("complete measured alias reference schema required", e)
	}
	raw, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 138,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "fed7907794a5698afc83971ffac8e7f9fcdc8d0f7703e7cfe1078f37a85da633" {
		t.Fatal("immutable1-137 serialized evolution prefix changed")
	}
	for i, obj := range []string{"gateway_instances", "model_aliases", "model_alias_history", "model_alias_target_versions", "alias_history_immutable_update", "alias_history_immutable_delete", "alias_target_immutable_update", "alias_target_immutable_delete", "alias_identity_immutable", "alias_target_snapshot"} {
		item := c.steps[137+i]
		if item.Step != 138+i || item.Object != obj {
			t.Fatal("alias reference migration ownership/order changed")
		}
	}
}

func TestMySQLAliasVariantPreservesImmutable147Prefix(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 149 {
		t.Fatal("complete measured deployment variant required", e)
	}
	raw, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 148,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "665a101e3944e01580161788838574fafa145be642de61aa47c434eb3943550e" {
		t.Fatal("immutable1-147 serialized evolution prefix changed")
	}
	if c.steps[147].Object != "deployments" || c.steps[148].Object != "deployment_model_identity" {
		t.Fatal("deployment variant ownership changed")
	}
}
