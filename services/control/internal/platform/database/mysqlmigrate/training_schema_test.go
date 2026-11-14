package mysqlmigrate

import (
	"bytes"
	"testing"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLTrainingSchemaImmutable59Prefix(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 85 {
		t.Fatal("complete initial training schema required", e)
	}
	raw, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 60,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "72271b287573ac91e0326cb352608a626e2fc116ef7e61389af7d55cffc8cabe" {
		t.Fatal("immutable1-59 serialized evolution prefix changed")
	}
}
