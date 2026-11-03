package configuration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJournalCapacityExplicitJSONEnvironmentAndRefusals(t *testing.T) {
	base := JournalCapacityRequest{FormatVersion: 1, ExpectedCurrentBytes: 16384, MaximumJournalBytes: 65536, Reason: "Synthetic journal recovery"}
	raw, _ := json.Marshal(base)
	got, e := LoadJournalCapacityRequest(raw, []byte("# Explicit overlay\nPROJECT_ONE_OFFLINE_JOURNAL_EXPECTED_CURRENT_BYTES=32768\nPROJECT_ONE_OFFLINE_JOURNAL_MAXIMUM_BYTES=131072\nPROJECT_ONE_OFFLINE_JOURNAL_CAPACITY_REASON=Recorded operator recovery\n"))
	if e != nil || got.ExpectedCurrentBytes != 32768 || got.MaximumJournalBytes != 131072 || got.Reason != "Recorded operator recovery" {
		t.Fatal("typed overlay", e)
	}
	for name, value := range map[string]string{
		"missing": `{}`, "null": `null`, "unknown": string(raw[:len(raw)-1]) + `,"other":1}`, "duplicate": string(raw[:len(raw)-1]) + `,"format_version":1}`, "false": strings.Replace(string(raw), `"maximum_journal_bytes":65536`, `"maximum_journal_bytes":false`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := LoadJournalCapacityRequest([]byte(value), nil); e == nil {
				t.Fatal("invalid JSON admitted")
			}
		})
	}
	for _, change := range []func(*JournalCapacityRequest){func(r *JournalCapacityRequest) { r.FormatVersion = 2 }, func(r *JournalCapacityRequest) { r.ExpectedCurrentBytes = 16383 }, func(r *JournalCapacityRequest) { r.MaximumJournalBytes = r.ExpectedCurrentBytes }, func(r *JournalCapacityRequest) { r.MaximumJournalBytes = MaximumJournalBytes + 1 }, func(r *JournalCapacityRequest) { r.Reason = " " }, func(r *JournalCapacityRequest) { r.Reason = "control\ntext" }, func(r *JournalCapacityRequest) { r.Reason = strings.Repeat("x", 501) }} {
		r := base
		change(&r)
		if r.Validate() == nil {
			t.Fatal("invalid request admitted")
		}
	}
	for _, env := range []string{"OTHER=1", "PROJECT_ONE_OFFLINE_JOURNAL_MAXIMUM_BYTES=false", "PROJECT_ONE_OFFLINE_JOURNAL_MAXIMUM_BYTES=1.5", "PROJECT_ONE_OFFLINE_JOURNAL_MAXIMUM_BYTES=65536\nPROJECT_ONE_OFFLINE_JOURNAL_MAXIMUM_BYTES=131072", "PROJECT_ONE_OFFLINE_JOURNAL_CAPACITY_REASON=$(external)", "PROJECT_ONE_OFFLINE_JOURNAL_MAXIMUM_BYTES="} {
		if _, e := LoadJournalCapacityRequest(raw, []byte(env)); e == nil {
			t.Fatal("invalid explicit environment admitted")
		}
	}
}
