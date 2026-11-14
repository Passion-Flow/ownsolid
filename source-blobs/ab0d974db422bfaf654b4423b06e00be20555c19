package migrate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Reconstruct the immutable prefix independently of defaultCatalog registration.
// This continues to test the actual upgrade from 454 after central integration.
func inferenceCutoversBase(t *testing.T) catalog {
	t.Helper()
	c, err := accountCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []func(map[string]table) schemaFragment{
		accountMailFragment, artifactStoresFragment, nodesComponentsFragment,
		datasetsFragment, modelsFragment, applicationsFragment, artifactStoresTargetsFragment,
		knowledgeFragment, middlewareFragment, mongoDatasetsFragment, taskDeliveriesFragment,
		trainingFragment, modelPreparationsFragment, registryFragment, privateclustersFragment,
	} {
		if err := appendFragment(&c, fragment(c.project(len(c.steps)).tables), c.fragments); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.steps) != 454 {
		t.Fatal("retained prefix changed", len(c.steps))
	}
	return c
}

func inferenceCutoversCatalog(t *testing.T) (catalog, schemaFragment) {
	t.Helper()
	c := inferenceCutoversBase(t)
	f := inferenceCutoversFragment(c.project(len(c.steps)).tables)
	if err := appendFragment(&c, f, c.fragments); err != nil {
		t.Fatal(err)
	}
	return c, f
}

func inferenceConstraint(t *testing.T, owner table, name string) constraint {
	t.Helper()
	for _, c := range owner.Constraints {
		if c.Name == name {
			return c
		}
	}
	t.Fatal("missing constraint", owner.Name, name)
	return constraint{}
}

func TestInferenceCutoversAppendOnlyProjection(t *testing.T) {
	base := inferenceCutoversBase(t)
	p := base.project(454)
	f := inferenceCutoversFragment(p.tables)
	if !reflect.DeepEqual(p, base.project(454)) || !reflect.DeepEqual(f, inferenceCutoversFragment(p.tables)) {
		t.Fatal("builder mutated its input or is nondeterministic")
	}
	before := append([]step(nil), base.steps...)
	if err := appendFragment(&base, f, base.fragments); err != nil {
		t.Fatal(err)
	}
	if len(base.steps) != 471 || len(base.tables) != 106 || !reflect.DeepEqual(before, base.steps[:454]) || !reflect.DeepEqual(p, base.project(454)) {
		t.Fatal("retained SQL/checksums/projection changed or contribution incomplete")
	}
	for _, s := range base.steps[454:] {
		if strings.Contains(s.SQL, "$1") || strings.Contains(s.SQL, "::") || strings.Contains(s.SQL, "ENGINE=") || strings.Contains(s.SQL, "ON CONFLICT") || strings.Contains(s.SQL, "OR REPLACE") {
			t.Fatal("foreign dialect or replacement in appended native operation", s.Object)
		}
		v := base.evolution[s.Number]
		if v.Kind == "alter" && (!reflect.DeepEqual(*v.Before, base.project(s.Number - 1).tables[v.Before.Name]) || !reflect.DeepEqual(*v.After, base.project(s.Number).tables[v.After.Name])) {
			t.Fatal("dirty operation lacks exact before/after projection", s.Object)
		}
	}
	final := base.project(len(base.steps))
	for _, name := range []string{"MODEL_ALIAS_HISTORY_UPDATE", "MODEL_ALIAS_HISTORY_DELETE", "MODEL_ALIAS_TARGET_VERSIONS_UPDATE", "MODEL_ALIAS_TARGET_VERSIONS_DELETE", "DEPLOYMENT_MODEL_IDENTITY"} {
		if !reflect.DeepEqual(p.triggers[name], final.triggers[name]) || final.triggers[name].Name == "" {
			t.Fatal("existing immutable identity/history guard changed", name)
		}
	}
	// Successor constraints must exist before old restrictions are dropped. A
	// dirty DDL prefix must never make deployment/alias variants unconstrained.
	for _, pair := range [][3]string{
		{"DEPLOYMENTS", "DEPLOYMENTS_VARIANT", "IC_DEPLOYMENTS_VARIANT"},
		{"MODEL_ALIAS_HISTORY", "ALIAS_HISTORY_ACTION", "IC_ALIAS_HISTORY_ACTION"},
		{"MODEL_ALIAS_TARGET_VERSIONS", "ALIAS_TARGET_FIRST_PATH", "IC_ALIAS_TARGET_VARIANT"},
	} {
		for prefix := 454; prefix <= len(base.steps); prefix++ {
			found := false
			for _, c := range base.project(prefix).tables[pair[0]].Constraints {
				found = found || c.Name == pair[1] || c.Name == pair[2]
			}
			if !found {
				t.Fatal("unguarded dirty prefix", prefix, pair[0])
			}
		}
	}
}

func TestInferenceCutoversRejectMissingPublicationOwner(t *testing.T) {
	c := inferenceCutoversBase(t)
	f := inferenceCutoversFragment(c.project(454).tables)
	delete(c.fragments, "model_preparations_v2")
	if err := appendFragment(&c, f, c.fragments); FailureCode(err) != "DAMENG_SCHEMA_FRAGMENT_DEPENDENCY_REQUIRED" || len(c.steps) != 454 {
		t.Fatal("derived variants enabled without their publication graph", err)
	}
}

func TestInferenceCutoversExactScopedForeignKeys(t *testing.T) {
	c, _ := inferenceCutoversCatalog(t)
	p := c.project(len(c.steps))
	for _, tc := range []struct {
		owner, name, target string
		local, remote       []string
	}{
		{"INFERENCE_NODES", "INFERENCE_NODES_NODE_FK", "MANAGED_NODES", []string{"TENANT_ID", "WORKSPACE_ID", "NODE_ID"}, []string{"TENANT_ID", "WORKSPACE_ID", "ID"}},
		{"MODEL_ALIAS_TARGET_VERSIONS", "IC_ALIAS_TARGET_SOURCE_FK", "MODEL_ALIAS_TARGET_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "SOURCE_REVISION"}, []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"}},
		{"MODEL_ALIAS_CUTOVER_INTENTS", "IC_ALIAS_INTENT_TARGET_FK", "MODEL_ALIAS_TARGET_VERSIONS", []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "RESULT_REVISION"}, []string{"TENANT_ID", "WORKSPACE_ID", "ALIAS_ID", "REVISION"}},
		{"MODEL_ALIAS_CUTOVER_INTENTS", "IC_ALIAS_INTENT_ACTOR_FK", "USERS", []string{"ACTOR_USER_ID"}, []string{"ID"}},
	} {
		fk := inferenceConstraint(t, p.tables[tc.owner], tc.name)
		key := inferenceConstraint(t, p.tables[tc.target], fk.ReferenceKey)
		if fk.Kind != "R" || fk.Reference != tc.target || !reflect.DeepEqual(fk.Columns, tc.local) || !reflect.DeepEqual(key.Columns, tc.remote) {
			t.Fatal("foreign key lost exact tenant/workspace/alias revision scope", tc.name)
		}
	}
	pk := inferenceConstraint(t, p.tables["MODEL_ALIAS_CUTOVER_INTENTS"], "IC_ALIAS_INTENT_PK")
	if !reflect.DeepEqual(pk.Columns, []string{"TENANT_ID", "WORKSPACE_ID", "ACTOR_USER_ID", "IDEMPOTENCY_KEY"}) {
		t.Fatal("cutover idempotency namespace changed")
	}
	for _, col := range p.tables["MODEL_ALIAS_CUTOVER_INTENTS"].Columns {
		if col.Nullable || col.Name == "FINGERPRINT" && (col.Type != "VARBINARY" || col.Width != 32) {
			t.Fatal("intent admits incomplete identity or fingerprint", col.Name)
		}
	}
}

func TestInferenceCutoversMatchFrozenPostgreSQLContracts(t *testing.T) {
	c, _ := inferenceCutoversCatalog(t)
	p := c.project(len(c.steps))
	root := filepath.Join("..", "..", "testdata", "platform-lineage")
	pg6, err := os.ReadFile(filepath.Join(root, "000006_models.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	pg10, err := os.ReadFile(filepath.Join(root, "000010_model_lifecycle.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	// Independent contract anchors make source changes require explicit review.
	for _, anchor := range []string{"CREATE TABLE inference_nodes (", "node_id uuid PRIMARY KEY", "octet_length(observation::text)<=32768", "FOREIGN KEY(tenant_id,workspace_id,node_id) REFERENCES managed_nodes(tenant_id,workspace_id,id)"} {
		if !strings.Contains(string(pg6), anchor) {
			t.Fatal("PG000006 source contract changed", anchor)
		}
	}
	for _, anchor := range []string{"catalog_digest='" + inferenceCutoverBaseDigest + "'", "catalog_digest<>'" + inferenceCutoverBaseDigest + "'", "action IN ('migration_current','published','disabled','paused','resumed','retargeted','rolled_back')", "source_revision<revision AND source_revision>=1", "PRIMARY KEY(tenant_id,workspace_id,actor_user_id,idempotency_key)", "d.serving_recipe_sha256 IS NOT DISTINCT FROM NEW.serving_recipe_sha256"} {
		if !strings.Contains(string(pg10), anchor) {
			t.Fatal("PG000010 source contract changed", anchor)
		}
	}
	for _, tc := range []struct{ owner, name, expression string }{
		{"INFERENCE_NODES", "INFERENCE_NODES_OBSERVATION", `"OBSERVATION" IS NULL OR (JSON_TYPE("OBSERVATION") IS NOT NULL AND JSON_TYPE("OBSERVATION")='OBJECT' AND OCTET_LENGTH("OBSERVATION")<=32768)`},
		{"MODEL_ALIAS_HISTORY", "IC_ALIAS_HISTORY_ACTION", `"ACTION" IN ('published','disabled','paused','resumed','retargeted','rolled_back') AND CHAR_LENGTH("REASON")>0`},
		{"MODEL_ALIAS_TARGET_VERSIONS", "IC_ALIAS_TARGET_ACTION", `"ACTION" IN ('migration_current','published','disabled','paused','resumed','retargeted','rolled_back')`},
		{"MODEL_ALIAS_TARGET_VERSIONS", "IC_ALIAS_TARGET_SOURCE", `("ACTION"='rolled_back' AND "SOURCE_REVISION" IS NOT NULL AND "SOURCE_REVISION">=1 AND "SOURCE_REVISION"<"REVISION") OR ("ACTION"<>'rolled_back' AND "SOURCE_REVISION" IS NULL)`},
		{"MODEL_ALIAS_CUTOVER_INTENTS", "IC_ALIAS_INTENT_FINGERPRINT", `OCTET_LENGTH("FINGERPRINT")=32`},
	} {
		if inferenceConstraint(t, p.tables[tc.owner], tc.name).Check != tc.expression {
			t.Fatal("native constraint drifted from frozen behavior", tc.name)
		}
	}
	observation := p.tables["INFERENCE_NODES"].Columns[3]
	if observation.Name != "OBSERVATION" || observation.Type != "CLOB" || observation.Nullable {
		t.Fatal("inference observation must be a required native JSON CLOB")
	}
	variant := inferenceConstraint(t, p.tables["DEPLOYMENTS"], "IC_DEPLOYMENTS_VARIANT").Check
	for _, required := range []string{`"MODEL_KIND"='approved_base'`, `"CATALOG_DIGEST"='` + inferenceCutoverBaseDigest + `'`, `"SERVING_RECIPE_SHA256" IS NULL`, `"MODEL_KIND"='merged_lora'`, `"CATALOG_DIGEST"<>'` + inferenceCutoverBaseDigest + `'`, `"SERVING_RECIPE_SHA256" IS NOT NULL`, `REGEXP_LIKE("SERVING_RECIPE_SHA256",'^[a-f0-9]{64}$','c')`} {
		if !strings.Contains(variant, required) {
			t.Fatal("deployment variant missing frozen identity guard", required)
		}
	}
	for _, col := range p.tables["DEPLOYMENTS"].Columns {
		if col.Name == "CATALOG_DIGEST" && col.Default != "'"+inferenceCutoverBaseDigest+"'" {
			t.Fatal("PG000010 deployment default not retained")
		}
	}
}

func TestInferenceCutoversGuardsRequireActualSnapshotAndImmutableIdentities(t *testing.T) {
	c, _ := inferenceCutoversCatalog(t)
	p := c.project(len(c.steps))
	snapshot := p.triggers["IC_ALIAS_TARGET_SNAPSHOT"]
	for _, required := range []string{
		`D."TENANT_ID"=A."TENANT_ID"`, `D."WORKSPACE_ID"=A."WORKSPACE_ID"`, `D."NODE_ID"=A."NODE_ID"`, `D."ID"=A."DEPLOYMENT_ID"`,
		`A."TENANT_ID"=:NEW."TENANT_ID"`, `A."WORKSPACE_ID"=:NEW."WORKSPACE_ID"`, `A."ID"=:NEW."ALIAS_ID"`, `A."REVISION"=:NEW."REVISION"`, `A."DEPLOYMENT_REVISION"=:NEW."DEPLOYMENT_REVISION"`,
		`D."ID"=:NEW."DEPLOYMENT_ID"`, `D."MODEL_ID"=:NEW."MODEL_ID"`, `D."MODEL_KIND"=:NEW."MODEL_KIND"`, `D."CATALOG_DIGEST"=:NEW."CATALOG_DIGEST"`,
		`D."SERVING_RECIPE_SHA256"=:NEW."SERVING_RECIPE_SHA256"`, `(D."SERVING_RECIPE_SHA256" IS NULL AND :NEW."SERVING_RECIPE_SHA256" IS NULL)`,
		`IF MATCH_COUNT<>1 THEN RAISE_APPLICATION_ERROR(-20063,'ALIAS_CURRENT_TARGET_SNAPSHOT_REQUIRED')`,
	} {
		if !strings.Contains(snapshot.Body, required) {
			t.Fatal("actual alias/deployment snapshot predicate missing", required)
		}
	}
	identity := p.triggers["IC_ALIAS_IDENTITY"]
	for _, field := range []string{"ID", "TENANT_ID", "WORKSPACE_ID", "NODE_ID", "GATEWAY_ID", "NAME", "CREATED_BY", "IDEMPOTENCY_KEY", "FINGERPRINT", "CREATED_AT"} {
		for _, required := range []string{`:NEW.` + quoted(field) + `<>:OLD.` + quoted(field), `:NEW.` + quoted(field) + ` IS NULL AND :OLD.` + quoted(field) + ` IS NOT NULL`, `:NEW.` + quoted(field) + ` IS NOT NULL AND :OLD.` + quoted(field) + ` IS NULL`} {
			if !strings.Contains(identity.Body, required) {
				t.Fatal("alias identity lost NULL-aware protection", field)
			}
		}
	}
	for _, mutable := range []string{"DEPLOYMENT_ID", "DEPLOYMENT_REVISION", "REVISION", "DISABLED_AT"} {
		if strings.Contains(identity.Body, `:NEW.`+quoted(mutable)) {
			t.Fatal("identity guard prevents legitimate cutover", mutable)
		}
	}
	for _, name := range []string{"IC_ALIAS_IDENTITY", "IC_ALIAS_TARGET_SNAPSHOT", "IC_ALIAS_INTENT_UPDATE", "IC_ALIAS_INTENT_DELETE"} {
		guard := p.triggers[name]
		ddl := "CREATE TRIGGER " + quoted(guard.Name) + " BEFORE " + guard.Event + " ON " + quoted(guard.Table) + " FOR EACH ROW " + guard.Body
		if !triggerDefinitionMatches(ddl, "FIXTURE", guard) {
			t.Fatal("production metadata decoder rejects exact trigger", name)
		}
		for _, changed := range []string{strings.Replace(ddl, "FOR EACH ROW", "FOR EACH ROW WHEN (1=0)", 1), ddl + " SELECT 1 FROM DUAL", strings.Replace(ddl, "BEFORE ", "AFTER ", 1), strings.Replace(ddl, "RAISE_APPLICATION_ERROR", "NULL; --", 1)} {
			if triggerDefinitionMatches(changed, "FIXTURE", guard) {
				t.Fatal("disabled/changed guard admitted by production metadata decoder", name)
			}
		}
	}
}

func TestInferenceCutoversCurrentBackfillRetainsExistingHistory(t *testing.T) {
	_, f := inferenceCutoversCatalog(t)
	for _, change := range f.Changes {
		if change.ID != "IC_ALIAS_CURRENT_BACKFILL" {
			continue
		}
		if change.Kind != "backfill" || change.UUIDParameters != 0 || change.AfterCount != 0 || !reflect.DeepEqual(change.ProbeTables, []string{"MODEL_ALIASES", "MODEL_ALIAS_TARGET_VERSIONS", "DEPLOYMENTS"}) {
			t.Fatal("backfill lost its bounded zero-missing postcondition")
		}
		for _, part := range []string{`SELECT A."ID",A."TENANT_ID",A."WORKSPACE_ID",A."ID",A."REVISION",'migration_current'`, `WHERE NOT EXISTS (SELECT 1 FROM "MODEL_ALIAS_TARGET_VERSIONS" T`, `T."TENANT_ID"=A."TENANT_ID" AND T."WORKSPACE_ID"=A."WORKSPACE_ID" AND T."ALIAS_ID"=A."ID" AND T."REVISION"=A."REVISION"`} {
			if !strings.Contains(change.SQL, part) {
				t.Fatal("backfill may duplicate/overwrite retained current target", part)
			}
		}
		for _, part := range []string{`T."DEPLOYMENT_ID"=D."ID"`, `T."DEPLOYMENT_REVISION"=A."DEPLOYMENT_REVISION"`, `T."MODEL_ID"=D."MODEL_ID"`, `T."MODEL_KIND"=D."MODEL_KIND"`, `T."CATALOG_DIGEST"=D."CATALOG_DIGEST"`, `T."SERVING_RECIPE_SHA256"=D."SERVING_RECIPE_SHA256"`, `T."SERVING_RECIPE_SHA256" IS NULL AND D."SERVING_RECIPE_SHA256" IS NULL`} {
			if !strings.Contains(change.Probe, part) {
				t.Fatal("postcondition accepts a mismatching pre-existing current snapshot", part)
			}
		}
		return
	}
	t.Fatal("existing alias current snapshot upgrade missing")
}

// Real migration state-machine tests with explicit effect/acknowledgement doubles.
// They establish no native SQL execution or actual vendor transaction behavior.
func TestInferenceCutoversUnknownEffectsRequireExplicitRecovery(t *testing.T) {
	c, _ := inferenceCutoversCatalog(t)
	for number := 455; number <= len(c.steps); number++ {
		for _, effect := range []bool{false, true} {
			name := c.steps[number-1].Object + "/absent"
			if effect {
				name = c.steps[number-1].Object + "/present"
			}
			t.Run(name, func(t *testing.T) {
				prefix := c
				prefix.steps = c.steps[:number]
				b := &fixtureBackend{state: snapshot{Journal: true, Present: map[string]bool{}}, executeFailure: true, effectBeforeFailure: effect}
				for _, s := range c.steps[:number-1] {
					b.state.Entries = append(b.state.Entries, journalEntry{s.Number, Scope, s.SHA256, s.Object, "applied", true, true})
					b.state.Present[s.Object] = true
				}
				if _, err := run(testContext(t), prefix, b, Apply); err == nil || b.executes != 1 || b.starts != 1 {
					t.Fatal("lost acknowledgement was accepted", err)
				}
				if _, err := run(testContext(t), prefix, b, Apply); FailureCode(err) != "DAMENG_SCHEMA_RECONCILIATION_REQUIRED" || b.executes != 1 {
					t.Fatal("ordinary apply replayed an uncertain operation", err)
				}
				b.executeFailure = false
				if effect {
					if _, err := run(testContext(t), prefix, b, Reconcile); err != nil || b.executes != 1 {
						t.Fatal("matching effect should reconcile without dispatch", err)
					}
				} else {
					if _, err := run(testContext(t), prefix, b, Reconcile); FailureCode(err) != "DAMENG_SCHEMA_EXPLICIT_RETRY_REQUIRED" || b.executes != 1 {
						t.Fatal("absent effect was silently retried", err)
					}
					if _, err := run(testContext(t), prefix, b, Retry); err != nil || b.executes != 2 || b.starts != 1 {
						t.Fatal("explicit retry did not retain original intent", err)
					}
				}
				if report, err := run(testContext(t), prefix, b, Readiness); err != nil || !report.FoundationReady || report.PlatformReady {
					t.Fatal("recovery failed or falsely granted platform readiness", report, err)
				}
			})
		}
	}
}
