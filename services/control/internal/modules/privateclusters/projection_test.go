package privateclusters

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrdinaryRunExcludesInternalRemoteAndHistoricalAuthority(t *testing.T) {
	remote := &remoteState{Lease: remoteRef{UID: "11111111-1111-4111-8111-111111111111", ResourceVersion: "synthetic-private-resource-version"}, Logs: "synthetic-private-retained-proof-log"}
	value := Run{Remote: remote, actor: "synthetic-historical-actor", session: "synthetic-historical-session", tokenHash: []byte("synthetic-historical-token")}
	raw, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 11 {
		t.Fatal("ordinary Run shape drift", string(raw))
	}
	for _, name := range []string{"id", "workspace_id", "connection_id", "phase", "result", "revision", "lease_uid", "job_uid", "pod_uid", "created_at", "updated_at"} {
		if _, ok := fields[name]; !ok {
			t.Fatal("required public field missing", name)
		}
	}
	for _, private := range []string{"remote", "resource_version", "synthetic-private", "synthetic-historical"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("internal state escaped ordinary projection", private)
		}
	}
	stored, e := json.Marshal(remote)
	if e != nil || !strings.Contains(string(stored), remote.Logs) || !strings.Contains(string(stored), remote.Lease.ResourceVersion) {
		t.Fatal("internal recovery serialization changed", e)
	}
}
