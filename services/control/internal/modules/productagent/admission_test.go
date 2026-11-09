package productagent

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
)

func TestCommandDispatchRejectsConflictingOrUnavailableRisk(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		command    riskTier
		catalog    riskTier
		method     string
	}{
		{name: "missing command risk", path: "/keys", catalog: riskRead, method: http.MethodGet},
		{name: "unknown command risk", path: "/keys", command: "other", catalog: riskRead, method: http.MethodGet},
		{name: "sensitive read with ordinary catalog policy", path: "/keys", command: riskSensitiveRead, catalog: riskRead, method: http.MethodGet},
		{name: "reversible write with ordinary catalog policy", path: "/keys", command: riskReversibleWrite, catalog: riskRead, method: http.MethodGet},
		{name: "navigation sharing a tool ID", path: "/keys", command: riskNavigate, catalog: riskRead, method: http.MethodGet},
		{name: "read with navigation destination", path: "/keys", command: riskRead, catalog: riskRead, method: http.MethodGet},
		{name: "read without reviewed catalog entry", command: riskRead},
		{name: "read with sensitive catalog policy", command: riskRead, catalog: riskSensitiveRead, method: http.MethodGet},
		{name: "read with write catalog policy", command: riskRead, catalog: riskReversibleWrite, method: http.MethodPost},
		{name: "read label with write method", command: riskRead, catalog: riskRead, method: http.MethodPost},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service, err := New(&applications.Service{Identity: &identity.Service{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			// The rejected command otherwise has an active read adapter and a scoped actor.
			const input = "show applications"
			service.catalog.aliases[input] = command{id: listApplications, risk: tt.command, portal: tt.path}
			if tt.catalog == "" {
				delete(service.tools, listApplications)
			} else {
				policy := service.tools[listApplications]
				policy.Risk, policy.Method = tt.catalog, tt.method
				service.tools[listApplications] = policy
			}
			actor := identity.Auth{Me: identity.Me{Audience: "portal", Workspace: &identity.Workspace{}}}
			result, err := service.Run(t.Context(), actor, input, "")
			if result != (Result{}) || !errors.Is(err, ErrCommandDenied) {
				t.Fatalf("expected denial before storage, got %#v, %v", result, err)
			}
		})
	}
}

func TestInstallationPolicyDeniesBeforeCommandDispatch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		policy ExecutionPolicy
		want   error
	}{
		{"no source", nil, errExecutionPolicyUnavailable},
		{"disabled", func(context.Context) (bool, error) { return false, nil }, ErrCommandDenied},
		{"read failed", func(context.Context) (bool, error) { return true, errors.New("private reader error") }, errExecutionPolicyUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, err := New(&applications.Service{Identity: &identity.Service{}}, tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			// Any navigation or read dispatch would require these dependencies.
			service.identity, service.applications = nil, nil
			actor := identity.Auth{Me: identity.Me{Audience: "portal", Workspace: &identity.Workspace{}}}
			for _, input := range []string{"open home", "show applications", "show conversations"} {
				result, err := service.Run(t.Context(), actor, input, "")
				if result != (Result{}) || err != tc.want {
					t.Fatalf("Run(%q) returned %#v, %v; want empty result and %v", input, result, err, tc.want)
				}
				result, err = service.RunForRequest(t.Context(), actor, input, "", uuid.NewString())
				if result != (Result{}) || err != tc.want {
					t.Fatalf("RunForRequest(%q) returned %#v, %v; want empty result and %v", input, result, err, tc.want)
				}
			}
		})
	}
}
