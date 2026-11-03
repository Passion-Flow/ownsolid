package mysqlmigrate

import "testing"

func TestMySQLPublicationExactTriggerPositions(t *testing.T) {
	for _, d := range []triggerDefinition{
		{Name: "model_preparation_review_update", Table: "model_preparations", Event: "UPDATE", Timing: "BEFORE", Order: 2},
		{Name: "model_publication_apply", Table: "model_publications", Event: "INSERT", Timing: "AFTER", Order: 1},
	} {
		if !ownedTriggerPosition(d) {
			t.Fatal("exact measured publication position refused")
		}
		for _, change := range []func(*triggerDefinition){
			func(v *triggerDefinition) { v.Name = "unowned_trigger" },
			func(v *triggerDefinition) { v.Table = "users" },
			func(v *triggerDefinition) { v.Event = "DELETE" },
			func(v *triggerDefinition) { v.Order++ },
		} {
			other := d
			change(&other)
			if ownedTriggerPosition(other) {
				t.Fatal("nonmatching publication exception admitted", other)
			}
		}
	}
}
