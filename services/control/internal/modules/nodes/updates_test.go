package nodes

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func updateBoundaryActor() identity.Auth {
	a := identity.Auth{}
	a.Me.Audience, a.Me.User.IsPlatformAdmin = "admin", true
	return a
}

func TestNodeUpdateInputBoundary(t *testing.T) {
	var service *Service
	for _, kind := range []string{"context", "actor", "node", "request", "name", "status", "version", "version_overflow", "reason", "reason_nul"} {
		t.Run(kind, func(t *testing.T) {
			ctx, a, id, request := context.Background(), updateBoundaryActor(), uuid.NewString(), uuid.NewString()
			name, status, reason, version := "Synthetic node update", "active", "Explicit synthetic change", int64(1)
			switch kind {
			case "context":
				ctx = nil
			case "actor":
				a.Me.Audience = "portal"
			case "node":
				id = "invalid"
			case "request":
				request = "invalid"
			case "name":
				name = "invalid\x00"
			case "status":
				status = "unknown"
			case "version":
				version = 0
			case "version_overflow":
				version = workspaces.MaximumVersion + 1
			case "reason":
				reason = " "
			case "reason_nul":
				reason = "invalid\x00"
			}
			value, e := service.Update(ctx, a, id, name, status, version, reason, request)
			if e == nil || e.Error() == "NODE_IDENTITY_REQUIRED" || !reflect.DeepEqual(value, Node{}) {
				t.Fatal("invalid mutation reached SQL or exposed DTO", e)
			}
		})
	}
}

func TestNodeUpdateIdentityAndStorageBoundary(t *testing.T) {
	for _, service := range []*Service{nil, {}, {Identity: &identity.Service{}}} {
		value, e := service.Update(t.Context(), updateBoundaryActor(), uuid.NewString(), "Synthetic update", "disabled", 1, "Explicit change", uuid.NewString())
		if e == nil || e.Error() != "NODE_IDENTITY_REQUIRED" || !reflect.DeepEqual(value, Node{}) {
			t.Fatal("missing mutation identity reached SQL", e)
		}
	}
	owner := &identity.Service{Storage: &nodeReadDeadlineStore{}}
	service := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}, DB: &pgxpool.Pool{}}
	if value, e := service.Update(t.Context(), updateBoundaryActor(), uuid.NewString(), "Synthetic update", "disabled", 1, "Explicit change", uuid.NewString()); e == nil || e.Error() != "NODE_STORAGE_MISMATCH" || !reflect.DeepEqual(value, Node{}) {
		t.Fatal("foreign mutation pool accepted", e)
	}
}

func TestNodeUpdateDeadlineAndZeroFailure(t *testing.T) {
	// Explicit deadline-only boundary double. It never executes SQL/callback or
	// fabricates product success. Actual mutation/observer outcomes use real SQL.
	policy := config.DefaultRuntimePolicy()
	policy.HTTP.RequestSeconds = 19
	store := &nodeReadDeadlineStore{}
	owner := &identity.Service{Storage: store, Config: config.Config{Runtime: &policy}}
	service := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	call := func(ctx context.Context) {
		value, e := service.Update(ctx, updateBoundaryActor(), uuid.NewString(), "Synthetic update", "disabled", 1, "Explicit change", uuid.NewString())
		if e == nil || e.Error() != "STORAGE_UNAVAILABLE" || !reflect.DeepEqual(value, Node{}) {
			t.Fatal("mutation boundary exposed DTO or lost failure", e)
		}
	}
	before := time.Now()
	call(t.Context())
	if store.deadline.Before(before.Add(18*time.Second)) || store.deadline.After(before.Add(20*time.Second)) {
		t.Fatal("mutation lost owned deadline")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	expected, _ := ctx.Deadline()
	call(ctx)
	if !store.deadline.Equal(expected) {
		t.Fatal("mutation extended caller deadline")
	}
	cancel()
	call(ctx)
	if !errors.Is(store.cancelled, context.Canceled) {
		t.Fatal("mutation lost caller cancellation")
	}
}

func TestNodeUpdateErrorRedactionAndSafeOutcomes(t *testing.T) {
	if updateError(nil) != nil || updateError(errors.New("private connection SQL diagnostic")).Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("mutation diagnostic escaped or success changed")
	}
	if updateError(&transaction.Error{Kind: transaction.UniqueConflict}).Error() != "NODE_NAME_EXISTS" {
		t.Fatal("mutation unique conflict lost domain meaning")
	}
	for _, code := range []string{"VERSION_CONFLICT", "NODE_REMOVED", "RESOURCE_LIMIT_REACHED", "NODE_NAME_EXISTS", "ACCESS_DENIED", string(transaction.CommitUnknown), string(transaction.Cancelled), string(transaction.Timeout)} {
		if e := updateError(errors.New(code)); e == nil || e.Error() != code {
			t.Fatal("safe mutation outcome changed", code)
		}
	}
}
