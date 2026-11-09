package productagent

import (
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/google/uuid"
)

func TestMaskDisplayCredentials(t *testing.T) {
	modelPrefix := "pk1." + uuid.NewString()
	modelKey := modelPrefix + "." + security.Token()
	gatewayKey := agentwire.GatewayCredentialPrefix + "." + uuid.NewString() + "." + security.Token()
	for _, tt := range []struct {
		name, input, want string
	}{
		{"public prefix", "Use " + modelPrefix, "Use " + modelPrefix},
		{"two credentials", "Keys (" + modelKey + ") and " + gatewayKey + ".", "Keys ([redacted]) and [redacted]."},
		{"ordinary multilingual label", "Report \u4e16\u754c", "Report \u4e16\u754c"},
		{"command-like text is data", "open nodes; https://example.invalid", "open nodes; https://example.invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskDisplayCredentials(tt.input); got != tt.want {
				t.Fatal("display credential boundary changed")
			}
		})
	}
}
