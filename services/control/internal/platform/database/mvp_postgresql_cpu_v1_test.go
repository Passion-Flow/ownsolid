package database

import (
	"bytes"
	"testing"
)

func TestMVPPostgreSQLCPUV1KeepsExactOriginalPrefixAndIndependentCatalog(t *testing.T) {
	old, err := orderedProfile("releases")
	if err != nil {
		t.Fatal(err)
	}
	full, err := orderedProfile(MVPPostgreSQLCPUV1Profile)
	if err != nil {
		t.Fatal(err)
	}
	platform, err := orderedProfile("platform")
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 22 || len(full) != MVPPostgreSQLCPUV1SchemaVersion || len(platform) != 27 {
		t.Fatal("catalog sizes")
	}
	for i, m := range old {
		if m.Version != full[i].Version || m.Checksum != full[i].Checksum || !bytes.Equal(m.SQL, full[i].SQL) {
			t.Fatal("rewritten prefix", i)
		}
	}
	if full[1].Checksum == platform[1].Checksum || full[22].Checksum == platform[22].Checksum {
		t.Fatal("independent lineage aliased")
	}
	if schemaProfile(MVPPostgreSQLCPUV1Profile) != MVPPostgreSQLCPUV1Profile {
		t.Fatal("profile alias")
	}
}
