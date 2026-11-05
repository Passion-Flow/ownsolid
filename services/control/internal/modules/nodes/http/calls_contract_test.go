package http

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
)

// TestEveryAgentOperationDeclaresItsCall keeps the HTTP contracts and the protocol's call
// table in step: every agent operation names the call admission classifies it as, and
// documents the retry hint of a refusal (ADR 0017).
func TestEveryAgentOperationDeclaresItsCall(t *testing.T) {
	const node, task, upload = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222", "33333333-3333-4333-8333-333333333333"
	replacer := strings.NewReplacer("{site_id}", "default", "{cell_id}", "default", "{node_id}", node, "{task_id}", task, "{upload_id}", upload)
	seen := map[wire.Call]bool{}
	for _, spec := range []string{"nodes", "components"} {
		raw, e := os.ReadFile("../../../../../../api/openapi/v1/" + spec + "/openapi.json")
		if e != nil {
			t.Fatal(e)
		}
		var document struct {
			Paths map[string]map[string]json.RawMessage `json:"paths"`
		}
		if e = json.Unmarshal(raw, &document); e != nil {
			t.Fatal(e)
		}
		for path, operations := range document.Paths {
			if !strings.HasPrefix(path, "/agent/v1/") {
				continue
			}
			for method, body := range operations {
				var operation struct {
					Call      wire.Call `json:"x-agent-call"`
					Responses map[string]struct {
						Headers map[string]json.RawMessage `json:"headers"`
					} `json:"responses"`
				}
				if method == "parameters" || json.Unmarshal(body, &operation) != nil {
					continue
				}
				call, named, ok := wire.PathCall(cellroute.Relative(replacer.Replace(path)))
				if !ok || call != operation.Call || call != wire.CallEnroll && named != node {
					t.Errorf("%s %s %s: declares %q, admission sees %q (%v)", spec, method, path, operation.Call, call, ok)
				}
				if _, ok := operation.Responses["default"].Headers["Retry-After"]; !ok {
					t.Errorf("%s %s %s: refusal has no Retry-After", spec, method, path)
				}
				seen[call] = true
			}
		}
	}
	for _, call := range wire.Calls() {
		if !seen[call] {
			t.Errorf("no HTTPS operation is a %s call", call)
		}
	}
}
