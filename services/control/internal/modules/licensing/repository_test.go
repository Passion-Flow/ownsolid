package licensing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func TestLicenseRepositoryMicrosecondFenceAndRollbackBoundaries(t *testing.T) {
	stamp := time.Date(2026, 10, 19, 1, 2, 3, 123456000, time.UTC)
	for _, test := range []struct {
		wall, observed time.Time
		tolerance      int
		want           time.Time
		suspect        bool
	}{
		{stamp.Add(789 * time.Nanosecond), stamp, 0, stamp, false},
		{stamp, stamp.Add(789 * time.Nanosecond), 0, stamp, false},
		{stamp.Add(-time.Microsecond), stamp, 0, stamp, true},
		{stamp.Add(-30 * time.Second), stamp, 30, stamp, false},
		{stamp.Add(-30*time.Second - time.Microsecond), stamp, 30, stamp, true},
		{stamp.Add(time.Hour), stamp, 0, stamp.Add(time.Hour), false},
		{stamp.In(time.FixedZone("synthetic-offset", 3600)), stamp, 0, stamp, false},
	} {
		actual, suspect, e := observeTime(test.wall, test.observed, test.tolerance)
		if e != nil || !actual.Equal(test.want) || actual.Location() != time.UTC || suspect != test.suspect {
			t.Fatal("persisted microsecond fence or exact rollback boundary changed", e)
		}
	}
	for _, invalid := range []time.Time{time.Time{}, time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		if _, _, e := observeTime(invalid, stamp, 0); e == nil {
			t.Fatal("invalid trusted wall time accepted")
		}
		if _, _, e := observeTime(stamp, invalid, 0); e == nil {
			t.Fatal("invalid stored fence accepted")
		}
	}
	for _, invalid := range []int{-1, wire.MaximumRollbackToleranceSeconds + 1} {
		if _, _, e := observeTime(stamp, stamp, invalid); e == nil {
			t.Fatal("invalid rollback policy accepted")
		}
	}
}

func TestLicenseRepositoryConfigurationAndInputBeforeStorage(t *testing.T) {
	_, trust, e := wire.GenerateKey("synthetic-repository-boundary")
	if e != nil {
		t.Fatal(e)
	}
	for _, owner := range []*identity.Service{nil, {}} {
		if _, e := New(owner, trust, wire.DefaultPolicy(), time.Now); e == nil || e.Error() != "LICENSE_CONFIGURATION_REQUIRED" {
			t.Fatal("missing trusted storage constructor accepted", e)
		}
	}
	if _, e := New(&identity.Service{}, trust, wire.DefaultPolicy(), nil); e == nil {
		t.Fatal("nil trusted clock accepted")
	}
	var absent *Service
	if e := absent.Check(context.Background()); e == nil || e.Error() != "LICENSE_CONFIGURATION_REQUIRED" {
		t.Fatal("nil License service reached storage", e)
	}
	actor := identity.Auth{}
	actor.Me.User.ID, actor.SessionID, actor.Me.Audience, actor.Me.User.IsPlatformAdmin = uuid.NewString(), uuid.NewString(), "admin", true
	for _, input := range []ImportInput{
		{Reason: ""}, {Reason: " leading"}, {Reason: "private\nvalue"},
		{Reason: strings.Repeat("\u4e16", wire.MaximumReasonRunes+1)},
		{Reason: "Synthetic reason", ExpectedCurrentLicenseID: "invalid"},
		{Reason: "Synthetic reason", Artifact: strings.Repeat("x", wire.MaximumArtifactBytes+1)},
	} {
		value, e := absent.Import(context.Background(), actor, input, uuid.NewString())
		if e == nil || e.Error() != "VALIDATION_FAILED" || value.ImportedLicenseID != "" || value.State.Current != nil {
			t.Fatal("malformed import reached absent storage or fabricated a receipt", e)
		}
	}
}

func TestLicenseRepositoryStableStorageOutcomesAndRedaction(t *testing.T) {
	if repositoryError(nil) != nil {
		t.Fatal("success gained error")
	}
	for _, kind := range []transaction.ErrorKind{transaction.Unavailable, transaction.Deadlock, transaction.LockTimeout, transaction.Cancelled, transaction.Timeout, transaction.CommitUnknown, transaction.CommitRolledBack} {
		if e := repositoryError(&transaction.Error{Kind: kind}); e == nil || e.Error() != string(kind) {
			t.Fatal("distinct actual storage outcome lost")
		}
	}
	if e := repositoryError(errors.New("private SQL/artifact/password")); e.Error() != "STORAGE_UNAVAILABLE" {
		t.Fatal("private diagnostic escaped")
	}
	if e := repositoryError(errors.New("LICENSE_CURRENT_CHANGED")); e.Error() != "LICENSE_CURRENT_CHANGED" {
		t.Fatal("committed domain denial lost")
	}
}
