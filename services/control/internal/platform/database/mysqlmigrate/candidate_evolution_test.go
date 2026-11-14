package mysqlmigrate

import (
	"encoding/json"
	"strings"
	"testing"

	candidate "github.com/Passion-Flow/ownsolid/services/control/migrations/mysql/candidates/plugin-tool"
)

// Explicit test-only metadata parser envelopes. No value here is a measured
// information_schema observation, reference/evolution artifact or SQL acceptance.
func candidateCheckEnvelope(object string, names []string) string {
	rows := []string{"  `id` char(36) NOT NULL"}
	for _, name := range names {
		rows = append(rows, "  CONSTRAINT `"+name+"` CHECK ((`id` <> 'EXPLICIT_TEST_ONLY'))")
	}
	return "CREATE TABLE `" + object + "` (\n" + strings.Join(rows, ",\n") + "\n) ENGINE=InnoDB"
}
func TestMySQLCandidateEvolutionExactCheckReplacement(t *testing.T) {
	for _, fixture := range []struct {
		object, source string
		old, new       []string
	}{
		{"workflow_runs", workflowRunToolSourceSHA256, []string{"workflow_answer_bytes", "workflow_non_success_result_absent", "workflow_success_result_required", "unrelated_fixture_check"}, []string{"workflow_answer_bytes", "workflow_result_kind_check", "workflow_tool_result_object", "workflow_tool_proof_object", "workflow_tool_non_success_result_absent", "workflow_tool_success_result_required", "workflow_result_graph_kind", "unrelated_fixture_check"}},
		{"workflow_node_traces", workflowTraceToolSourceSHA256, []string{"workflow_node_traces_kind_check", "unrelated_fixture_check"}, []string{"workflow_node_traces_tool_kind_check", "unrelated_fixture_check"}},
	} {
		t.Run(fixture.object, func(t *testing.T) {
			previous := entry{reference: reference{Object: fixture.object, Definition: candidateCheckEnvelope(fixture.object, fixture.old)}}
			next := entry{reference: reference{Object: fixture.object, SourceSHA256: fixture.source, Definition: candidateCheckEnvelope(fixture.object, fixture.new)}}
			if !validWorkflowCheckEvolution(previous, next) {
				t.Fatal("exact owned replacement refused")
			}
			for _, change := range []string{"unrelated_expression", "unrelated_missing", "unrelated_extra", "duplicate", "auto_name", "disabled", "source_changed", "old_missing", "old_retained"} {
				before, after := previous, next
				switch change {
				case "unrelated_expression":
					after.Definition = strings.Replace(after.Definition, "`unrelated_fixture_check` CHECK ((`id` <> 'EXPLICIT_TEST_ONLY'))", "`unrelated_fixture_check` CHECK ((`id` = 'EXPLICIT_TEST_ONLY'))", 1)
				case "unrelated_missing":
					after.Definition = strings.Replace(after.Definition, "  CONSTRAINT `unrelated_fixture_check` CHECK ((`id` <> 'EXPLICIT_TEST_ONLY'))", "", 1)
				case "unrelated_extra":
					after.Definition = strings.Replace(after.Definition, "\n) ENGINE", ",\n  CONSTRAINT `extra_check` CHECK ((`id` <> 'EXPLICIT_TEST_ONLY'))\n) ENGINE", 1)
				case "duplicate":
					after.Definition = strings.Replace(after.Definition, "\n) ENGINE", ",\n  CONSTRAINT `unrelated_fixture_check` CHECK ((`id` <> 'EXPLICIT_TEST_ONLY'))\n) ENGINE", 1)
				case "auto_name":
					before.Definition = strings.Replace(before.Definition, "`"+fixture.old[len(fixture.old)-2]+"`", "`guessed_chk_99`", 1)
				case "disabled":
					after.Definition = strings.Replace(after.Definition, "CHECK ((`id` <> 'EXPLICIT_TEST_ONLY'))", "CHECK ((`id` <> 'EXPLICIT_TEST_ONLY')) /*!80016 NOT ENFORCED */", 1)
				case "source_changed":
					after.SourceSHA256 = hash("EXPLICIT_UNOWNED_SOURCE")
				case "old_missing":
					before.Definition = strings.Replace(before.Definition, "`"+fixture.old[0]+"`", "`missing_old_symbol`", 1)
				case "old_retained":
					after.Definition = strings.Replace(after.Definition, "\n) ENGINE", ",\n  CONSTRAINT `"+fixture.old[len(fixture.old)-2]+"` CHECK ((`id` <> 'EXPLICIT_TEST_ONLY'))\n) ENGINE", 1)
				}
				if validWorkflowCheckEvolution(before, after) {
					t.Fatal("unowned replacement admitted", change)
				}
			}
		})
	}
	for file, pin := range map[string]string{"000192_workflow_run_tool_result.sql": workflowRunToolSourceSHA256, "000193_workflow_trace_tool_kind.sql": workflowTraceToolSourceSHA256} {
		raw, e := candidate.Files.ReadFile(file)
		if e != nil || hash(string(raw)) != pin {
			t.Fatal("actual owned source fingerprint changed", file, e)
		}
	}
}
func TestMySQLCandidateEvolutionExactTriggerPositionAndPredecessor(t *testing.T) {
	for _, d := range []triggerDefinition{
		{Name: "workflow_run_result_identity", Table: "workflow_runs", Timing: "BEFORE", Event: "UPDATE", Order: 2},
		{Name: "user_preferences_initialize", Table: "users", Timing: "AFTER", Event: "INSERT", Order: 1},
		{Name: "workflow_terminal_notification", Table: "workflow_runs", Timing: "AFTER", Event: "UPDATE", Order: 1},
	} {
		if !ownedTriggerPosition(d) {
			t.Fatal("owned candidate position refused")
		}
		for _, mutate := range []func(*triggerDefinition){func(p *triggerDefinition) { p.Name = "unowned_trigger" }, func(p *triggerDefinition) { p.Table = "other_table" }, func(p *triggerDefinition) { p.Event = "DELETE" }, func(p *triggerDefinition) { p.Timing = "UNOWNED" }, func(p *triggerDefinition) { p.Order++ }} {
			other := d
			mutate(&other)
			if ownedTriggerPosition(other) {
				t.Fatal("unowned candidate position admitted", other)
			}
		}
	}
	d := triggerUnitDefinition()
	d.Name = "workflow_run_identity_guard"
	d.Table = "workflow_runs"
	d.Event = "UPDATE"
	raw, _ := json.Marshal(d)
	_, canonical, e := canonicalTrigger(string(raw))
	if e != nil {
		t.Fatal(e)
	}
	prior := map[string]entry{d.Name: {reference: reference{Kind: createTrigger, Definition: canonical}}}
	next := triggerDefinition{Name: "workflow_run_result_identity"}
	if !validWorkflowTriggerPredecessor(next, prior) || validWorkflowTriggerPredecessor(next, nil) {
		t.Fatal("original independent guard not required")
	}
	for _, change := range []string{"kind", "group", "order", "invalid"} {
		p := prior[d.Name]
		v := d
		switch change {
		case "kind":
			p.Kind = createTable
		case "group":
			v.Table = "other_table"
		case "order":
			v.Order = 2
		case "invalid":
			v.Statement = ""
		}
		if change != "kind" {
			raw, _ := json.Marshal(v)
			p.Definition = string(raw)
		}
		if validWorkflowTriggerPredecessor(next, map[string]entry{d.Name: p}) {
			t.Fatal("wrong predecessor admitted", change)
		}
	}
}
func TestMySQLCandidateEvolutionPreferenceSeedCanonicalBoundary(t *testing.T) {
	_, document, _ := evolutionFixture(t)
	document.Steps[0] = reference{Step: 16, File: "000016_user_preferences_seed.sql", Object: userPreferencesSeedObject, Kind: installationSeed, SourceSHA256: hash(userPreferencesSeedSQL + "\n"), Definition: userPreferencesSeedDefinition, DefinitionSHA256: hash(userPreferencesSeedDefinition)}
	raw, _ := json.Marshal(document)
	if _, e := CanonicalizeMeasuredEvolution(raw); e != nil {
		t.Fatal("exact semantic seed boundary refused", e)
	}
	document.Steps[0].Definition = "EXPLICIT_UNOWNED_SEED"
	document.Steps[0].DefinitionSHA256 = hash(document.Steps[0].Definition)
	raw, _ = json.Marshal(document)
	if _, e := CanonicalizeMeasuredEvolution(raw); e == nil {
		t.Fatal("arbitrary seed predicate admitted")
	}
}
