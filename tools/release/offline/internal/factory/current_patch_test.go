package factory

import (
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestPatchBaselineMustBeOneSignedDigest(t *testing.T) {
	digest := wire.Digest([]byte("installed manifest"))
	m := currentInventory()
	m.Prerequisites = append(m.Prerequisites, patchBaselinePrefix+digest)
	if got, err := patchBaseline(m); err != nil || got != digest {
		t.Fatalf("baseline: %s, %v", got, err)
	}
	m.Prerequisites = append(m.Prerequisites, patchBaselinePrefix+digest)
	if _, err := patchBaseline(m); ErrorCode(err) != "RELEASE_PATCH_BASELINE_INVALID" {
		t.Fatalf("accepted an ambiguous signed baseline: %v", err)
	}
	m.Prerequisites = []string{patchBaselinePrefix + "latest"}
	if _, err := patchBaseline(m); ErrorCode(err) != "RELEASE_PATCH_BASELINE_INVALID" {
		t.Fatalf("accepted a mutable baseline: %v", err)
	}
}
