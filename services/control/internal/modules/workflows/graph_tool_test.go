package workflows

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func validToolGraph() Graph {
	start, tool, end := uuid.NewString(), uuid.NewString(), uuid.NewString()
	raw, _ := json.Marshal(ToolConfiguration{PluginVersionID: uuid.NewString()})
	return Graph{SchemaVersion: ToolGraphSchemaVersion, Nodes: []Node{{ID: start, Kind: StartNode, Configuration: json.RawMessage(`{}`)}, {ID: tool, Kind: ToolNode, Configuration: raw}, {ID: end, Kind: EndNode, Configuration: json.RawMessage(`{}`)}}, Edges: []Edge{{ID: uuid.NewString(), Source: start, Target: tool}, {ID: uuid.NewString(), Source: tool, Target: end}}}
}
func TestSignedToolGraphRemainsExplicitStrictAndSeparatesAnswerContract(t *testing.T) {
	policy := config.DefaultRuntimePolicy().Workflows
	parse := func(g Graph) (ParsedGraph, error) { raw, _ := json.Marshal(g); return ParseGraph(raw, policy, 100) }
	valid := validToolGraph()
	parsed, e := parse(valid)
	if e != nil || !parsed.IsTool() || parsed.ResultKind() != "model_list" || !parsed.ValidateExecutable().Valid || parsed.Tool.PluginVersionID == "" {
		t.Fatal("explicit tool graph", e)
	}
	var roundtrip Graph
	if json.Unmarshal(parsed.CanonicalJSON, &roundtrip) != nil {
		t.Fatal("canonical tool graph")
	}
	again, e := parse(roundtrip)
	if e != nil || again.SHA256 != parsed.SHA256 {
		t.Fatal("tool reload drift", e)
	}
	t.Run("incomplete_template_cannot_execute", func(t *testing.T) {
		g := validToolGraph()
		g.Nodes[1].Configuration = json.RawMessage(`{"plugin_version_id":""}`)
		p, e := parse(g)
		if e != nil || p.ValidateExecutable().Valid {
			t.Fatal(e)
		}
	})
	for _, test := range []struct {
		name   string
		change func(*Graph)
	}{
		{"answer_schema_cannot_enable_tool", func(g *Graph) { g.SchemaVersion = 1 }},
		{"unknown_schema", func(g *Graph) { g.SchemaVersion = 3 }},
		{"missing_start", func(g *Graph) { g.Nodes[0].Kind = AnswerNode }},
		{"missing_tool", func(g *Graph) { g.Nodes[1].Kind = RetrievalNode }},
		{"duplicate_kind", func(g *Graph) { g.Nodes[2].Kind = StartNode }},
		{"unknown_executor_argument", func(g *Graph) {
			g.Nodes[1].Configuration = json.RawMessage(`{"plugin_version_id":"","executor":"shell"}`)
		}},
		{"supplied_result", func(g *Graph) { g.Nodes[1].Configuration = json.RawMessage(`{"plugin_version_id":"","items":[]}`) }},
		{"url_argument", func(g *Graph) {
			g.Nodes[1].Configuration = json.RawMessage(`{"plugin_version_id":"https://example.invalid"}`)
		}},
		{"case_variant", func(g *Graph) { g.Nodes[1].Configuration = json.RawMessage(`{"Plugin_version_id":""}`) }},
		{"duplicate_field", func(g *Graph) {
			g.Nodes[1].Configuration = json.RawMessage(`{"plugin_version_id":"","plugin_version_id":""}`)
		}},
		{"null_configuration", func(g *Graph) { g.Nodes[1].Configuration = json.RawMessage(`null`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := validToolGraph()
			test.change(&g)
			if _, e := parse(g); e == nil {
				t.Fatal("unsafe graph accepted")
			}
		})
	}
	for _, test := range []struct {
		name   string
		change func(*Graph)
	}{
		{"reverse", func(g *Graph) { g.Edges[0].Source, g.Edges[0].Target = g.Edges[0].Target, g.Edges[0].Source }},
		{"disconnected", func(g *Graph) { g.Edges = g.Edges[:1] }},
		{"cycle", func(g *Graph) {
			g.Edges = append(g.Edges, Edge{ID: uuid.NewString(), Source: g.Nodes[2].ID, Target: g.Nodes[0].ID})
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := validToolGraph()
			test.change(&g)
			p, e := parse(g)
			if e != nil || p.ValidateExecutable().Valid {
				t.Fatal("unsafe topology", e)
			}
		})
	}
	raw := strings.Replace(string(parsed.CanonicalJSON), `"schema_version":2`, `"schema_version":2,"schema_version":1`, 1)
	if _, e := ParseGraph([]byte(raw), policy, 100); e == nil {
		t.Fatal("duplicate graph schema accepted")
	}
	answer, e := parse(validGraph(t))
	if e != nil || answer.IsTool() || answer.ResultKind() != "answer" || !answer.ValidateExecutable().Valid {
		t.Fatal("M13 answer contract regressed", e)
	}
}
