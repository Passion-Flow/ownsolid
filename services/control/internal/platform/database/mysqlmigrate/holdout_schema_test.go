package mysqlmigrate

import (
	"bytes"
	"testing"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLHoldoutSchemaImmutable85Prefix(t *testing.T) {
	c, e := DefaultCatalog()
	if e != nil || c.Steps() < 94 {
		t.Fatal("complete measured holdout schema required", e)
	}
	raw, e := assets.Files.ReadFile("evolution.json")
	if e != nil {
		t.Fatal(e)
	}
	cut := bytes.LastIndex(raw, []byte(",\n    {\n      \"step\": 86,"))
	if cut < 0 || hash(string(append(append([]byte{}, raw[:cut]...), []byte("\n  ]\n}\n")...))) != "5085e41304119bdbab59ae9f50386001b64a1258036bc6b67baa202e3cbc9204" {
		t.Fatal("immutable1-85 serialized evolution prefix changed")
	}
}
