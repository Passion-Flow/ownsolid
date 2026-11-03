package scope

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestActualOpenedScopeBindingRejectsForeignMarkerAndReplacement(t *testing.T) {
	c := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	original, e := Create(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	bound := c
	bound.BoundScopeID = original.ScopeID
	if got, e := Read(ctx, bound); e != nil || got.ScopeID != original.ScopeID {
		t.Fatal("original scope refused", e)
	}
	bound.BoundScopeID = strings.Repeat("f", 32)
	if _, e = Read(ctx, bound); e == nil || e.Error() != "OFFLINE_SCOPE_BINDING_MISMATCH" {
		t.Fatal("foreign real marker accepted", e)
	}
	bound.BoundScopeID = "not-a-scope"
	if _, e = Read(ctx, bound); e == nil || e.Error() != "OFFLINE_SCOPE_BINDING_INVALID" {
		t.Fatal("invalid scope binding accepted", e)
	}
	// Actual same-path replacement with a newly created otherwise valid scope:
	// the original config bytes and fingerprint match, but the opened marker must
	// not be adopted under the original registered scope identity.
	if e = os.Rename(c.ScopeRoot, c.ScopeRoot+"-retained-original"); e != nil {
		t.Fatal(e)
	}
	replacement, e := Create(ctx, c)
	if e != nil || replacement.ScopeID == original.ScopeID {
		t.Fatal("replacement fixture", e)
	}
	bound.BoundScopeID = original.ScopeID
	if _, e = Read(ctx, bound); e == nil || e.Error() != "OFFLINE_SCOPE_BINDING_MISMATCH" {
		t.Fatal("replacement adopted as original scope", e)
	}
}
