package v1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

type timestampDocument struct {
	CreatedAt time.Time   `json:"created_at"`
	ClosedAt  *time.Time  `json:"closed_at,omitempty"`
	Observed  []time.Time `json:"observed"`
}

func TestStrictTimestampDocumentRoundtripRetainsTypedBoundaries(t *testing.T) {
	now := time.Date(2026, time.October, 22, 3, 39, 49, 568087300, time.UTC)
	want := timestampDocument{CreatedAt: now, ClosedAt: &now, Observed: []time.Time{now}}
	raw, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var actual timestampDocument
	if err = DecodeStrict(raw, &actual); err != nil || !reflect.DeepEqual(actual, want) {
		t.Fatal("standard JSON timestamp failed strict roundtrip", err)
	}
	for name, changed := range map[string]string{
		"null":            strings.Replace(string(raw), `"created_at":"2026-10-22T03:39:49.5680873Z"`, `"created_at":null`, 1),
		"object":          strings.Replace(string(raw), `"created_at":"2026-10-22T03:39:49.5680873Z"`, `"created_at":{}`, 1),
		"number":          strings.Replace(string(raw), `"created_at":"2026-10-22T03:39:49.5680873Z"`, `"created_at":123`, 1),
		"invalid_time":    strings.Replace(string(raw), "T03:39:49", "T25:39:49", 1),
		"missing_zone":    strings.Replace(string(raw), ".5680873Z", ".5680873", 1),
		"duplicate":       strings.TrimSuffix(string(raw), "}") + `,"created_at":"2026-10-22T03:39:49Z"}`,
		"unknown":         strings.TrimSuffix(string(raw), "}") + `,"extra":false}`,
		"case":            strings.Replace(string(raw), "created_at", "Created_at", 1),
		"missing":         strings.Replace(string(raw), `"created_at":"2026-10-22T03:39:49.5680873Z",`, "", 1),
		"nested_null":     strings.Replace(string(raw), `"observed":["2026-10-22T03:39:49.5680873Z"]`, `"observed":[null]`, 1),
		"trailing":        string(raw) + ` {}`,
		"invalid_unicode": strings.Replace(string(raw), ".5680873Z", `\ud800`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if changed == string(raw) || DecodeStrict([]byte(changed), new(timestampDocument)) == nil {
				t.Fatal("invalid timestamp document accepted or fixture unchanged")
			}
		})
	}
}
