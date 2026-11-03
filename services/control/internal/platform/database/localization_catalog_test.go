package database

import (
	"bytes"
	"testing"
)

func TestM16CatalogPreservesExactM15Prefix(t *testing.T) {
	old, e := orderedProfile("portal")
	if e != nil || len(old) != 21 {
		t.Fatal(e)
	}
	next, e := orderedProfile("localization")
	if e != nil || len(next) != 22 {
		t.Fatal(e)
	}
	for i := range old {
		if old[i].Checksum != next[i].Checksum || !bytes.Equal(old[i].SQL, next[i].SQL) {
			t.Fatal("applied prefix changed", i)
		}
	}
	if bytes.Contains(next[21].SQL, []byte("CREATE TABLE")) || bytes.Contains(next[21].SQL, []byte("UPDATE ")) {
		t.Fatal("locale migration changes business data")
	}
}
