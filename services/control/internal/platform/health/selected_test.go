package health

import (
	"context"
	"testing"
)

func TestMissingSelectedReadinessNeverPasses(t *testing.T) {
	ctx := context.Background()
	if v, status, e := CheckSelected(ctx, nil, nil, "GET", "/health/live"); e != nil || status != 200 || v == nil {
		t.Fatal("liveness unavailable", e)
	}
	if v, status, e := CheckSelected(ctx, nil, nil, "GET", "/health/ready"); e == nil || status != 0 || v != nil {
		t.Fatal("missing selected readiness accepted")
	}
	if v, status, e := CheckSelected(ctx, nil, nil, "POST", "/health/live"); e == nil || status != 0 || v != nil {
		t.Fatal("unsupported method accepted")
	}
}
