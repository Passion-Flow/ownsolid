package http

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http/httptest"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"strings"
	"testing"
)

func TestWorkflowRequestDecoderPreservesStrictGraphAndRequiredFalseValues(t *testing.T) {
	var input workflows.UpdateRequest
	graph := workflows.Graph{SchemaVersion: 1, Nodes: []workflows.Node{}, Edges: []workflows.Edge{}}
	for _, kind := range []string{"start", "retrieval", "answer", "end"} {
		configuration := json.RawMessage(`{}`)
		if kind == "retrieval" {
			configuration = json.RawMessage(`{"base_id":"","top_k":5,"minimum_score":0.2}`)
		}
		if kind == "answer" {
			configuration = json.RawMessage(`{"alias_id":"","instructions":""}`)
		}
		graph.Nodes = append(graph.Nodes, workflows.Node{ID: uuid.NewString(), Kind: kind, Configuration: configuration})
	}
	raw, _ := json.Marshal(workflows.UpdateRequest{Name: "Caf\u00e9 \u5de5\u4f5c\u6d41", Graph: graph, Enabled: false, ExpectedRevision: 1, Reason: "Explicit synthetic mutation"})
	request := httptest.NewRequest("PATCH", "/", strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	if e := decode(request, &input, 1048576); e != nil {
		t.Fatal(e)
	}
	if input.Enabled || input.Name != "Caf\u00e9 \u5de5\u4f5c\u6d41" || len(input.Graph.Nodes) != 4 {
		t.Fatal("explicit false or graph lost")
	}
	for _, body := range []string{strings.Replace(string(raw), `"enabled":false,`, "", 1), strings.Replace(string(raw), `"enabled":false`, `"enabled":null`, 1), strings.Replace(string(raw), `"enabled":false`, `"enabled":false,"enabled":true`, 1), strings.Replace(string(raw), `"name":`, `"Name":`, 1), strings.Replace(string(raw), `"expected_revision":1`, `"expected_revision":"1"`, 1), string(raw) + "{}", strings.Replace(string(raw), `"reason":`, `"unknown":`, 1)} {
		request := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if e := decode(request, &input, 1048576); e == nil {
			t.Fatal("malformed request accepted", body)
		}
	}
	request = httptest.NewRequest("PATCH", "/", strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "application/json")
	if e := decode(request, &input, 4); e == nil || e.Error() != "PAYLOAD_TOO_LARGE" {
		t.Fatal("oversized body accepted", e)
	}
	request = httptest.NewRequest("PATCH", "/", strings.NewReader(string(raw)))
	request.Header.Set("Content-Type", "text/plain")
	if e := decode(request, &input, 1048576); e == nil || e.Error() != "UNSUPPORTED_MEDIA_TYPE" {
		t.Fatal("unsupported media accepted", e)
	}
}
