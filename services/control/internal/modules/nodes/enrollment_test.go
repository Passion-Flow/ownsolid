package nodes

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func enrollmentBoundaryInput() wire.Enrollment {
	return wire.Enrollment{JoinToken: security.Token(), Handshake: wire.Handshake{
		ProtocolVersion: wire.Version, Capabilities: wire.Capabilities(wire.ReadOnlyProfile),
		Facts: wire.Facts{OSID: "ubuntu", OSVersion: "24.04", Architecture: "amd64", Hostname: "synthetic-enrollment",
			LogicalCPUs: 2, MemoryTotalBytes: 1 << 30, DiskPath: "/", DiskTotalBytes: 10 << 30,
			DiskAvailableBytes: 5 << 30, ObservedAt: time.Now().UTC(), AgentVersion: wire.AgentVersion}}}
}

func TestNodeEnrollmentInputBoundary(t *testing.T) {
	var service *Service
	for _, name := range []string{"context", "request", "token", "protocol", "capabilities", "facts", "component", "inference"} {
		t.Run(name, func(t *testing.T) {
			input, ctx, request := enrollmentBoundaryInput(), context.Background(), uuid.NewString()
			switch name {
			case "context":
				ctx = nil
			case "request":
				request = "invalid"
			case "token":
				input.JoinToken = "invalid"
			case "protocol":
				input.ProtocolVersion = "unsupported"
			case "capabilities":
				input.Capabilities = []string{"shell.v1"}
			case "facts":
				input.Facts.Hostname = "invalid\x00"
			case "component":
				input.Component = &wire.ComponentObservation{}
			case "inference":
				input.Inference = &wire.InferenceObservation{}
			}
			value, e := service.Enroll(ctx, input, request)
			if e == nil || e.Error() == "NODE_IDENTITY_REQUIRED" || !reflect.DeepEqual(value, wire.Enrolled{}) {
				t.Fatal("invalid enrollment reached storage or exposed credential", e)
			}
		})
	}
}

func TestNodeEnrollmentIdentityAndStorageBoundary(t *testing.T) {
	input := enrollmentBoundaryInput()
	for _, service := range []*Service{nil, {}, {Identity: &identity.Service{}}} {
		value, e := service.Enroll(t.Context(), input, uuid.NewString())
		if e == nil || e.Error() != "NODE_IDENTITY_REQUIRED" || !reflect.DeepEqual(value, wire.Enrolled{}) {
			t.Fatal("missing enrollment identity reached SQL", e)
		}
	}
	owner := &identity.Service{Storage: &nodeReadDeadlineStore{}}
	service := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}, DB: &pgxpool.Pool{}}
	if value, e := service.Enroll(t.Context(), input, uuid.NewString()); e == nil || e.Error() != "NODE_STORAGE_MISMATCH" || !reflect.DeepEqual(value, wire.Enrolled{}) {
		t.Fatal("foreign enrollment pool was accepted", e)
	}
}

func TestNodeEnrollmentDeadlineAndZeroFailure(t *testing.T) {
	// This explicit boundary double never executes SQL or the callback. Actual
	// post-callback failure/commit and credential assertions use real SQL tests.
	policy := config.DefaultRuntimePolicy()
	policy.HTTP.RequestSeconds = 17
	store := &nodeReadDeadlineStore{}
	owner := &identity.Service{Storage: store, Config: config.Config{Runtime: &policy}}
	service := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	before := time.Now()
	value, e := service.Enroll(t.Context(), enrollmentBoundaryInput(), uuid.NewString())
	if e == nil || e.Error() != "STORAGE_UNAVAILABLE" || !reflect.DeepEqual(value, wire.Enrolled{}) ||
		store.deadline.Before(before.Add(16*time.Second)) || store.deadline.After(before.Add(18*time.Second)) {
		t.Fatal("enrollment lost owned deadline or exposed failure credential", e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	expected, _ := ctx.Deadline()
	value, e = service.Enroll(ctx, enrollmentBoundaryInput(), uuid.NewString())
	if e == nil || !reflect.DeepEqual(value, wire.Enrolled{}) || !store.deadline.Equal(expected) {
		t.Fatal("enrollment extended shorter caller or exposed failure credential", e)
	}
	cancel()
	service.Enroll(ctx, enrollmentBoundaryInput(), uuid.NewString())
	if store.cancelled == nil {
		t.Fatal("enrollment lost caller cancellation")
	}
}

func TestNodeEnrollmentErrorRedactionAndSafeOutcomes(t *testing.T) {
	if actual := enrollmentError(errors.New("private SQL or connection diagnostic")); actual == nil || actual.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("private enrollment diagnostic escaped", actual)
	}
	for _, code := range []string{"TOKEN_UNUSABLE", "AGENT_PROFILE_REJECTED", "RESOURCE_LIMIT_REACHED", "NODE_NAME_EXISTS", "ACCESS_DENIED",
		string(transaction.CommitUnknown), string(transaction.Cancelled), string(transaction.Timeout)} {
		if e := enrollmentError(errors.New(code)); e == nil || e.Error() != code {
			t.Fatal("safe enrollment outcome changed", code)
		}
	}
}
