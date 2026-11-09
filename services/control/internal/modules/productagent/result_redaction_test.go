package productagent

import (
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/google/uuid"
)

func TestSafeDisplayName(t *testing.T) {
	modelPrefix := "pk1." + uuid.NewString()
	modelKey := modelPrefix + "." + security.Token()
	gatewayKey := agentwire.GatewayCredentialPrefix + "." + uuid.NewString() + "." + security.Token()
	for _, tt := range []struct {
		name, input, want string
	}{
		{"public prefix", "Use " + modelPrefix, "Use " + modelPrefix},
		{"two credentials", "Keys (" + modelKey + ") and " + gatewayKey + ".", "Keys ([redacted]) and [redacted]."},
		{"ordinary multilingual label", "Report \u4e16\u754c", "Report \u4e16\u754c"},
		{"overrides and isolates", "Report \u202eabc\u202c \u2067xyz\u2069", "Report abc xyz"},
		{"directional marks", "Report\u061c\u200e\u200f", "Report"},
		{"ordinary right-to-left letters and joiner", "Report \u0646\u200c\u0627\u0645", "Report \u0646\u200c\u0627\u0645"},
		{"split credential", modelPrefix + ".\u2066" + security.Token() + "\u2069", "[redacted]"},
		{"command-like text is data", "open nodes; https://example.invalid", "open nodes; https://example.invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeDisplayName(tt.input); got != tt.want {
				t.Fatal("display credential boundary changed")
			}
		})
	}
}
