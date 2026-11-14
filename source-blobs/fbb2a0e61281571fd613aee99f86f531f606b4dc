package v1

import (
	"testing"
	"time"
)

func TestSessionFallbackDelay(t *testing.T) {
	for _, maximum := range []int{1, 7, 60, 900} {
		p := DefaultPacing()
		p.ReconnectMaxSeconds = maximum
		if got := SessionFallbackDelay(p, false); got != 300*time.Second {
			t.Fatalf("unknown path changed: %s", got)
		}
		want := time.Duration(maximum) * time.Second
		if want > 300*time.Second {
			want = 300 * time.Second
		}
		if got := SessionFallbackDelay(p, true); got != want {
			t.Fatalf("known path max %d: %s want %s", maximum, got, want)
		}
	}
	if got := SessionFallbackDelay(Pacing{}, true); got != 300*time.Second {
		t.Fatalf("invalid pacing changed safe fallback: %s", got)
	}
}
