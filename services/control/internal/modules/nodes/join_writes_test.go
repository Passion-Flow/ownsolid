package nodes

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func joinBoundaryAdmin() identity.Auth {
	a := identity.Auth{}
	a.Me.Audience = "admin"
	a.Me.User.IsPlatformAdmin = true
	return a
}
func TestNodeJoinInputsFailBeforeStorage(t *testing.T) {
	var s *Service
	a := joinBoundaryAdmin()
	id, request := uuid.NewString(), uuid.NewString()
	for name, call := range map[string]func() error{
		"mint_workspace": func() error { _, e := s.Mint(context.Background(), a, "invalid", "Synthetic", request); return e },
		"mint_request":   func() error { _, e := s.Mint(context.Background(), a, id, "Synthetic", "invalid"); return e },
		"mint_name":      func() error { _, e := s.Mint(context.Background(), a, id, " ", request); return e },
		"mint_profile": func() error {
			_, e := s.MintForProfile(context.Background(), a, id, "Synthetic", "unrecognized_profile", request)
			return e
		},
		"revoke_id": func() error {
			_, e := s.RevokeToken(context.Background(), a, "invalid", "Synthetic", request)
			return e
		},
		"revoke_request":        func() error { _, e := s.RevokeToken(context.Background(), a, id, "Synthetic", "invalid"); return e },
		"revoke_reason":         func() error { _, e := s.RevokeToken(context.Background(), a, id, " ", request); return e },
		"revoke_reason_control": func() error { _, e := s.RevokeToken(context.Background(), a, id, "Synthetic\x00", request); return e },
	} {
		t.Run(name, func(t *testing.T) {
			if e := call(); e == nil || e.Error() != "VALIDATION_FAILED" {
				t.Fatal("invalid node join input reached storage", e)
			}
		})
	}
	if v, e := s.Mint(context.Background(), identity.Auth{}, id, "Synthetic", request); e == nil || e.Error() != "ACCESS_DENIED" || !reflect.DeepEqual(v, MintedToken{}) {
		t.Fatal("missing Admin exposed token or reached storage", e)
	}
	if v, e := s.RevokeToken(context.Background(), identity.Auth{}, id, "Synthetic", request); e == nil || e.Error() != "ACCESS_DENIED" || !reflect.DeepEqual(v, JoinToken{}) {
		t.Fatal("missing Admin exposed token history or reached storage", e)
	}
}
func TestNodeJoinOwnedDeadlineAndZeroFailureOutput(t *testing.T) {
	// The existing explicit boundary store does not execute SQL/callbacks. Actual
	// vendor persistence,authority and post-callback COMMIT faults use tagged tests.
	p := config.DefaultRuntimePolicy()
	p.HTTP.RequestSeconds = 17
	store := &nodeReadDeadlineStore{}
	owner := &identity.Service{Storage: store, Config: config.Config{Runtime: &p}}
	s := &Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	a := joinBoundaryAdmin()
	id, request := uuid.NewString(), uuid.NewString()
	before := time.Now()
	v, e := s.Mint(context.Background(), a, id, "Synthetic", request)
	if e == nil || e.Error() != "STORAGE_UNAVAILABLE" || !reflect.DeepEqual(v, MintedToken{}) || store.deadline.Before(before.Add(16*time.Second)) || store.deadline.After(before.Add(18*time.Second)) {
		t.Fatal("mint lost owned deadline or exposed failure output", e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	expected, _ := ctx.Deadline()
	revoked, e := s.RevokeToken(ctx, a, id, "Synthetic", request)
	if e == nil || !reflect.DeepEqual(revoked, JoinToken{}) || !store.deadline.Equal(expected) {
		t.Fatal("revoke extended caller deadline or exposed failure output", e)
	}
	cancel()
	s.Mint(ctx, a, id, "Synthetic", request)
	if store.cancelled == nil {
		t.Fatal("writer ignored cancelled caller")
	}
}
func TestNodeJoinStorageErrorRedactionAndDomainOutcomes(t *testing.T) {
	for _, e := range []error{errors.New("sensitive-value SQL diagnostic"), &transaction.Error{Kind: transaction.Unavailable}} {
		if actual := joinWriteError(e); actual == nil || actual.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("raw node join diagnostic escaped")
		}
	}
	for _, code := range []string{"RESOURCE_LIMIT_REACHED", "NODE_JOIN_STATE_CONFLICT", string(transaction.CommitUnknown), string(transaction.Cancelled), string(transaction.Timeout)} {
		if e := joinWriteError(errors.New(code)); e == nil || e.Error() != code {
			t.Fatal("safe node join storage/domain outcome lost", code)
		}
	}
}
