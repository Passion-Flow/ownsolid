package mysqlmigrate

import (
	"bytes"
	"testing"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

const preparationSchemaStepCount = 117

func TestMySQLPreparationSchemaImmutable94Prefix(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < preparationSchemaStepCount {
		t.Fatal("complete measured preparation schema required", e)
	}
	raw, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 95,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "c3bb0b03746c2136a575aaee0c7118bc9a1e3c75281dee4d8c86694d52e24b7e" {
		t.Fatal("immutable1-94 serialized evolution prefix changed")
	}
}
