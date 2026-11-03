package mysqlmigrate

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	assets "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql"
)

func TestMySQLMembershipSeedHasExactOwnedSourceAndOrder(t *testing.T) {
	base, _ := assets.Files.ReadFile("reference.json")
	evolved, _ := assets.Files.ReadFile("evolution.json")
	var original referenceDocument
	var extension evolutionDocument
	if json.Unmarshal(base, &original) != nil || json.Unmarshal(evolved, &extension) != nil {
		t.Fatal("actual reference decode")
	}
	original.Steps = append(original.Steps, extension.Steps...)
	if len(original.Steps) < 35 {
		t.Fatal("current membership schema is incomplete")
	}
	for _, name := range []string{"valid", "arbitrary_sql", "wrong_object", "wrong_invariant", "unexpected_predecessor", "missing_nullable_predecessor"} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(original)
			var d referenceDocument
			json.Unmarshal(raw, &d)
			files := fstest.MapFS{}
			for _, item := range append([]reference{d.Journal}, d.Steps...) {
				b, e := assets.Files.ReadFile(item.File)
				if e != nil {
					t.Fatal(e)
				}
				files[item.File] = &fstest.MapFile{Data: b, Mode: 0444}
			}
			seed := &d.Steps[33]
			switch name {
			case "arbitrary_sql":
				files[seed.File].Data = []byte("UPDATE users SET is_platform_admin=true;\n")
				seed.SourceSHA256 = hash(string(files[seed.File].Data))
			case "wrong_object":
				seed.Object = "unowned_seed"
			case "wrong_invariant":
				seed.Definition = "EXISTS arbitrary success"
				seed.DefinitionSHA256 = hash(seed.Definition)
			case "unexpected_predecessor":
				seed.PreviousDefinitionSHA256 = d.Steps[32].DefinitionSHA256
			case "missing_nullable_predecessor":
				d.Steps[32].Definition = d.Steps[4].Definition
				d.Steps[32].DefinitionSHA256 = hash(d.Steps[32].Definition)
			}
			raw, _ = json.Marshal(d)
			_, e := loadCatalog(raw, files)
			if name == "valid" {
				if e != nil {
					t.Fatal(e)
				}
			} else if e == nil {
				t.Fatal("unsafe membership migration source admitted")
			}
		})
	}
}
func TestMySQLMembershipSeedCanonicalizationIsNotGenericCode(t *testing.T) {
	value := evolutionDocument{Format: evolutionFormat, BaseReferenceSHA256: hash("synthetic base"), Steps: []reference{{Step: 34, File: "000034_membership_authority_seed.sql", SourceSHA256: hash(membershipAuthoritySeedSQL + "\n"), Object: membershipAuthoritySeedObject, Kind: installationSeed, Definition: membershipAuthoritySeedDefinition, DefinitionSHA256: hash(membershipAuthoritySeedDefinition)}}}
	raw, _ := json.Marshal(value)
	out, e := CanonicalizeMeasuredEvolution(raw)
	if e != nil || len(out) == 0 {
		t.Fatal("exact owned seed rejected", e)
	}
	for _, change := range []string{"object", "definition", "predecessor"} {
		altered := value
		altered.Steps = append([]reference{}, value.Steps...)
		switch change {
		case "object":
			altered.Steps[0].Object = "installation_seed"
		case "definition":
			altered.Steps[0].Definition = "arbitrary code"
			altered.Steps[0].DefinitionSHA256 = hash("arbitrary code")
		case "predecessor":
			altered.Steps[0].PreviousDefinitionSHA256 = hash("wrong predecessor")
		}
		raw, _ = json.Marshal(altered)
		if _, e = CanonicalizeMeasuredEvolution(raw); e == nil {
			t.Fatal("unowned seed metadata accepted")
		}
	}
}
