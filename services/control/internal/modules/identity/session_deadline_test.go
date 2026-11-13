package identity

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSourceSessionDeadlineBoundary(t *testing.T) {
	base := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
	for _, item := range []struct {
		name  string
		value time.Time
		valid bool
	}{
		{"utc_microseconds", base, true},
		{"zero_offset_alias", base.In(time.FixedZone("zero", 0)), true},
		{"zero", time.Time{}, false},
		{"sub_microsecond_precision", base.Add(time.Nanosecond), false},
		{"non_utc_offset", base.In(time.FixedZone("offset", 3600)), false},
		{"before_portable_epoch", time.Date(1969, 12, 31, 23, 59, 59, 0, time.UTC), false},
		{"outside_shared_timestamp_domain", time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), false},
	} {
		t.Run(item.name, func(t *testing.T) {
			if sourceSessionDeadline(item.value) != item.valid {
				t.Fatal("source session deadline admitted an unsupported timestamp domain")
			}
		})
	}
}
func TestIssueSessionUntilRejectsInvalidAuthorityBeforeStorage(t *testing.T) {
	owner := &Service{}
	source := SessionSource{TenantID: uuid.NewString(), ConnectorID: uuid.NewString(), BindingID: uuid.NewString(), FlowID: uuid.NewString(), AuthVersion: 1}
	for _, item := range []struct {
		name   string
		ctx    context.Context
		source SessionSource
		until  time.Time
	}{
		{"missing_context", nil, source, time.Now().UTC().Truncate(time.Microsecond)},
		{"missing_source", context.Background(), SessionSource{}, time.Now().UTC().Truncate(time.Microsecond)},
		{"missing_deadline", context.Background(), source, time.Time{}},
	} {
		t.Run(item.name, func(t *testing.T) {
			out, e := owner.IssueSessionUntilWithin(item.ctx, nil, uuid.NewString(), 1, "portal", "", uuid.NewString(), item.source, item.until)
			if e == nil || e.Error() != "AUTH_SOURCE_DEADLINE_INVALID" || out != (IssuedSession{}) {
				t.Fatal("invalid source authority reached storage or released session", e)
			}
		})
	}
}
