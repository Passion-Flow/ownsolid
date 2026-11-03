package database

import (
	"bytes"
	"testing"
)

func TestModelsCatalogPreservesMaintenancePrefix(t *testing.T) {
	old, e := orderedProfile("maintenance")
	if e != nil {
		t.Fatal(e)
	}
	current, e := orderedProfile("models")
	if e != nil || len(old) != 10 || len(current) != 11 {
		t.Fatal(e)
	}
	for i := range old {
		if old[i].Checksum != current[i].Checksum || !bytes.Equal(old[i].SQL, current[i].SQL) {
			t.Fatal("installed prefix changed", i)
		}
	}
}
