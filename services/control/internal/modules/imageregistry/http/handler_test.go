package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
)

const pullPath = "/agent/v1/nodes/11111111-2222-4333-8444-555555555555/tasks/22222222-2222-4333-8444-555555555555/image-registry/manifest"

func pull() *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://admin.example.invalid"+pullPath, strings.NewReader(`{"lease_token":"x"}`))
	r.Header.Set("Authorization", "Bearer "+security.Token())
	return r
}

// Image pulls authenticate on their own path, so they apply the certificate rule of every
// other agent call before anything else (ADR 0050).
func TestImagePullsAnswerToTheCertificateRule(t *testing.T) {
	var asked string
	refuse := func(_ context.Context, node string, peer agentidentity.Peer) error {
		asked = node
		if !peer.Presented {
			return errors.New("AGENT_CERTIFICATE_REQUIRED")
		}
		return nil
	}
	h := &Handler{Service: &imageregistry.Service{}, CheckPeer: refuse}
	if _, _, e := h.Machine(pull()); e == nil || e.Error() != "AGENT_CERTIFICATE_REQUIRED" {
		t.Fatalf("a pull without a certificate: %v", e)
	}
	if asked != "11111111-2222-4333-8444-555555555555" {
		t.Fatalf("checked node %q", asked)
	}
	unchecked := &Handler{Service: &imageregistry.Service{}}
	if _, _, e := unchecked.Machine(pull()); e == nil || e.Error() != "RESOURCE_NOT_FOUND" {
		t.Fatalf("a handler without the check must serve nothing: %v", e)
	}
}
