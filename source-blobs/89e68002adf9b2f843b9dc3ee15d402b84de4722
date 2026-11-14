package distribution_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

// No database, configuration double or fabricated row is present. A nil owner
// makes any attempt to select machine configuration before admission observable.
func TestGatewayMachineOriginSelectionPreservesAdmissionOrder(t *testing.T) {
	admin := identity.Auth{Me: identity.Me{Audience: "admin", User: identity.User{ID: uuid.NewString(), IsPlatformAdmin: true}}}
	valid := distribution.ProvisionRequest{WorkspaceID: uuid.NewString(), NodeID: uuid.NewString(), Name: "Synthetic admission order", ClientOrigin: "https://gateway.example.invalid", Reason: "Synthetic configuration boundary"}
	for _, test := range []struct {
		name string
		auth identity.Auth
		edit func(*distribution.ProvisionRequest, *string)
		code string
	}{
		{"anonymous", identity.Auth{}, nil, "ACCESS_DENIED"},
		{"portal-admin", identity.Auth{Me: identity.Me{Audience: "portal", User: admin.Me.User}}, nil, "ACCESS_DENIED"},
		{"ordinary-admin-audience", identity.Auth{Me: identity.Me{Audience: "admin", User: identity.User{ID: uuid.NewString()}}}, nil, "ACCESS_DENIED"},
		{"invalid-node", admin, func(in *distribution.ProvisionRequest, _ *string) { in.NodeID = "invalid" }, "VALIDATION_FAILED"},
		{"invalid-intent", admin, func(_ *distribution.ProvisionRequest, key *string) { *key = "invalid" }, "VALIDATION_FAILED"},
		{"invalid-client-origin", admin, func(in *distribution.ProvisionRequest, _ *string) { in.ClientOrigin = "http://gateway.example.invalid" }, "VALIDATION_FAILED"},
		{"missing-reason", admin, func(in *distribution.ProvisionRequest, _ *string) { in.Reason = "" }, "VALIDATION_FAILED"},
		{"missing-repository-owner", admin, nil, "GATEWAY_IDENTITY_REQUIRED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input, key := valid, uuid.NewString()
			if test.edit != nil {
				test.edit(&input, &key)
			}
			out, err := (&distribution.Service{}).Provision(context.Background(), test.auth, input, key, uuid.NewString())
			if err == nil || err.Error() != test.code || !reflect.DeepEqual(out, distribution.Provisioned{}) {
				t.Fatal("admission leaked configuration or returned an uncommitted secret", err)
			}
		})
	}
}
