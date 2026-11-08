package http_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/google/uuid"
)

func TestLaneDManagedGatewayTLS(t *testing.T) {
	var lose, created atomic.Bool
	c := newWorkflowCLI(t, nil, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/gateways") && lose.CompareAndSwap(true, false) {
				recorder := httptest.NewRecorder()
				next.ServeHTTP(recorder, r)
				created.Store(recorder.Code == http.StatusCreated)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
	// The registration branch checks that the installation enables agent
	// delivery. The independent GW-114 fixture exercises actual CA signing.
	c.service.Config.ModelGatewayIdentity = &config.ModelGatewayIdentityConfiguration{
		CACertificateFile: "/run/test-gateway-ca.pem", CAPrivateKeyFile: "/run/test-gateway-ca-key.pem",
	}
	node := enrollWorkflowNode(c, "Managed gateway fixture", wire.DockerInferenceProfile)
	key := uuid.NewString()
	args := []string{"deployment", "alias", "provision", node.NodeID, "--name", "Fixture gateway",
		"--idempotency-key", key, "--reason", "Queue fixture gateway", "--output", "json"}

	foreign := append(append([]string{}, args...), "--workspace", uuid.NewString())
	c.refused("RESOURCE_NOT_FOUND", foreign...)
	c.count(0, "SELECT count(*) FROM gateway_instances")
	lose.Store(true)
	c.refused("retry with the same --idempotency-key "+key, args...)
	if !created.Load() {
		t.Fatal("the registration did not commit before losing its response")
	}

	raw := c.ok(`"delivery_state": "starting"`, args...)
	var gateway distribution.Gateway
	if err := json.Unmarshal([]byte(raw), &gateway); err != nil || gateway.ID == "" ||
		gateway.NodeID != node.NodeID || gateway.Delivery != "agent" || gateway.ClientOrigin != nil {
		t.Fatalf("invalid managed gateway record: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields["secret"] != nil || fields["secret_available"] != nil {
		t.Fatal("registration exposed its credential envelope")
	}
	c.ok(gateway.ID, "deployment", "alias", "gateways", "--output", "json")
	c.count(1, "SELECT count(*) FROM gateway_instances WHERE idempotency_key=$1 AND delivery_task_id IS NOT NULL", key)
	c.count(1, "SELECT count(*) FROM audit_events WHERE action='gateway.instance_created' AND target_id=$1", gateway.ID)
}
