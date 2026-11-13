package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestWorkspaceOperationReadPreservesOutcomeAndGlobalRoute(t *testing.T) {
	for _, state := range []string{"committed", "unconfirmed"} {
		t.Run(state, func(t *testing.T) {
			var event any
			if state == "committed" {
				event = map[string]string{"action": "workspace.member_removed", "workspace_id": workspaceID,
					"target_type": "workspace_membership", "target_id": secondNodeID}
			}
			requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != workspaceReadPath+"/operations/"+nodeID ||
					r.URL.RawQuery != "" || r.ContentLength != 0 || r.Header.Get("X-CSRF-Token") != "" {
					t.Error("receipt read acquired context, filters or mutation semantics")
				}
				w.Header().Set("Cache-Control", "no-store")
				respond(w, map[string]any{"request_id": nodeID, "state": state, "event": event})
			})
			for _, format := range []string{"json", "yaml", "table"} {
				out, stderr, err := run(t, "workspace", "operation", "get", nodeID, "-o", format)
				if err != nil || stderr != "" || !strings.Contains(out, nodeID) || !strings.Contains(out, state) {
					t.Fatalf("receipt %s: %q / %q (%v)", format, out, stderr, err)
				}
				if format != "table" {
					var value map[string]any
					if format == "json" {
						err = json.Unmarshal([]byte(out), &value)
					} else {
						err = yaml.Unmarshal([]byte(out), &value)
					}
					actual, exists := value["event"]
					if err != nil || !exists || (actual == nil) != (state == "unconfirmed") {
						t.Fatalf("receipt lost explicit event/null: %q (%v)", out, err)
					}
					if state == "committed" && !strings.Contains(out, secondNodeID) {
						t.Fatal("structured receipt lost target identity")
					}
				} else if state == "committed" && (!strings.Contains(out, "workspace.member_removed") || !strings.Contains(out, workspaceID)) {
					t.Fatal("table lost committed action or workspace")
				}
			}
			if requests.Load() != 3 {
				t.Fatal("receipt read retried, cached or acquired extra context")
			}
		})
	}
}

func TestWorkspaceOperationArgumentsFailBeforeIO(t *testing.T) {
	requests := authenticatedCLIServer(t, func(http.ResponseWriter, *http.Request) { t.Error("bad receipt input reached HTTP") })
	for _, args := range [][]string{
		{}, {"research"}, {"00000000-0000-0000-0000-000000000000"}, {"ABCDEF01-0000-4000-8000-000000000001"},
		{nodeID, "extra"}, {nodeID, "--workspace", workspaceID}, {nodeID, "--limit", "1"}, {"../" + nodeID},
	} {
		out, _, err := run(t, append([]string{"workspace", "operation", "get"}, args...)...)
		if err == nil || out != "" {
			t.Errorf("bad request selector produced %q (%v)", out, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("invalid request selector caused I/O")
	}
}

func TestWorkspaceOperationMalformedReceiptHasNoOutput(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"request_id":"` + secondNodeID + `","state":"unconfirmed","event":null}`,
		`{"request_id":"` + nodeID + `","state":"rolled_back","event":null}`,
		`{"request_id":"` + nodeID + `","state":"unconfirmed"}`,
		`{"request_id":"` + nodeID + `","state":"unconfirmed","event":{}}`,
		`{"request_id":"` + nodeID + `","state":"committed","event":null}`,
		`{"request_id":"` + nodeID + `","state":"committed","event":[]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			authenticatedCLIServer(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"data":%s}`, raw)
			})
			for _, format := range []string{"table", "json", "yaml"} {
				out, _, err := run(t, "workspace", "operation", "get", nodeID, "-o", format)
				if err == nil || out != "" {
					t.Fatalf("malformed receipt printed %q (%v)", out, err)
				}
			}
		})
	}
}

func TestWorkspaceOperationRejectsInconsistentCommitEvent(t *testing.T) {
	for _, event := range []map[string]string{
		{"action": "workspace.created", "workspace_id": workspaceID, "target_type": "workspace", "target_id": nodeID},
		{"action": "workspace.unknown", "workspace_id": workspaceID, "target_type": "workspace", "target_id": workspaceID},
		{"action": "workspace.member_removed", "workspace_id": workspaceID, "target_type": "workspace_note", "target_id": nodeID},
		{"action": "workspace.note_deleted", "workspace_id": "invalid", "target_type": "workspace_note", "target_id": nodeID},
		{"action": "workspace.note_deleted", "workspace_id": workspaceID, "target_type": "workspace_note", "target_id": "00000000-0000-0000-0000-000000000000"},
	} {
		t.Run(fmt.Sprint(event), func(t *testing.T) {
			authenticatedCLIServer(t, func(w http.ResponseWriter, _ *http.Request) {
				respond(w, map[string]any{"request_id": nodeID, "state": "committed", "event": event})
			})
			out, _, err := run(t, "workspace", "operation", "get", nodeID)
			if err == nil || out != "" {
				t.Fatalf("inconsistent committed receipt printed %q (%v)", out, err)
			}
		})
	}
}

func TestWorkspaceOperationFailureRequiresExplicitReadRetry(t *testing.T) {
	for _, code := range []struct {
		status int
		name   string
	}{{401, "AUTH_SESSION_EXPIRED"}, {403, "ACCESS_DENIED"}, {400, "VALIDATION_FAILED"}, {409, "STORAGE_COMMIT_OUTCOME_UNKNOWN"}, {503, "STORAGE_UNAVAILABLE"}} {
		t.Run(code.name, func(t *testing.T) {
			var attempts atomic.Int64
			requests := authenticatedCLIServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != workspaceReadPath+"/operations/"+nodeID {
					t.Error("receipt retry changed intent")
				}
				if attempts.Add(1) == 1 {
					w.Header().Set("Content-Type", "application/problem+json")
					w.WriteHeader(code.status)
					_, _ = fmt.Fprintf(w, `{"type":"about:blank","title":"Receipt unavailable","status":%d,"code":%q,"request_id":"12345678-0000-4000-8000-000000000009"}`, code.status, code.name)
					return
				}
				respond(w, map[string]any{"request_id": nodeID, "state": "unconfirmed", "event": nil})
			})
			out, _, err := run(t, "workspace", "operation", "get", nodeID, "-o", "json")
			if err == nil || out != "" || !strings.Contains(err.Error(), code.name) || requests.Load() != 1 {
				t.Fatalf("receipt refusal hidden or retried: %q (%v)", out, err)
			}
			out, _, err = run(t, "workspace", "operation", "get", nodeID, "-o", "json")
			if err != nil || !strings.Contains(out, "unconfirmed") || requests.Load() != 2 {
				t.Fatalf("explicit receipt read retry failed: %q (%v)", out, err)
			}
		})
	}
}
