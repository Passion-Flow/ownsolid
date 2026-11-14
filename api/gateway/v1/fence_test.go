package v1

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFenceReceiptRequiresScopedActualLockAndPersistentFenceProperties(t *testing.T) {
	receipt := FenceReceipt{SchemaVersion: 1, GatewayID: "00000000-0000-0000-0000-000000000001", NodeID: "00000000-0000-0000-0000-000000000002", ControlOriginSHA256: strings.Repeat("a", 64), ChallengeID: "00000000-0000-0000-0000-000000000003", StateFenceSHA256: strings.Repeat("b", 64), FencedAtUnixMilliseconds: 1000, CheckedAtUnixMilliseconds: 1001, ExclusiveStateLockConfirmed: true, PersistentFenceConfirmed: true, PendingOutcomes: 1, GatewayVersion: "0.7.0"}
	b, _ := json.Marshal(receipt)
	decoded, e := DecodeFenceReceipt(b)
	if e != nil {
		t.Fatal(e)
	}
	first, _ := decoded.Digest()
	decoded.ChallengeID = "00000000-0000-0000-0000-000000000004"
	second, _ := decoded.Digest()
	if first == second {
		t.Fatal("challenge not bound")
	}
	for _, bad := range []string{strings.Replace(string(b), `"persistent_fence_confirmed":true`, `"persistent_fence_confirmed":false`, 1), strings.Replace(string(b), `"pending_outcomes":1`, `"pending_outcomes":null`, 1), strings.Replace(string(b), `"gateway_id"`, `"GATEWAY_ID"`, 1), strings.Replace(string(b), `"gateway_version":"0.7.0"`, `"gateway_version":"0.7.0","prompt":"must not persist"`, 1)} {
		if _, e := DecodeFenceReceipt([]byte(bad)); e == nil {
			t.Fatal("invalid fence evidence accepted")
		}
	}
}
