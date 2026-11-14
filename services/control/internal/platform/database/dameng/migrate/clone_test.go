package migrate

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTableClonePreservesMetadataBytesAndNestedOwnership(t *testing.T) {
	for _, original := range append(foundationTables(), journalTable()) {
		before, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}
		copy := cloneTable(original)
		after, err := json.Marshal(copy)
		if err != nil || string(before) != string(after) {
			t.Fatal("clone changed canonical metadata bytes", original.Name)
		}
		copy.Name = "CHANGED"
		copy.Columns[0].Name = "CHANGED"
		for i := range copy.Constraints {
			copy.Constraints[i].Name = "CHANGED"
			for j := range copy.Constraints[i].Columns {
				copy.Constraints[i].Columns[j] = "CHANGED"
			}
		}
		retained, err := json.Marshal(original)
		if err != nil || string(retained) != string(before) {
			t.Fatal("caller mutated retained table or key ownership", original.Name)
		}
	}
}

func TestTableClonePreservesNilAndEmptyMetadata(t *testing.T) {
	for _, original := range []table{
		{},
		{Columns: []column{}, Constraints: []constraint{}},
		{Constraints: []constraint{{Columns: nil}, {Columns: []string{}}}},
	} {
		if !reflect.DeepEqual(original, cloneTable(original)) {
			t.Fatal("clone changed nil/empty metadata shape")
		}
	}
}
