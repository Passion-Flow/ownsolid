package workflows

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strings"
	"testing"
)

func validGraph(t *testing.T) Graph {
	t.Helper()
	ids := []string{uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()}
	retrieve, _ := json.Marshal(RetrievalConfiguration{BaseID: uuid.NewString(), TopK: 5, MinimumScore: 0.2})
	answer, _ := json.Marshal(AnswerConfiguration{AliasID: uuid.NewString(), Instructions: "Answer in one sentence using the supplied evidence."})
	return Graph{SchemaVersion: 1, Nodes: []Node{{ID: ids[0], Kind: StartNode, Configuration: json.RawMessage(`{}`)}, {ID: ids[1], Kind: RetrievalNode, Configuration: retrieve}, {ID: ids[2], Kind: AnswerNode, Configuration: answer}, {ID: ids[3], Kind: EndNode, Configuration: json.RawMessage(`{}`)}}, Edges: []Edge{{ID: uuid.NewString(), Source: ids[0], Target: ids[1]}, {ID: uuid.NewString(), Source: ids[1], Target: ids[2]}, {ID: uuid.NewString(), Source: ids[2], Target: ids[3]}}}
}
func parseTestGraph(t *testing.T, g Graph) ParsedGraph {
	t.Helper()
	raw, e := json.Marshal(g)
	if e != nil {
		t.Fatal(e)
	}
	p, e := ParseGraph(raw, config.DefaultWorkflowPolicy(), 20)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestCanonicalGraphPreservesContentAcrossOrderingAndReload(t *testing.T) {
	g := validGraph(t)
	p := parseTestGraph(t, g)
	if !p.ValidateExecutable().Valid {
		t.Fatal("valid connected initial path refused")
	}
	g.Nodes[0], g.Nodes[3] = g.Nodes[3], g.Nodes[0]
	g.Edges[0], g.Edges[2] = g.Edges[2], g.Edges[0]
	reordered := parseTestGraph(t, g)
	if reordered.SHA256 != p.SHA256 || string(reordered.CanonicalJSON) != string(p.CanonicalJSON) {
		t.Fatal("equivalent graph ordering changed canonical identity")
	}
	restored, e := ParseGraph(p.CanonicalJSON, config.DefaultWorkflowPolicy(), 20)
	if e != nil || restored.SHA256 != p.SHA256 || !restored.ValidateExecutable().Valid {
		t.Fatal("canonical reload lost executable state", e)
	}
}
func TestEditableIncompleteDraftCannotExecute(t *testing.T) {
	g := validGraph(t)
	g.Edges = []Edge{}
	g.Nodes[1].Configuration = json.RawMessage(`{"base_id":"","top_k":5,"minimum_score":0.2}`)
	g.Nodes[2].Configuration = json.RawMessage(`{"alias_id":"","instructions":""}`)
	p := parseTestGraph(t, g)
	if p.ValidateExecutable().Valid {
		t.Fatal("incomplete saved draft became executable")
	}
}
func TestExecutableTopologyRejectsCycleReverseAndDisconnectedGraph(t *testing.T) {
	for _, name := range []string{"cycle", "reversed", "disconnected", "extra"} {
		t.Run(name, func(t *testing.T) {
			g := validGraph(t)
			switch name {
			case "cycle":
				g.Edges[2].Target = g.Nodes[1].ID
			case "reversed":
				g.Edges[1].Source, g.Edges[1].Target = g.Edges[1].Target, g.Edges[1].Source
			case "disconnected":
				g.Edges = g.Edges[:2]
			case "extra":
				g.Edges = append(g.Edges, Edge{ID: uuid.NewString(), Source: g.Nodes[0].ID, Target: g.Nodes[3].ID})
			}
			if parseTestGraph(t, g).ValidateExecutable().Valid {
				t.Fatal("invalid topology became executable")
			}
		})
	}
}
func TestGraphRejectsMalformedOrExecutableConfiguration(t *testing.T) {
	for _, name := range []string{"unknown_kind", "duplicate_node", "duplicate_edge", "self_edge", "missing_endpoint", "unknown_configuration", "wrong_number", "bad_identifier", "oversized_prompt", "bad_position"} {
		t.Run(name, func(t *testing.T) {
			g := validGraph(t)
			switch name {
			case "unknown_kind":
				g.Nodes[1].Kind = "shell"
			case "duplicate_node":
				g.Nodes[2].ID = g.Nodes[1].ID
			case "duplicate_edge":
				g.Edges[1].ID = g.Edges[0].ID
			case "self_edge":
				g.Edges[0].Target = g.Edges[0].Source
			case "missing_endpoint":
				g.Edges[0].Target = uuid.NewString()
			case "unknown_configuration":
				g.Nodes[2].Configuration = json.RawMessage(`{"alias_id":"","instructions":"","command":"execute"}`)
			case "wrong_number":
				g.Nodes[1].Configuration = json.RawMessage(`{"base_id":"","top_k":1.5,"minimum_score":0}`)
			case "bad_identifier":
				g.Nodes[1].Configuration = json.RawMessage(`{"base_id":"../foreign","top_k":5,"minimum_score":0}`)
			case "oversized_prompt":
				v, _ := json.Marshal(AnswerConfiguration{AliasID: uuid.NewString(), Instructions: strings.Repeat("x", 4097)})
				g.Nodes[2].Configuration = v
			case "bad_position":
				g.Nodes[0].Position.X = MaximumPositionMagnitude + 1
			}
			raw, e := json.Marshal(g)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = ParseGraph(raw, config.DefaultWorkflowPolicy(), 20); e == nil {
				t.Fatal("malformed graph accepted")
			}
		})
	}
	raw, _ := json.Marshal(validGraph(t))
	for _, bad := range []string{strings.Replace(string(raw), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1), strings.Replace(string(raw), `"x":0,"y":0`, `"x":0`, 1), strings.Replace(string(raw), `"position":{"x":0,"y":0}`, `"position":null`, 1), string(raw) + `{}`} {
		if _, e := ParseGraph([]byte(bad), config.DefaultWorkflowPolicy(), 20); e == nil {
			t.Fatal("duplicate/missing/null/trailing graph accepted")
		}
	}
}
