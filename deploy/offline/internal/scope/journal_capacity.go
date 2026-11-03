package scope

import (
	"bytes"
	"context"
	"reflect"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const journalCapacityAction = "journal_capacity_extended"

type JournalCapacityRecord struct {
	Request       configuration.JournalCapacityRequest `json:"request"`
	RequestSHA256 string                               `json:"request_sha256"`
}
type JournalCapacityStatus struct {
	InitialMaximumBytes   int64  `json:"initial_maximum_bytes"`
	EffectiveMaximumBytes int64  `json:"effective_maximum_bytes"`
	UsedBytes             int64  `json:"used_bytes"`
	Qualification         string `json:"qualification"`
}
type JournalCapacityResult struct {
	Status        string                `json:"status"`
	ScopeID       string                `json:"scope_id"`
	RequestSHA256 string                `json:"request_sha256"`
	Journal       JournalCapacityStatus `json:"journal"`
}

func validateJournalCapacityEvent(events []Event, e Event) error {
	bad := func() error { return failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_TRANSITION_INVALID") }
	r := e.JournalCapacity
	if e.Action != journalCapacityAction || e.Outcome != "completed" || r == nil || r.Request.Validate() != nil || r.RequestSHA256 != wire.Digest(encode(r.Request)) {
		return bad()
	}
	expected := Event{FormatVersion: e.FormatVersion, Sequence: e.Sequence, PreviousSHA256: e.PreviousSHA256, ScopeID: e.ScopeID, ConfigurationSHA256: e.ConfigurationSHA256, ArchiveSHA256: e.ArchiveSHA256, ManifestSHA256: e.ManifestSHA256, OperationID: e.OperationID, Action: journalCapacityAction, Outcome: "completed", Time: e.Time, JournalCapacity: r}
	if !reflect.DeepEqual(e, expected) {
		return bad()
	}
	previous := int64(0)
	for _, prior := range events {
		if prior.OperationID == e.OperationID {
			return bad()
		}
		if prior.JournalCapacity != nil {
			previous = prior.JournalCapacity.Request.MaximumJournalBytes
		}
	}
	if previous != 0 && r.Request.ExpectedCurrentBytes != previous {
		return bad()
	}
	return nil
}

func effectiveJournalCapacity(events []Event, initial int64) (int64, error) {
	current := initial
	for _, event := range events {
		if event.JournalCapacity == nil {
			continue
		}
		r := event.JournalCapacity.Request
		if r.Validate() != nil || r.ExpectedCurrentBytes != current {
			return 0, failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_CHAIN_INVALID")
		}
		current = r.MaximumJournalBytes
	}
	return current, nil
}

// The complete hash chain is validated first. A later extension cannot authorize
// an earlier prefix that exceeded the policy effective at that point in time.
func validateJournalCapacityPrefixes(raw []byte, events []Event, initial int64) (int64, error) {
	lines := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
	if !bytes.HasSuffix(raw, []byte("\n")) || len(lines) != len(events) {
		return 0, failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_CHAIN_INVALID")
	}
	limit, used := initial, int64(0)
	for i, event := range events {
		if event.JournalCapacity != nil {
			r := event.JournalCapacity.Request
			if r.Validate() != nil || r.ExpectedCurrentBytes != limit {
				return 0, failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_CHAIN_INVALID")
			}
			limit = r.MaximumJournalBytes
		}
		used += int64(len(lines[i]) + 1)
		if used > limit {
			return 0, failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
		}
	}
	return limit, nil
}

func (l *locked) journalStatus() JournalCapacityStatus {
	limit, _ := effectiveJournalCapacity(l.events, l.config.MaximumJournalBytes)
	return JournalCapacityStatus{InitialMaximumBytes: l.config.MaximumJournalBytes, EffectiveMaximumBytes: limit, UsedBytes: l.journalBytes, Qualification: "verified_local_journal_capacity_only_not_runtime_health_or_business_authority"}
}

func ExtendJournalCapacity(ctx context.Context, c configuration.Config, r configuration.JournalCapacityRequest) (JournalCapacityResult, error) {
	if e := r.Validate(); e != nil {
		return JournalCapacityResult{}, e
	}
	l, e := open(c, true)
	if e != nil {
		return JournalCapacityResult{}, e
	}
	defer l.close()
	result := func(status string) JournalCapacityResult {
		return JournalCapacityResult{Status: status, ScopeID: l.marker.ScopeID, RequestSHA256: wire.Digest(encode(r)), Journal: l.journalStatus()}
	}
	if ctx.Err() != nil {
		return result("journal_capacity_unchanged"), failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	for _, event := range l.events {
		if event.JournalCapacity != nil && event.JournalCapacity.RequestSHA256 == wire.Digest(encode(r)) {
			return result("observed_prior_journal_capacity_extension"), nil
		}
	}
	limit, e := effectiveJournalCapacity(l.events, c.MaximumJournalBytes)
	if e != nil {
		return result("journal_capacity_unchanged"), e
	}
	if r.ExpectedCurrentBytes != limit {
		return result("journal_capacity_unchanged"), failure("OFFLINE_SCOPE_JOURNAL_EXPECTED_CAPACITY_CHANGED")
	}
	for _, path := range []string{"/", c.ScopeRoot} {
		var fs syscall.Statfs_t
		if syscall.Statfs(path, &fs) != nil || fs.Blocks == 0 || float64(fs.Bavail)*float64(fs.Bsize)-float64(r.MaximumJournalBytes-limit) < float64(fs.Blocks)*float64(fs.Bsize)*minimumProcessFilesystemFreeFraction {
			return result("journal_capacity_unchanged"), failure("OFFLINE_SCOPE_PROCESS_STORAGE_HEADROOM_REQUIRED")
		}
	}
	id, e := nonce()
	if e != nil {
		return result("journal_capacity_unchanged"), e
	}
	record := JournalCapacityRecord{Request: r, RequestSHA256: wire.Digest(encode(r))}
	if e = l.appendEvent(Event{OperationID: id, Action: journalCapacityAction, Outcome: "completed", JournalCapacity: &record}); e != nil {
		return result("journal_capacity_outcome_unresolved"), e
	}
	return result("journal_capacity_extended"), nil
}

// The locked journal has already passed complete context/chain/capacity validation.
func (l *locked) maximumJournalBytes() int64 {
	limit, _ := effectiveJournalCapacity(l.events, l.config.MaximumJournalBytes)
	return limit
}
