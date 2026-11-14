package migrate

import (
	"reflect"
	"testing"
)

func TestBusinessFragmentsPreserveEveryRetainedPrefix(t *testing.T) {
	base, err := accountCatalog()
	if err != nil {
		t.Fatal(err)
	}
	composed, err := defaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base.steps, composed.steps[:68]) {
		t.Fatal("complete account prefix changed")
	}
	for _, entry := range []struct {
		id            string
		build         func(map[string]table) schemaFragment
		steps, tables int
	}{
		{"account_mail_v2", accountMailFragment, 77, 21},
		{"artifactstores_v2", artifactStoresFragment, 89, 25},
		{"nodes_components_v2", nodesComponentsFragment, 103, 30},
		{"datasets_v2", datasetsFragment, 124, 37},
		{"models_v2", modelsFragment, 159, 49},
		{"applications_v2", applicationsFragment, 194, 60},
		{"artifactstores_targets_v2", artifactStoresTargetsFragment, 199, 60},
		{"knowledge_v2", knowledgeFragment, 228, 67},
		{"middleware_v2", middlewareFragment, 235, 69},
		{"mongodatasets_v2", mongoDatasetsFragment, 240, 70},
		{"taskdeliveries_v2", taskDeliveriesFragment, 255, 73},
		{"training_v2", trainingFragment, 327, 81},
		{"model_preparations_v2", modelPreparationsFragment, 431, 94},
		{"registry_v2", registryFragment, 444, 101},
		{"privateclusters_v2", privateclustersFragment, 454, 104},
		{"cloudinventory_v2", cloudInventoryFragment, 459, 106},
		{"inference_cutovers_v2", inferenceCutoversFragment, 476, 108},
	} {
		before := append([]step(nil), base.steps...)
		fragment := entry.build(base.project(len(base.steps)).tables)
		if fragment.ID != entry.id {
			t.Fatal("fragment identity changed", entry.id)
		}
		if err := appendFragment(&base, fragment, base.fragments); err != nil {
			t.Fatal(entry.id, err)
		}
		if len(base.steps) != entry.steps || len(base.tables) != entry.tables ||
			!reflect.DeepEqual(before, base.steps[:len(before)]) ||
			!reflect.DeepEqual(base.steps, composed.steps[:entry.steps]) {
			t.Fatal("fragment missing or prior checkpoint rewritten", entry.id, len(base.steps), len(base.tables))
		}
	}
	if !reflect.DeepEqual(base, composed) {
		t.Fatal("registered catalog differs from reviewed fragment sequence")
	}
}

func TestDestinationForeignKeysRequireTheirActualOwners(t *testing.T) {
	base, err := accountCatalog()
	if err != nil {
		t.Fatal(err)
	}
	// Build this fragment against the complete projection, then deliberately
	// attempt to register it before its dependencies in a different catalog.
	complete, err := defaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	fragment := artifactStoresTargetsFragment(complete.project(194).tables)
	if err := appendFragment(&base, fragment, base.fragments); FailureCode(err) != "DAMENG_SCHEMA_FRAGMENT_DEPENDENCY_REQUIRED" {
		t.Fatal("destination constraints admitted before their owners", err)
	}
	if len(base.steps) != 68 || len(base.tables) != 21 {
		t.Fatal("dependency refusal mutated the catalog")
	}
}
