package database

import (
	"reflect"
	"testing"
)

func TestM17ReleaseCatalogIsExactAccount22WithoutNewState(t *testing.T) {
	old, e := orderedProfile("localization")
	if e != nil {
		t.Fatal(e)
	}
	next, e := orderedProfile("releases")
	if e != nil || len(next) != 22 || !reflect.DeepEqual(old, next) {
		t.Fatal("M17 changed installed catalog", e)
	}
	original := []string{"releases"}
	mapped := schemaProfiles(original)
	if mapped[0] != "localization" || original[0] != "releases" {
		t.Fatal("schema mapping mutated caller/profile identity")
	}
	for _, invalid := range []string{"release", "Releases", "m17", "releases ", "kubernetes"} {
		if _, e = orderedProfile(invalid); e == nil {
			t.Fatal("unknown graph admitted", invalid)
		}
	}
}
