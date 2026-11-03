package view

import (
	"strings"
	"testing"
)

func sample() Snapshot {
	return Snapshot{Format: Format, ScopeID: strings.Repeat("1", 32), ReleaseID: "r1", Profile: "synthetic-test", ArchiveSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64), ConfigurationSHA256: strings.Repeat("c", 64), Sequence: 1, HeadSHA256: strings.Repeat("d", 64), JournalBytes: 100, RecordedAt: "2026-10-24T00:00:00Z", FirstSequence: 1, Events: []Event{{Sequence: 1, OperationID: strings.Repeat("2", 32), Action: "release_prepare", Outcome: "failed", Code: "RELEASE_INPUT_INVALID", Time: "2026-10-24T00:00:00Z"}}}
}
func TestReleaseViewClosedBoundedProjection(t *testing.T) {
	s := sample()
	if e := s.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Snapshot){func(s *Snapshot) { s.Events[0].Code = "/private/credential" }, func(s *Snapshot) { s.Events[0].Action = "secret=value" }, func(s *Snapshot) { s.Sequence++ }, func(s *Snapshot) { s.HeadSHA256 = "unknown" }, func(s *Snapshot) { s.RecordedAt = "" }, func(s *Snapshot) { s.FirstSequence = 0 }, func(s *Snapshot) { s.Events = append(s.Events, s.Events...) }} {
		v := sample()
		mutate(&v)
		if v.Validate() == nil {
			t.Fatal("invalid projection admitted")
		}
	}
}
