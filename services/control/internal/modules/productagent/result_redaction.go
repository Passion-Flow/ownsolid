package productagent

import (
	"regexp"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Match the bearer formats in the model-gateway and distribution contracts.
// A public key prefix alone can still be used as a display label.
var displayCredential = regexp.MustCompile(`(?:pk1|` + regexp.QuoteMeta(agentwire.GatewayCredentialPrefix) +
	`)\.[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}\.[A-Za-z0-9_-]{43}`)

const maskedCredential = "[redacted]"

func maskDisplayCredentials(label string) string {
	return displayCredential.ReplaceAllString(label, maskedCredential)
}
