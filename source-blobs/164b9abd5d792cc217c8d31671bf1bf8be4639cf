package sso

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestSSOMaintenanceStableErrorsAndPrestorageBoundaries(t *testing.T) {
	if maintenanceError(nil) != nil {
		t.Fatal("successful maintenance gained an error")
	}
	for _, kind := range []transaction.ErrorKind{transaction.Unavailable, transaction.Deadlock, transaction.LockTimeout, transaction.Cancelled, transaction.Timeout, transaction.CommitUnknown, transaction.CommitRolledBack} {
		if e := maintenanceError(&transaction.Error{Kind: kind}); e == nil || e.Error() != string(kind) {
			t.Fatal("stable maintenance outcome discarded")
		}
	}
	if e := maintenanceError(errors.New("sensitive vendor query/password")); e.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("maintenance diagnostic leaked raw input")
	}
	var missing *Service
	if e := missing.Cleanup(context.Background()); e == nil || e.Error() != "SSO_CONFIGURATION_REQUIRED" {
		t.Fatal("nil maintenance service accepted")
	}
	s := &Service{Identity: &identity.Service{}, policy: config.DefaultSSOPolicy()}
	if e := s.Cleanup(nil); e == nil || e.Error() != "SSO_CONFIGURATION_REQUIRED" {
		t.Fatal("nil maintenance context accepted")
	}
	s.policy.CleanupBatchSize = 0
	if e := s.Cleanup(context.Background()); e == nil || e.Error() != "CONFIG_SSO_POLICY_INVALID" {
		t.Fatal("invalid policy reached absent storage")
	}
	if e := InvalidateAuthoritiesWithin(context.Background(), nil); e == nil || e.Error() != "SSO_TRANSACTION_STORAGE_MISMATCH" {
		t.Fatal("borrowed invalidation accepted absent transaction")
	}
	if e := InvalidateAuthorities(context.Background(), nil); e == nil || e.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("legacy facade panicked or hid absent actual transaction")
	}
	if _, e := maintenanceIDs(context.Background(), nil, transaction.Statement{}, 1); e == nil {
		t.Fatal("ID scanner accepted absent storage")
	}
	if e := cleanupFlowsWithin(context.Background(), nil, 1, 1); e == nil {
		t.Fatal("cleanup accepted absent storage")
	}
}

func TestSSOMaintenanceRunRejectsInvalidPolicyAndJoinsCancellation(t *testing.T) {
	s := &Service{Identity: &identity.Service{}, policy: config.DefaultSSOPolicy()}
	s.policy.CleanupSeconds = 0
	var reported error
	s.Run(context.Background(), func(e error) { reported = e })
	if reported == nil || reported.Error() != "CONFIG_SSO_POLICY_INVALID" {
		t.Fatal("invalid policy constructed an unsafe ticker")
	}
	s.policy = config.DefaultSSOPolicy()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx, func(error) { t.Error("normal shutdown reported failure") }); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance loop did not join cancellation")
	}
}
