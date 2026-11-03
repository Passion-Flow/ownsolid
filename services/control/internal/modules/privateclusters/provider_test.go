package privateclusters

import (
	"encoding/json"
	"strings"
	"testing"

	proof "github.com/Passion-Flow/ownsolid/services/control/internal/platform/privateclusterproof"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Explicit provider-shape doubles are not image, cluster or execution evidence.
func syntheticClient() *client {
	return &client{profile: syntheticProfile(), policy: Defaults().Policy, approval: bundle.ImageProofResult{ManifestSHA256: strings.Repeat("d", 64), Asset: release.Asset{ImageID: "sha256:" + strings.Repeat("a", 64), ImageConfigDigest: "sha256:" + strings.Repeat("b", 64)}}}
}
func syntheticRun(t *testing.T, c *client) providerRun {
	t.Helper()
	id := "66666666-6666-4666-8666-666666666666"
	r, e := c.prepared(id, proof.Challenge{FormatVersion: 1, RunID: id, NamespaceUID: c.profile.NamespaceUID, InstallationID: c.profile.InstallationID, Nonce: strings.Repeat("c", 64), Architecture: c.profile.Architecture(), RunAsUser: c.policy.RunAsUser})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestClosedJobRejectsAdmissionExpansion(t *testing.T) {
	c := syntheticClient()
	r := syntheticRun(t, c)
	body, e := c.jobBody(r)
	if e != nil {
		t.Fatal(e)
	}
	expected := body["spec"].(map[string]any)
	if !sameSpec(normalizedJSON(expected).(map[string]any), expected, false) {
		t.Fatal("own fixed Job refused")
	}
	for _, mutate := range []func(map[string]any){
		func(s map[string]any) { s["ttlSecondsAfterFinished"] = float64(1) },
		func(s map[string]any) { s["backoffLimit"] = float64(1) },
		func(s map[string]any) {
			p := s["template"].(map[string]any)["spec"].(map[string]any)
			p["volumes"] = []any{map[string]any{"name": "secret", "secret": map[string]any{"secretName": "ambient"}}}
		},
		func(s map[string]any) {
			p := s["template"].(map[string]any)["spec"].(map[string]any)
			p["automountServiceAccountToken"] = true
		},
		func(s map[string]any) {
			p := s["template"].(map[string]any)["spec"].(map[string]any)
			p["containers"].([]any)[0].(map[string]any)["image"] = "unapproved:latest"
		},
		func(s map[string]any) {
			p := s["template"].(map[string]any)["spec"].(map[string]any)
			p["containers"].([]any)[0].(map[string]any)["command"] = []any{"sh", "-c", "arbitrary"}
		},
		func(s map[string]any) {
			p := s["template"].(map[string]any)["spec"].(map[string]any)
			p["initContainers"] = []any{map[string]any{"image": "unapproved"}}
		},
	} {
		actual := normalizedJSON(expected).(map[string]any)
		mutate(actual)
		if sameSpec(actual, expected, false) {
			t.Fatal("expanded execution accepted")
		}
	}
	// A new policy cannot silently reinterpret an immutable old spec.
	c.policy.MemoryMiB++
	if _, e = c.jobBody(r); e == nil {
		t.Fatal("changed immutable policy accepted")
	}
}
func TestNonExpiringLeaseRequiresExactOriginalIdentity(t *testing.T) {
	c := syntheticClient()
	r := syntheticRun(t, c)
	raw, _ := json.Marshal(c.leaseBody(r))
	var v leaseObject
	if e := json.Unmarshal(raw, &v); e != nil {
		t.Fatal(e)
	}
	v.Metadata.UID = "77777777-7777-4777-8777-777777777777"
	v.Metadata.ResourceVersion = "12"
	if _, e := c.checkLease(v, r); e != nil {
		t.Fatal(e)
	}
	n := int64(1)
	v.Spec.LeaseDurationSeconds = &n
	if _, e := c.checkLease(v, r); e == nil {
		t.Fatal("expiring/stealable lease accepted")
	}
	v.Spec.LeaseDurationSeconds = nil
	r.LeaseUID = "88888888-8888-4888-8888-888888888888"
	if _, e := c.checkLease(v, r); e == nil {
		t.Fatal("replacement lease adopted")
	}
}

func TestDeletionMarkerIsOnlyAnObservedWaitState(t *testing.T) {
	stamp := "2026-10-22T01:00:00Z"
	m := metadata{DeletionTimestamp: &stamp}
	waiting, e := deletionMarker(&m)
	if e != nil || !waiting || m.DeletionTimestamp != nil {
		t.Fatal("exact deletion observation")
	}
	bad := "not-time"
	if _, e = deletionMarker(&metadata{DeletionTimestamp: &bad}); e == nil {
		t.Fatal("malformed deletion observation accepted")
	}
}
