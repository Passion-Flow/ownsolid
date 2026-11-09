package identity

import (
	"testing"
	"time"
)

func TestAdminTOTPRFC6238SHA1Vectors(t *testing.T) {
	// Six-digit truncation of the RFC's public eight-digit SHA-1 examples.
	seed := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		now := time.Unix(v.unix, 0)
		step, ok := adminTOTPStep(seed, v.code, now, -1)
		if !ok || step != v.unix/30 {
			t.Fatalf("RFC vector at %d failed", v.unix)
		}
		if _, ok := adminTOTPStep(seed, v.code, now, step); ok {
			t.Fatalf("RFC vector at %d was replayable", v.unix)
		}
	}
}
