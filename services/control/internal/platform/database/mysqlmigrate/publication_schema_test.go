package mysqlmigrate

import (
	"bytes"
	"testing"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

const publicationSchemaStepCount = 137

func TestMySQLPublicationSchemaImmutable117Prefix(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < publicationSchemaStepCount {
		t.Fatal("complete measured product publication schema required", e)
	}
	raw, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 118,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "45269fcc470e9d3c297b19c8463b152bd22ace27a018aadde137b89d4666de09" {
		t.Fatal("immutable1-117 serialized evolution prefix changed")
	}
}
