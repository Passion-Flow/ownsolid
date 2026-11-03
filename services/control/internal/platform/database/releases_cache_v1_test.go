package database

import (
	"reflect"
	"testing"
)

func TestReleasesCacheV1UsesExactAccount22Catalog(t *testing.T) {
	old, e := orderedProfile("releases")
	if e != nil {
		t.Fatal(e)
	}
	next, e := orderedProfile("releases-cache-v1")
	if e != nil || len(next) != 22 || !reflect.DeepEqual(old, next) {
		t.Fatal("cache profile changed the installed schema", e)
	}
	original := []string{"releases-cache-v1"}
	if got := schemaProfiles(original); got[0] != "localization" || original[0] != "releases-cache-v1" {
		t.Fatal("schema alias changed caller identity", got, original)
	}
	if _, e = orderedProfile("releases-cache-v2"); e == nil {
		t.Fatal("unversioned future schema admitted")
	}
}
