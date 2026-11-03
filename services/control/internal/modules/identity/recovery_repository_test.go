package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

func TestRecoveryInputsAndMissingAuthorityFailBeforeMutation(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	for _, e := range []error{s.RequestReset(ctx, "synthetic@example.invalid", "operator", "synthetic", ""), s.Reset(ctx, "invalid", "unused", "portal", "")} {
		if code(e) != "VALIDATION_FAILED" && code(e) != "TOKEN_UNUSABLE" {
			t.Fatal(e)
		}
	}
	for _, e := range []error{s.RequestReset(ctx, "synthetic@example.invalid", "portal", "synthetic", ""), s.Reset(ctx, security.Token(), "unused", "portal", ""), s.RecoverAdmin(ctx, "unused", "Synthetic recovery")} {
		if code(e) != "IDENTITY_STORAGE_REQUIRED" {
			t.Fatal(e)
		}
	}
	if e := revokeAuthorityWithin(ctx, nil, "invalid", "password_reset"); code(e) != "IDENTITY_AUTHORITY_CONTEXT_REQUIRED" {
		t.Fatal(e)
	}
	if e := s.InvalidateAccountAuthorities(ctx, nil, "Synthetic restore"); code(e) != "IDENTITY_TRANSACTION_REQUIRED" {
		t.Fatal(e)
	}
}

func TestRecoveryOperatorReasonDomain(t *testing.T) {
	for _, value := range []string{"", " \t", strings.Repeat("a", maximumOperatorReasonRunes+1), string([]byte{0xff})} {
		if recoveryReason(value) {
			t.Fatal("invalid operator reason accepted")
		}
		if e := (&Service{}).RecoverAdmin(context.Background(), "unused", value); code(e) != "OPERATOR_REASON_REQUIRED" {
			t.Fatal(e)
		}
	}
	if !recoveryReason(strings.Repeat("\u754c", maximumOperatorReasonRunes)) {
		t.Fatal("valid Unicode reason refused")
	}
}
