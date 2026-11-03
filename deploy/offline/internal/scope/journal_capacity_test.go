package scope

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func journalCapacityFixture(t *testing.T) configuration.Config {
	t.Helper()
	c := fixture(t)
	c.MaximumJournalBytes = configuration.MinimumJournalBytes
	if _, e := Create(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	return c
}
func TestJournalCapacityRealPrefixReopenReservationsAndMonotonicReplay(t *testing.T) {
	c := journalCapacityFixture(t)
	ctx := context.Background()
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	marker, _ := os.ReadFile(c.ScopeRoot + "/" + markerName)
	fingerprint := c.Fingerprint()
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	if admitProcessJournal(l, 8) == nil {
		t.Fatal("initial reservation exceeded cap")
	}
	l.close()
	r := configuration.JournalCapacityRequest{FormatVersion: 1, ExpectedCurrentBytes: c.MaximumJournalBytes, MaximumJournalBytes: 65536, Reason: "Synthetic explicit journal extension"}
	out, e := ExtendJournalCapacity(ctx, c, r)
	if e != nil || out.Journal.EffectiveMaximumBytes != 65536 || out.Journal.InitialMaximumBytes != c.MaximumJournalBytes {
		t.Fatal("extension", out, e)
	}
	after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	m, _ := os.ReadFile(c.ScopeRoot + "/" + markerName)
	if !bytes.HasPrefix(after, before) || !bytes.Equal(marker, m) || c.Fingerprint() != fingerprint {
		t.Fatal("original context or prefix changed")
	}
	l, e = open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	if admitProcessJournal(l, 8) != nil {
		t.Fatal("new capacity not effective")
	}
	l.close()
	for i := 0; i < 20; i++ {
		current := out.Journal.EffectiveMaximumBytes
		next := configuration.JournalCapacityRequest{FormatVersion: 1, ExpectedCurrentBytes: current, MaximumJournalBytes: current + 2048, Reason: strings.Repeat("s", 500)}
		out, e = ExtendJournalCapacity(ctx, c, next)
		if e != nil {
			t.Fatal("later extension", e)
		}
	}
	raw, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if int64(len(raw)) <= c.MaximumJournalBytes {
		t.Fatal("fixture did not exceed initial read boundary")
	}
	repeat, e := ExtendJournalCapacity(ctx, c, r)
	unchanged, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	if e != nil || repeat.Status != "observed_prior_journal_capacity_extension" || repeat.Journal.EffectiveMaximumBytes != out.Journal.EffectiveMaximumBytes || !bytes.Equal(raw, unchanged) {
		t.Fatal("prior request replay changed capacity", e)
	}
	status, e := Read(ctx, c)
	if e != nil || status.JournalCapacity == nil || status.JournalCapacity.UsedBytes != int64(len(raw)) || status.JournalCapacity.EffectiveMaximumBytes != out.Journal.EffectiveMaximumBytes {
		t.Fatal("reopen/status", e)
	}
	r.Reason = "Stale different request"
	if _, e = ExtendJournalCapacity(ctx, c, r); e == nil {
		t.Fatal("stale request admitted")
	}
}
func TestJournalCapacityRejectsForgedMixedAndWrongInitialEventsWithoutWrite(t *testing.T) {
	c := journalCapacityFixture(t)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	before, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
	for name, change := range map[string]func(*Event){
		"mixed_engine": func(e *Event) { e.Engine = &engine.Observation{} }, "mixed_process": func(e *Event) { e.Process = &ProcessRecord{} }, "reference": func(e *Event) { e.ReferenceOperationID = strings.Repeat("a", 32) }, "code": func(e *Event) { e.Code = "FORGED" }, "outcome": func(e *Event) { e.Outcome = "started" }, "action": func(e *Event) { e.Action = "process_start" }, "missing": func(e *Event) { e.JournalCapacity = nil }, "digest": func(e *Event) { e.JournalCapacity.RequestSHA256 = strings.Repeat("a", 64) }, "wrong_initial": func(e *Event) {
			e.JournalCapacity.Request.ExpectedCurrentBytes *= 2
			e.JournalCapacity.RequestSHA256 = wire.Digest(encode(e.JournalCapacity.Request))
		},
	} {
		t.Run(name, func(t *testing.T) {
			id, _ := nonce()
			r := configuration.JournalCapacityRequest{FormatVersion: 1, ExpectedCurrentBytes: c.MaximumJournalBytes, MaximumJournalBytes: 65536, Reason: "Synthetic validation"}
			event := Event{OperationID: id, Action: journalCapacityAction, Outcome: "completed", JournalCapacity: &JournalCapacityRecord{Request: r, RequestSHA256: wire.Digest(encode(r))}}
			change(&event)
			if l.appendEvent(event) == nil {
				t.Fatal("forged event admitted")
			}
			after, _ := os.ReadFile(c.ScopeRoot + "/" + journalName)
			if !bytes.Equal(before, after) {
				t.Fatal("invalid event wrote journal")
			}
		})
	}
}
func TestJournalCapacityConcurrentExpectedValueAndCancellation(t *testing.T) {
	c := journalCapacityFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := configuration.JournalCapacityRequest{FormatVersion: 1, ExpectedCurrentBytes: c.MaximumJournalBytes, MaximumJournalBytes: 65536, Reason: "Synthetic concurrent request"}
	if _, e := ExtendJournalCapacity(ctx, c, r); e == nil {
		t.Fatal("cancelled request admitted")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			copy := r
			if i == 1 {
				copy.Reason = "Different concurrent request"
			}
			_, e := ExtendJournalCapacity(context.Background(), c, copy)
			results <- e
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if e.Error() != "OFFLINE_SCOPE_BUSY" && e.Error() != "OFFLINE_SCOPE_JOURNAL_EXPECTED_CAPACITY_CHANGED" {
			t.Fatal("unexpected contention", e)
		}
	}
	if success != 1 {
		t.Fatal("capacity expected value did not serialize", success)
	}
}

func TestJournalCapacityRejectsRetroactivelyExpandedValidHashChain(t *testing.T) {
	c := journalCapacityFixture(t)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := regular(l.root, journalName, configuration.MaximumJournalBytes)
	if e != nil {
		t.Fatal(e)
	}
	m, previous, sequence, limit := l.marker, l.lastSHA, len(l.events), c.MaximumJournalBytes
	for i := 0; i < 7; i++ {
		maximum := limit + 1
		if i == 6 {
			maximum = 65536
		}
		request := configuration.JournalCapacityRequest{FormatVersion: 1, ExpectedCurrentBytes: limit, MaximumJournalBytes: maximum, Reason: "Synthetic adversarial history"}
		id, _ := nonce()
		event := Event{FormatVersion: scopeFormatVersion, Sequence: sequence + 1, PreviousSHA256: previous, ScopeID: m.ScopeID, ConfigurationSHA256: m.ConfigurationSHA256, ArchiveSHA256: m.ArchiveSHA256, ManifestSHA256: m.ManifestSHA256, OperationID: id, Action: journalCapacityAction, Outcome: "completed", Time: "2026-10-18T00:00:00Z", JournalCapacity: &JournalCapacityRecord{Request: request, RequestSHA256: wire.Digest(encode(request))}}
		line := bytes.TrimSuffix(encode(event), []byte("\n"))
		// Valid JSON trailing whitespace makes early prefixes exceed the initial cap
		// before the later large extension. Each individual event stays within4096.
		if i < 6 {
			line = append(line, bytes.Repeat([]byte(" "), maximumEventBytes-len(line))...)
		}
		previous = wire.Digest(line)
		raw = append(raw, line...)
		raw = append(raw, '\n')
		sequence++
		limit = maximum
	}
	events, hash, e := validateJournal(raw, m)
	if e != nil || hash != previous {
		t.Fatal("fixture is not an otherwise valid context/hash/request chain", e)
	}
	if final, e := effectiveJournalCapacity(events, c.MaximumJournalBytes); e != nil || final != 65536 || int64(len(raw)) > final {
		t.Fatal("fixture does not isolate historical prefix capacity", e)
	}
	l.close()
	// Explicitly corrupt only this owned test scope, with a matching head, so real
	// reopen must reject the historical budget rather than just a broken checksum.
	if e = os.WriteFile(c.ScopeRoot+"/"+journalName, raw, 0600); e != nil {
		t.Fatal(e)
	}
	head := journalHead{FormatVersion: scopeFormatVersion, ScopeID: m.ScopeID, ConfigurationSHA256: m.ConfigurationSHA256, Sequence: sequence, EventSHA256: previous, JournalBytes: int64(len(raw))}
	if e = os.WriteFile(c.ScopeRoot+"/"+headName, encode(head), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = Read(context.Background(), c); e == nil || e.Error() != "OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED" {
		t.Fatal("later increase retroactively authorized an oversized prefix", e)
	}
}

func TestJournalCapacityPreservesPendingProcessAndDoesNotResend(t *testing.T) {
	c, client, worker := processFixture(t)
	worker.fault = "before-start"
	result, e := processRuntime(context.Background(), c, client, worker, "running", false)
	if e == nil || !result.Pending {
		t.Fatal("explicit uncertain process fixture required", e)
	}
	starts, stops := worker.starts, worker.stops
	before, e := Read(context.Background(), c)
	if e != nil || before.Process == nil || !before.Process.Pending {
		t.Fatal("pending source status", e)
	}
	request := configuration.JournalCapacityRequest{FormatVersion: 1, ExpectedCurrentBytes: c.MaximumJournalBytes, MaximumJournalBytes: c.MaximumJournalBytes * 2, Reason: "Synthetic metadata while an unrelated effect remains pending"}
	if _, e = ExtendJournalCapacity(context.Background(), c, request); e != nil {
		t.Fatal(e)
	}
	after, e := Read(context.Background(), c)
	if e != nil || after.Process == nil || !after.Process.Pending || before.Process.OperationID != after.Process.OperationID || worker.starts != starts || worker.stops != stops {
		t.Fatal("capacity metadata changed pending business authority", e)
	}
	if _, e = processRuntime(context.Background(), c, client, worker, "running", false); e == nil || worker.starts != starts || worker.stops != stops {
		t.Fatal("metadata extension authorized pending resend", e)
	}
}
