package mysqlmigrate

import (
	"regexp"
	"strings"
)

// These are fingerprints of real owned DDL bytes, not generated table metadata.
// They grant only these two reviewed CHECK replacements; any future source shape
// requires a separate first-party contract and actual new measurement.
const workflowRunToolSourceSHA256 = "892b0e8951cd9cb162dd242084784b011d939e2e947716fff69c367a33848f79"
const workflowTraceToolSourceSHA256 = "51c0fe620ef2e5f146beb1ee027e037a56e6574589b6c501959dc9fa2eaafe73"

var workflowCheckLine = regexp.MustCompile("^  CONSTRAINT `([a-z][a-z0-9_]{0,63})` CHECK \\(")

// Read exact SHOW CREATE TABLE CHECK lines. This does not parse/translate DDL,
// normalize an expression, guess a generated name or synthesize a definition.
// Unexpected multiline/unenforced metadata fails closed for these owned shapes.
func workflowChecks(definition string) (map[string]string, bool) {
	checks := map[string]string{}
	for _, line := range strings.Split(definition, "\n") {
		if !strings.Contains(line, "CHECK") {
			continue
		}
		found := workflowCheckLine.FindStringSubmatch(line)
		if len(found) != 2 || checks[found[1]] != "" || strings.Contains(line, "NOT ENFORCED") || !strings.HasSuffix(strings.TrimSuffix(line, ","), ")") {
			return nil, false
		}
		checks[found[1]] = strings.TrimSuffix(line, ",")
	}
	return checks, len(checks) > 0
}
func validWorkflowCheckEvolution(previous, next entry) bool {
	var removed, added []string
	switch next.Object {
	case "workflow_runs":
		if next.SourceSHA256 != workflowRunToolSourceSHA256 {
			return false
		}
		removed = []string{"workflow_non_success_result_absent", "workflow_success_result_required"}
		added = []string{"workflow_result_kind_check", "workflow_tool_result_object", "workflow_tool_proof_object", "workflow_tool_non_success_result_absent", "workflow_tool_success_result_required", "workflow_result_graph_kind"}
	case "workflow_node_traces":
		if next.SourceSHA256 != workflowTraceToolSourceSHA256 {
			return false
		}
		removed = []string{"workflow_node_traces_kind_check"}
		added = []string{"workflow_node_traces_tool_kind_check"}
	default:
		return true
	}
	before, ok := workflowChecks(previous.Definition)
	if !ok {
		return false
	}
	after, ok := workflowChecks(next.Definition)
	if !ok {
		return false
	}
	for _, name := range removed {
		if before[name] == "" || after[name] != "" {
			return false
		}
		delete(before, name)
	}
	for _, name := range added {
		if before[name] != "" || after[name] == "" {
			return false
		}
		delete(after, name)
	}
	if len(before) != len(after) {
		return false
	}
	for name, expression := range before {
		if after[name] != expression {
			return false
		}
	}
	return true
}

func validWorkflowTriggerPredecessor(d triggerDefinition, definitions map[string]entry) bool {
	if d.Name != "workflow_run_result_identity" {
		return true
	}
	predecessor, exists := definitions["workflow_run_identity_guard"]
	prior, _, e := canonicalTrigger(predecessor.Definition)
	return exists && e == nil && predecessor.Kind == createTrigger && prior.Table == "workflow_runs" && prior.Timing == "BEFORE" && prior.Event == "UPDATE" && prior.Order == 1
}
