package distribution_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestGatewayProvisionAcceptsOnlyExplicitInferenceCapableEnrollmentProfiles(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	for _, profile := range []string{wire.ReadOnlyProfile, wire.SystemdComponentsProfile, wire.SystemdMaintenanceProfile, wire.DockerInferenceProfile, wire.DockerAIProfile, wire.DockerModelLifecycleProfile} {
		t.Run(profile, func(t *testing.T) {
			join, e := c.s.Nodes.MintForProfile(ctx, c.a, c.a.Me.Workspace.ID, "Explicit gateway profile "+profile, profile, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			handshake := c.handshake
			handshake.ProtocolVersion = wire.ProfileVersion(profile)
			handshake.ExecutionProfile = profile
			handshake.Capabilities = wire.Capabilities(profile)
			handshake.Facts.ObservedAt = time.Now().UTC()
			enrolled, e := c.s.Nodes.Enroll(ctx, wire.Enrollment{JoinToken: join.Token, Handshake: handshake}, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			input := c.input()
			input.NodeID = enrolled.NodeID
			_, e = c.s.Provision(ctx, c.a, input, uuid.NewString(), uuid.NewString())
			if wire.IsInferenceProfile(profile) {
				if e != nil {
					t.Fatal("explicit inference-capable profile refused", e)
				}
			} else if e == nil || e.Error() != "GATEWAY_NODE_UNAVAILABLE" {
				t.Fatal("non-inference profile accepted", e)
			}
		})
	}
}
