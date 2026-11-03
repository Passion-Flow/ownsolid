// Package workflows owns bounded answer and signed builtin tool workflows.
package workflows

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const GraphSchemaVersion = 1
const ToolGraphSchemaVersion = 2
const ToolNodeCount = 3
const InitialNodeCount = 4
const MaximumDraftEdges = 8
const MaximumPositionMagnitude = 1000000
const (
	StartNode     = "start"
	RetrievalNode = "retrieval"
	AnswerNode    = "answer"
	EndNode       = "end"
	ToolNode      = "tool"
)

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Node struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	Position      Position        `json:"position"`
	Configuration json.RawMessage `json:"configuration"`
}
type Edge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
}
type Graph struct {
	SchemaVersion int    `json:"schema_version"`
	Nodes         []Node `json:"nodes"`
	Edges         []Edge `json:"edges"`
}
type RetrievalConfiguration struct {
	BaseID       string  `json:"base_id"`
	TopK         int     `json:"top_k"`
	MinimumScore float64 `json:"minimum_score"`
}
type AnswerConfiguration struct {
	AliasID      string `json:"alias_id"`
	Instructions string `json:"instructions"`
}
type ToolConfiguration struct {
	PluginVersionID string `json:"plugin_version_id"`
}

type GraphIssue struct {
	Code   string `json:"code"`
	NodeID string `json:"node_id,omitempty"`
	EdgeID string `json:"edge_id,omitempty"`
}
type Validation struct {
	Valid  bool         `json:"valid"`
	Issues []GraphIssue `json:"issues"`
}
type ParsedGraph struct {
	Graph         Graph
	Retrieval     RetrievalConfiguration
	Answer        AnswerConfiguration
	Tool          ToolConfiguration
	CanonicalJSON []byte
	SHA256        string
}

func graphFailure() error { return errors.New("WORKFLOW_GRAPH_INVALID") }
func exactConfiguration(raw []byte, out any, fields int) error {
	if len(raw) == 0 || wire.DecodeStrict(raw, out) != nil {
		return graphFailure()
	}
	var actual map[string]json.RawMessage
	if json.Unmarshal(raw, &actual) != nil || actual == nil || len(actual) != fields {
		return graphFailure()
	}
	for _, v := range actual {
		if string(v) == "null" {
			return graphFailure()
		}
	}
	return nil
}
func exactKeys(raw []byte, keys ...string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil || len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		value, ok := object[key]
		if !ok || string(value) == "null" {
			return false
		}
	}
	return true
}
func decodeGraph(raw []byte, g *Graph) error {
	// The shared strict scanner validates Unicode, every duplicate key and null.
	// A second typed decoder supports RawMessage node configurations, which are
	// then independently decoded against their exact node-kind schema.
	var generic any
	if wire.DecodeStrict(raw, &generic) != nil || !exactKeys(raw, "schema_version", "nodes", "edges") {
		return graphFailure()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(g) != nil {
		return graphFailure()
	}
	var root struct {
		Nodes []json.RawMessage `json:"nodes"`
		Edges []json.RawMessage `json:"edges"`
	}
	if json.Unmarshal(raw, &root) != nil {
		return graphFailure()
	}
	for _, node := range root.Nodes {
		if !exactKeys(node, "id", "kind", "position", "configuration") {
			return graphFailure()
		}
		var fields map[string]json.RawMessage
		json.Unmarshal(node, &fields)
		if !exactKeys(fields["position"], "x", "y") {
			return graphFailure()
		}
	}
	for _, edge := range root.Edges {
		if !exactKeys(edge, "id", "source", "target") {
			return graphFailure()
		}
	}
	return nil
}
func finitePosition(p Position) bool {
	return !math.IsNaN(p.X) && !math.IsNaN(p.Y) && !math.IsInf(p.X, 0) && !math.IsInf(p.Y, 0) && math.Abs(p.X) <= MaximumPositionMagnitude && math.Abs(p.Y) <= MaximumPositionMagnitude
}
func instructionText(s string, maximum int) bool {
	return utf8.ValidString(s) && len(s) <= maximum && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}

// ParseGraph accepts incomplete editable topology but never malformed structure,
// executable expressions, unknown fields or unbounded/private runtime objects.
func ParseGraph(raw []byte, p config.WorkflowPolicy, maximumTopK int) (ParsedGraph, error) {
	var out ParsedGraph
	if p.Validate() != nil || len(raw) > p.GraphBytes || len(raw) == 0 || decodeGraph(raw, &out.Graph) != nil {
		return out, graphFailure()
	}
	g := &out.Graph
	expectedNodes := InitialNodeCount
	if g.SchemaVersion == ToolGraphSchemaVersion {
		expectedNodes = ToolNodeCount
	}
	if (g.SchemaVersion != GraphSchemaVersion && g.SchemaVersion != ToolGraphSchemaVersion) || len(g.Nodes) != expectedNodes || g.Edges == nil || len(g.Edges) > MaximumDraftEdges {
		return out, graphFailure()
	}
	var required map[string]json.RawMessage
	if json.Unmarshal(raw, &required) != nil || len(required) != 3 {
		return out, graphFailure()
	}
	kinds := map[string]bool{}
	ids := map[string]bool{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if !wire.ValidID(n.ID) || ids[n.ID] || kinds[n.Kind] || !finitePosition(n.Position) {
			return out, graphFailure()
		}
		ids[n.ID] = true
		kinds[n.Kind] = true
		switch n.Kind {
		case StartNode, EndNode:
			var empty struct{}
			if exactConfiguration(n.Configuration, &empty, 0) != nil {
				return out, graphFailure()
			}
			n.Configuration = json.RawMessage(`{}`)
		case ToolNode:
			if g.SchemaVersion != ToolGraphSchemaVersion || exactConfiguration(n.Configuration, &out.Tool, 1) != nil || out.Tool.PluginVersionID != "" && !wire.ValidID(out.Tool.PluginVersionID) {
				return out, graphFailure()
			}
			n.Configuration, _ = json.Marshal(out.Tool)
		case RetrievalNode:
			if g.SchemaVersion != GraphSchemaVersion {
				return out, graphFailure()
			}
			if exactConfiguration(n.Configuration, &out.Retrieval, 3) != nil {
				return out, graphFailure()
			}
			r := out.Retrieval
			if r.BaseID != "" && !wire.ValidID(r.BaseID) || r.TopK < 1 || r.TopK > maximumTopK || math.IsNaN(r.MinimumScore) || math.IsInf(r.MinimumScore, 0) || r.MinimumScore < -1 || r.MinimumScore > 1 {
				return out, graphFailure()
			}
			n.Configuration, _ = json.Marshal(r)
		case AnswerNode:
			if g.SchemaVersion != GraphSchemaVersion {
				return out, graphFailure()
			}
			if exactConfiguration(n.Configuration, &out.Answer, 2) != nil {
				return out, graphFailure()
			}
			a := out.Answer
			if a.AliasID != "" && !wire.ValidID(a.AliasID) || !instructionText(a.Instructions, p.InstructionsBytes) {
				return out, graphFailure()
			}
			n.Configuration, _ = json.Marshal(a)
		default:
			return out, graphFailure()
		}
	}
	for _, kind := range []string{StartNode, EndNode} {
		if !kinds[kind] {
			return out, graphFailure()
		}
	}
	if g.SchemaVersion == ToolGraphSchemaVersion && !kinds[ToolNode] || g.SchemaVersion == GraphSchemaVersion && (!kinds[RetrievalNode] || !kinds[AnswerNode]) {
		return out, graphFailure()
	}
	edges := map[string]bool{}
	pairs := map[string]bool{}
	for _, e := range g.Edges {
		pair := e.Source + ":" + e.Target
		if !wire.ValidID(e.ID) || edges[e.ID] || !ids[e.Source] || !ids[e.Target] || e.Source == e.Target || pairs[pair] {
			return out, graphFailure()
		}
		edges[e.ID] = true
		pairs[pair] = true
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool { return g.Edges[i].ID < g.Edges[j].ID })
	out.CanonicalJSON, _ = json.Marshal(g)
	digest := sha256.Sum256(out.CanonicalJSON)
	out.SHA256 = hex.EncodeToString(digest[:])
	return out, nil
}

// ValidateExecutable validates topology and inputs before any external work.
// Resource/session authorization is a separate current server transaction.
func (p ParsedGraph) ValidateExecutable() Validation {
	v := Validation{Issues: []GraphIssue{}}
	add := func(code, node, edge string) {
		v.Issues = append(v.Issues, GraphIssue{Code: code, NodeID: node, EdgeID: edge})
	}
	nodes := map[string]Node{}
	byKind := map[string]string{}
	outgoing := map[string][]Edge{}
	for _, n := range p.Graph.Nodes {
		nodes[n.ID] = n
		byKind[n.Kind] = n.ID
	}
	for _, e := range p.Graph.Edges {
		outgoing[e.Source] = append(outgoing[e.Source], e)
	}
	state := map[string]int{}
	var visit func(string)
	visit = func(id string) {
		if state[id] == 2 {
			return
		}
		state[id] = 1
		for _, e := range outgoing[id] {
			if state[e.Target] == 1 {
				add("WORKFLOW_CYCLE", id, e.ID)
			} else if state[e.Target] == 0 {
				visit(e.Target)
			}
		}
		state[id] = 2
	}
	for _, n := range p.Graph.Nodes {
		if state[n.ID] == 0 {
			visit(n.ID)
		}
	}
	expected := map[string]string{StartNode: RetrievalNode, RetrievalNode: AnswerNode, AnswerNode: EndNode}
	expectedNodes := InitialNodeCount
	if p.Graph.SchemaVersion == ToolGraphSchemaVersion {
		expected = map[string]string{StartNode: ToolNode, ToolNode: EndNode}
		expectedNodes = ToolNodeCount
	}
	if len(p.Graph.Edges) != expectedNodes-1 {
		add("WORKFLOW_CONNECTIONS_REQUIRED", "", "")
	}
	for _, n := range p.Graph.Nodes {
		es := outgoing[n.ID]
		if n.Kind == EndNode {
			if len(es) != 0 {
				add("WORKFLOW_END_HAS_OUTPUT", n.ID, "")
			}
			continue
		}
		if len(es) != 1 {
			add("WORKFLOW_CONNECTIONS_REQUIRED", n.ID, "")
			continue
		}
		if nodes[es[0].Target].Kind != expected[n.Kind] {
			add("WORKFLOW_CONNECTION_ORDER_INVALID", n.ID, es[0].ID)
		}
	}
	if p.Graph.SchemaVersion == ToolGraphSchemaVersion {
		if p.Tool.PluginVersionID == "" {
			add("WORKFLOW_INPUT_REQUIRED", byKind[ToolNode], "")
		}
	} else {
		if p.Retrieval.BaseID == "" {
			add("WORKFLOW_INPUT_REQUIRED", byKind[RetrievalNode], "")
		}
		if p.Answer.AliasID == "" || strings.TrimSpace(p.Answer.Instructions) == "" {
			add("WORKFLOW_INPUT_REQUIRED", byKind[AnswerNode], "")
		}
	}
	v.Valid = len(v.Issues) == 0
	return v
}

func (p ParsedGraph) IsTool() bool { return p.Graph.SchemaVersion == ToolGraphSchemaVersion }
func (p ParsedGraph) ResultKind() string {
	if p.IsTool() {
		return "model_list"
	}
	return "answer"
}
