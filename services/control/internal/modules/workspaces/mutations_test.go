package workspaces

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This rejection-only test double panics on any SQL/transaction attempt. It never
// supplies successful database results or represents an integration adapter.
type mutationNoSQL struct{ transaction.Store }

func (mutationNoSQL) Dialect() transaction.Dialect { return transaction.PostgreSQL }
func (mutationNoSQL) Within(context.Context, bool, func(context.Context, transaction.Executor) error) error {
	panic("unexpected SQL before validation")
}
func (mutationNoSQL) QueryRow(context.Context, transaction.Statement) transaction.Row {
	panic("unexpected SQL before validation")
}
func mutationUnitActor() identity.Auth {
	return identity.Auth{Me: identity.Me{User: identity.User{ID: uuid.NewString(), IsPlatformAdmin: true}, Audience: "admin"}, SessionID: uuid.NewString(), Epoch: 1, TokenHash: make([]byte, 32)}
}

func TestWorkspaceMutationInputsBeforeSQL(t *testing.T) {
	s := &Service{Identity: &identity.Service{Storage: mutationNoSQL{}}}
	a := mutationUnitActor()
	ctx := context.Background()
	id, user, request := uuid.NewString(), uuid.NewString(), uuid.NewString()
	cases := []struct {
		name string
		run  func() error
	}{
		{"invalid_initial_owner", func() error { _, e := s.Create(ctx, a, "Valid name", uuid.Nil.String(), request); return e }},
		{"invalid_create_request", func() error { _, e := s.Create(ctx, a, "Valid name", user, "private-invalid-request"); return e }},
		{"invalid_name", func() error { _, e := s.Create(ctx, a, "\x00", user, request); return e }},
		{"invalid_update_id", func() error {
			_, e := s.Update(ctx, a, "invalid", "Valid", "active", 1, "Synthetic", request)
			return e
		}},
		{"invalid_update_reason", func() error { _, e := s.Update(ctx, a, id, "Valid", "active", 1, "\x00", request); return e }},
		{"invalid_status", func() error { _, e := s.Update(ctx, a, id, "Valid", "unknown", 1, "Synthetic", request); return e }},
		{"unsafe_version", func() error {
			_, e := s.Update(ctx, a, id, "Valid", "active", MaximumVersion+1, "Synthetic", request)
			return e
		}},
		{"invalid_member_role", func() error {
			_, e := s.AddMember(ctx, a, id, "synthetic@example.invalid", "unknown", request)
			return e
		}},
		{"invalid_email", func() error { _, e := s.AddMember(ctx, a, id, "bad", "member", request); return e }},
		{"invalid_member_request", func() error {
			_, e := s.ChangeMember(ctx, a, id, user, "member", 1, "Synthetic", "bad", false)
			return e
		}},
		{"empty_member_reason", func() error { _, e := s.ChangeMember(ctx, a, id, user, "", 1, " ", request, true); return e }},
		{"note_NUL", func() error { _, e := s.CreateNote(ctx, a, id, "Valid", "\x00", request); return e }},
		{"note_invalid_UTF8", func() error { _, e := s.CreateNote(ctx, a, id, "Valid", string([]byte{0xff}), request); return e }},
		{"note_bytes", func() error {
			_, e := s.CreateNote(ctx, a, id, "Valid", strings.Repeat("x", MaximumNoteTextBytes+1), request)
			return e
		}},
		{"note_scope", func() error { _, e := s.CreateNote(ctx, a, uuid.Nil.String(), "Valid", "text", request); return e }},
		{"note_version", func() error { _, e := s.ChangeNote(ctx, a, id, user, "", "", 0, request, true); return e }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if e := c.run(); e == nil || e.Error() != "VALIDATION_FAILED" {
				t.Fatal("invalid input must fail before SQL", e)
			}
		})
	}
}
func TestWorkspaceMutationAuthorityAndStorageBeforeSQL(t *testing.T) {
	a := mutationUnitActor()
	ctx := context.Background()
	id, request := uuid.NewString(), uuid.NewString()
	s := &Service{Identity: &identity.Service{Storage: mutationNoSQL{}}}
	for _, change := range []func(*identity.Auth){func(a *identity.Auth) { a.SessionID = "bad" }, func(a *identity.Auth) { a.TokenHash = nil }, func(a *identity.Auth) { a.Epoch = 0 }, func(a *identity.Auth) { a.Me.Audience = "unknown" }} {
		invalid := a
		change(&invalid)
		if _, e := s.CreateNote(ctx, invalid, id, "Valid", "text", request); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal(e)
		}
		if _, e := s.AddMember(ctx, invalid, id, "synthetic@example.invalid", "member", request); e == nil || e.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatal(e)
		}
	}
	missing := &Service{}
	if _, e := missing.CreateNote(ctx, a, id, "Valid", "text", request); e == nil || e.Error() != "WORKSPACE_IDENTITY_REQUIRED" {
		t.Fatal(e)
	}
	pool, other := &pgxpool.Pool{}, &pgxpool.Pool{}
	mismatched := &Service{DB: pool, Identity: &identity.Service{DB: other}}
	if _, e := mismatched.CreateNote(ctx, a, id, "Valid", "text", request); e == nil || e.Error() != "WORKSPACE_STORAGE_MISMATCH" {
		t.Fatal(e)
	}
	ordinary := a
	ordinary.Me.Audience = "portal"
	if _, e := s.Create(ctx, ordinary, "Valid", id, request); e == nil || e.Error() != "ACCESS_DENIED" {
		t.Fatal(e)
	}
}
func TestWorkspaceMutationFixedWireAndCommitOutcome(t *testing.T) {
	if !ValidVersion(MaximumVersion) || ValidVersion(MaximumVersion+1) || ValidVersion(0) {
		t.Fatal("version wire domain")
	}
	if !ValidReason(strings.Repeat("\u6d4b", 500)) || ValidReason(strings.Repeat("\u6d4b", 501)) || ValidReason("\x00") || ValidReason(string([]byte{0xff})) {
		t.Fatal("reason domain")
	}
	exact := strings.Repeat("\u6d4b", 2730) + "xx"
	if len(exact) != MaximumNoteTextBytes {
		t.Fatal("fixture bytes")
	}
	if _, e := noteInput("Unicode", exact); e != nil {
		t.Fatal(e)
	}
	if _, e := noteInput("Unicode", exact+"x"); e == nil {
		t.Fatal("byte ceiling")
	}
	for _, id := range []string{uuid.Nil.String(), "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA", "invalid"} {
		if ValidID(id) {
			t.Fatal("noncanonical/nil ID")
		}
	}
	if e := incrementable(MaximumVersion); e == nil || e.Error() != "RESOURCE_LIMIT_REACHED" {
		t.Fatal(e)
	}
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	if workspaceError(unknown) != unknown {
		t.Fatal("commit outcome erased")
	}
	if e := uniqueMutation(&transaction.Error{Kind: transaction.UniqueConflict}, "WORKSPACE_EXISTS"); e == nil || e.Error() != "WORKSPACE_EXISTS" {
		t.Fatal(e)
	}
	if e := workspaceError(&transaction.Error{Kind: transaction.Unavailable}); e == nil || e.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal(e)
	}
}
