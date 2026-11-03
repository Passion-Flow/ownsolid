package databaseaccess

import (
	"reflect"
	"regexp"
	"sort"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

func TestPostgreSQLVariantRuntimeGrantsMatchActualMigrations(t *testing.T) {
	var actual []string
	for _, path := range []string{"postgresql/000021_mongodb_dataset_imports.up.sql", "postgresql/000022_aws_inventory.up.sql", "postgresql/000023_artifact_objects.up.sql", "postgresql/000024_image_registry.up.sql", "postgresql/000025_task_events.up.sql", "postgresql/000026_middleware.up.sql", "postgresql/000027_private_cluster.up.sql"} {
		raw, err := migrations.Files.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range regexp.MustCompile(`(?m)^CREATE TABLE ([a-z_]+) \(`).FindAllSubmatch(raw, -1) {
			actual = append(actual, string(match[1]))
		}
	}
	sort.Strings(actual)
	wanted := append([]string(nil), postgreSQLVariantTables[:]...)
	sort.Strings(wanted)
	if len(actual) != 22 || !reflect.DeepEqual(actual, wanted) {
		t.Fatalf("runtime grants differ from implemented variant tables: %v / %v", actual, wanted)
	}
}

func TestPostgreSQLVariantCatalogRemainsIndependentOfSharedTables(t *testing.T) {
	if len(applicationTables) != 85 {
		t.Fatal("vendor variants expanded the shared initial catalog")
	}
	for _, required := range postgreSQLVariantTables {
		found := false
		for _, name := range postgreSQLTables() {
			if name == required {
				found = true
			}
		}
		if !found {
			t.Fatal("implemented PostgreSQL variant table has no runtime grant", required)
		}
		for _, name := range applicationTables {
			if name == required {
				t.Fatal("PostgreSQL variant entered the shared initial catalog", required)
			}
		}
	}
}
