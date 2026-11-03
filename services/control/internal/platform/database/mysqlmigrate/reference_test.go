package mysqlmigrate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMySQLEvolutionMeasuredReferenceCanonicalization(t *testing.T) {
	_, document, _ := evolutionFixture(t)
	item := &document.Steps[0]
	item.Definition = strings.Replace(item.Definition, "`name` varchar(101) COLLATE", "`name` varchar(101) CHARACTER SET utf8mb4 COLLATE", 1)
	item.DefinitionSHA256 = hash(item.Definition)
	raw, _ := json.Marshal(document)
	canonical, e := CanonicalizeMeasuredEvolution(raw)
	if e != nil {
		t.Fatal(e)
	}
	var got evolutionDocument
	if json.Unmarshal(canonical, &got) != nil || got.Steps[0].Definition != canonicalDefinition(item.Definition) || got.Steps[0].SourceSHA256 != item.SourceSHA256 || got.Steps[0].PreviousDefinitionSHA256 != item.PreviousDefinitionSHA256 || got.Steps[0].DefinitionSHA256 != hash(got.Steps[0].Definition) {
		t.Fatal("metadata canonicalization changed source/predecessor/meaning")
	}
	if repeated, e := CanonicalizeMeasuredEvolution(canonical); e != nil || string(repeated) != string(canonical) {
		t.Fatal("canonical metadata not stable", e)
	}
	for _, change := range []func(*evolutionDocument){func(d *evolutionDocument) { d.Steps[0].DefinitionSHA256 = strings.Repeat("0", 64) }, func(d *evolutionDocument) { d.Steps[0].Kind = "execute_sql" }, func(d *evolutionDocument) { d.Steps[0].Object = "escape`" }, func(d *evolutionDocument) { d.Steps[0].PreviousDefinitionSHA256 = "" }} {
		var altered evolutionDocument
		json.Unmarshal(raw, &altered)
		change(&altered)
		invalid, _ := json.Marshal(altered)
		if _, e := CanonicalizeMeasuredEvolution(invalid); e == nil {
			t.Fatal("unverified metadata admitted")
		}
	}
}
