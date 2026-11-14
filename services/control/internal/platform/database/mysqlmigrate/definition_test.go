package mysqlmigrate

import (
	"strings"
	"testing"
)

func TestMySQLDefinitionAcceptsOnlyRedundantUTF8Declaration(t *testing.T) {
	catalog, e := DefaultCatalog()
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range append([]entry{catalog.journal}, catalog.steps...) {
		if item.Kind != createTable {
			continue
		}
		if canonicalDefinition(item.Definition) != item.Definition {
			t.Fatal("reference is not canonical", item.Object)
		}
	}
	base := "CREATE TABLE `example` (\n  `name` varchar(100) COLLATE utf8mb4_0900_bin NOT NULL,\n  `body` text COLLATE utf8mb4_0900_bin,\n  `id` char(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin"
	explicit := strings.ReplaceAll(base, " COLLATE utf8mb4_0900_bin", " CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin")
	if canonicalDefinition(explicit) != base {
		t.Fatal("actual dump declaration representation not normalized")
	}
	for name, changed := range map[string]string{
		"charset":   strings.Replace(explicit, "CHARACTER SET utf8mb4 COLLATE", "CHARACTER SET latin1 COLLATE", 1),
		"collation": strings.Replace(explicit, "COLLATE utf8mb4_0900_bin", "COLLATE utf8mb4_0900_ai_ci", 1),
		"nullable":  strings.Replace(explicit, "NOT NULL", "DEFAULT NULL", 1),
		"type":      strings.Replace(explicit, "varchar(100)", "varchar(101)", 1),
		"column":    strings.Replace(explicit, "`name`", "`other`", 1),
		"table":     strings.Replace(explicit, "ENGINE=InnoDB", "ENGINE=MyISAM", 1),
		"ASCII":     strings.Replace(explicit, "CHARACTER SET ascii", "CHARACTER SET latin1", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if canonicalDefinition(changed) == base {
				t.Fatal("semantic drift hidden")
			}
		})
	}
	literal := "  `value` varchar(100) COLLATE utf8mb4_0900_bin DEFAULT ' CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin',"
	if canonicalDefinition("CREATE TABLE `example` (\n"+literal+"\n)") != "CREATE TABLE `example` (\n"+literal+"\n)" {
		t.Fatal("default data literal modified")
	}
	check := "  CONSTRAINT `check` CHECK ((`value` = ' CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin'))"
	if canonicalDefinition("CREATE TABLE `example` (\n"+check+"\n)") != "CREATE TABLE `example` (\n"+check+"\n)" {
		t.Fatal("CHECK data literal modified")
	}
}
