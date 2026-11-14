package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
)

const MaxFenceReceiptBytes = 4096
const MaxJSONInteger uint64 = 9007199254740991

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[a-z0-9.-]+)?$`)

type FenceReceipt struct {
	SchemaVersion               uint32 `json:"schema_version"`
	GatewayID                   string `json:"gateway_id"`
	NodeID                      string `json:"node_id"`
	ControlOriginSHA256         string `json:"control_origin_sha256"`
	ChallengeID                 string `json:"challenge_id"`
	StateFenceSHA256            string `json:"state_fence_sha256"`
	FencedAtUnixMilliseconds    uint64 `json:"fenced_at_unix_milliseconds"`
	CheckedAtUnixMilliseconds   uint64 `json:"checked_at_unix_milliseconds"`
	ExclusiveStateLockConfirmed bool   `json:"exclusive_state_lock_confirmed"`
	PersistentFenceConfirmed    bool   `json:"persistent_fence_confirmed"`
	PendingOutcomes             uint64 `json:"pending_outcomes"`
	GatewayVersion              string `json:"gateway_version"`
}

func (r FenceReceipt) Validate() error {
	if r.SchemaVersion != 1 || !ValidID(r.GatewayID) || !ValidID(r.NodeID) || !ValidID(r.ChallengeID) || !digestPattern.MatchString(r.ControlOriginSHA256) || !digestPattern.MatchString(r.StateFenceSHA256) || r.FencedAtUnixMilliseconds == 0 || r.CheckedAtUnixMilliseconds < r.FencedAtUnixMilliseconds || r.CheckedAtUnixMilliseconds > MaxJSONInteger || !r.ExclusiveStateLockConfirmed || !r.PersistentFenceConfirmed || r.PendingOutcomes > 65536 || len(r.GatewayVersion) > 64 || !versionPattern.MatchString(r.GatewayVersion) {
		return invalid()
	}
	return nil
}
func (r FenceReceipt) Digest() (string, error) {
	if e := r.Validate(); e != nil {
		return "", e
	}
	b, e := json.Marshal(r)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}
func DecodeFenceReceipt(b []byte) (FenceReceipt, error) {
	var out FenceReceipt
	if len(b) > MaxFenceReceiptBytes {
		return out, invalid()
	}
	if e := decodeRequest(b, &out, []string{"schema_version", "gateway_id", "node_id", "control_origin_sha256", "challenge_id", "state_fence_sha256", "fenced_at_unix_milliseconds", "checked_at_unix_milliseconds", "exclusive_state_lock_confirmed", "persistent_fence_confirmed", "pending_outcomes", "gateway_version"}); e != nil {
		return out, e
	}
	return out, out.Validate()
}
