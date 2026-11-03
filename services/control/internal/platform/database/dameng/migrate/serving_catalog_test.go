package migrate

import (
	"reflect"
	"strings"
	"testing"
)

func TestControlServingCatalogMatchesCompleteCompiledContract(t *testing.T) {
	if err := RequireControlServingCatalog(); err != nil {
		t.Fatal(err)
	}
	c, err := defaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	p := c.project(len(c.steps))
	if len(c.steps) != 476 || len(c.tables) != 108 || len(p.tables) != 109 {
		t.Fatal("complete reviewed native graph absent")
	}
	for _, name := range []string{"INFERENCE_NODES", "MODEL_ALIAS_CUTOVER_INTENTS", "AWS_DISCOVERED_RESOURCES", "AWS_IMPORT_RECEIPTS", "MODEL_PUBLICATIONS"} {
		if p.tables[name].Name != name {
			t.Fatal("missing serving relation", name)
		}
	}
	if !c.fragments["inference_cutovers_v2"] || !c.fragments["cloudinventory_v2"] {
		t.Fatal("current contributions not registered")
	}
	g := requiredGrants(c)
	if len(g) != 109 || !g["MODEL_ALIAS_TARGET_VERSIONS"]["REFERENCES"] || g["AWS_IMPORT_RECEIPTS"]["REFERENCES"] || !reflect.DeepEqual(g[journalName], map[string]bool{"SELECT": true}) {
		t.Fatal("restricted grant graph drift")
	}
}

func TestControlServingCatalogRejectsMissingAndSameSizeDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*catalog)
	}{
		{"foundation_only", func(c *catalog) {
			v, err := accountCatalog()
			if err != nil {
				t.Fatal(err)
			}
			*c = v
		}},
		{"missing_inference_registration", func(c *catalog) { c.steps = c.steps[:459] }},
		{"same_count_changed_sql", func(c *catalog) { c.steps[0].SQL += " /* drift */" }},
		{"same_count_changed_checksum", func(c *catalog) { c.steps[0].SHA256 = strings.Repeat("0", 64) }},
		{"changed_number", func(c *catalog) { c.steps[0].Number = 2 }},
		{"seed_replaced", func(c *catalog) { c.steps[30].SQL = `INSERT INTO "INSTALLATION_STATE" ("ID") VALUES (2)` }},
		{"column_nullable", func(c *catalog) { c.tables[0].Columns[0].Nullable = true }},
		{"constraint_missing", func(c *catalog) { c.tables[0].Constraints = nil }},
		{"journal_changed", func(c *catalog) { c.journal.Columns[0].Nullable = true }},
		{"relation_replaced", func(c *catalog) { c.tables[0].Name = "UNREVIEWED_RELATION" }},
		{"fragment_removed", func(c *catalog) { delete(c.fragments, "inference_cutovers_v2") }},
		{"fragment_added", func(c *catalog) { c.fragments["unreviewed_v2"] = true }},
		{"delete_authority_added", func(c *catalog) { c.deleteRules["UNREVIEWED_FK"] = "CASCADE" }},
		{"evolution_removed", func(c *catalog) { delete(c.evolution, 32) }},
		{"trigger_replaced", func(c *catalog) {
			for n, x := range c.evolution {
				if x.Trigger != nil {
					v := *x.Trigger
					v.Body = "BEGIN NULL; END;"
					x.Trigger = &v
					c.evolution[n] = x
					return
				}
			}
			t.Fatal("missing fixture trigger")
		}},
		{"index_direction_replaced", func(c *catalog) {
			for n, x := range c.evolution {
				if x.Index != nil {
					v := *x.Index
					v.Descending = append([]bool(nil), v.Descending...)
					v.Descending[0] = !v.Descending[0]
					x.Index = &v
					c.evolution[n] = x
					return
				}
			}
			t.Fatal("missing fixture index")
		}},
		{"backfill_probe_replaced", func(c *catalog) {
			for n, x := range c.evolution {
				if x.Kind == "backfill" {
					x.Probe = "SELECT 0 FROM DUAL"
					c.evolution[n] = x
					return
				}
			}
			t.Fatal("missing fixture backfill")
		}},
		{"foreign_scope_reduced", func(c *catalog) {
			for n, x := range c.evolution {
				if x.After != nil && x.After.Name == "MODEL_ALIAS_CUTOVER_INTENTS" {
					v := cloneTable(*x.After)
					for i := range v.Constraints {
						if v.Constraints[i].Name == "IC_ALIAS_INTENT_TARGET_FK" {
							v.Constraints[i].Columns = []string{"ALIAS_ID"}
						}
					}
					x.After = &v
					c.evolution[n] = x
					return
				}
			}
			t.Fatal("missing fixture intent")
		}},
		{"foreign_target_missing", func(c *catalog) {
			for n := range c.tables {
				for i := range c.tables[n].Constraints {
					if c.tables[n].Constraints[i].Kind == "R" {
						c.tables[n].Constraints[i].Reference = "UNREVIEWED_TARGET"
						return
					}
				}
			}
			t.Fatal("missing fixture foreign key")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := defaultCatalog()
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&c)
			if FailureCode(requireControlServingCatalog(c)) != "DAMENG_CONTROL_SERVING_CATALOG_INCOMPLETE" {
				t.Fatal("changed catalog admitted")
			}
		})
	}
}

func TestControlServingWitnessBindsRestrictedGrantBytes(t *testing.T) {
	c, err := defaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	p := c.project(len(c.steps))
	grants := requiredGrants(c)
	before, err := controlServingCatalogDigest(c, p, grants)
	if err != nil || before != controlServingCatalogSHA256 {
		t.Fatal("approved witness mismatch", err)
	}
	grants[journalName]["INSERT"] = true
	after, err := controlServingCatalogDigest(c, p, grants)
	if err != nil || after == before {
		t.Fatal("journal write authority absent from witness", err)
	}
}

func TestControlServingInstallationLockUsesValidatedOriginalNamespace(t *testing.T) {
	for _, schema := range []string{"APP", "P1_OWNER", "ISOLATED_TEST_123"} {
		id, err := SchemaLockID(schema)
		if err != nil || InstallationLockID(schema) != id || id != lockID(schema) {
			t.Fatal("forked installation lock namespace", err)
		}
	}
	for _, schema := range []string{"", "SYS", "SYSDBA", "LOWERcase", "P1;SELECT"} {
		if InstallationLockID(schema) != -1 {
			t.Fatal("invalid installation identity admitted")
		}
	}
}
