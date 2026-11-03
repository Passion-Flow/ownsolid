package components

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBoundedJournalPreservesSourceAndSanitizesHostileText(t *testing.T) {
	now := time.Now().UTC()
	since := now.Add(-time.Minute)
	var body bytes.Buffer
	for i := 0; i < 8; i++ {
		entry := map[string]any{"__REALTIME_TIMESTAMP": strconv.FormatInt(now.UnixMicro(), 10), "PRIORITY": "6", "MESSAGE": "<script>untrusted</script>\n\u202e" + strings.Repeat("<", 600)}
		encoded, _ := json.Marshal(entry)
		body.Write(encoded)
		body.WriteByte('\n')
	}
	logs, e := parseJournal(body.Bytes(), since, now)
	if e != nil || len(logs.Entries) > 8 || !logs.Available || !logs.Truncated {
		t.Fatal("bounded synthetic journal rejected", e)
	}
	encoded, _ := json.Marshal(logs)
	if len(encoded) > 12288 {
		t.Fatal("escaped logs exceeded protocol bound")
	}
	if !logs.Entries[0].Sanitized || strings.ContainsAny(logs.Entries[0].Message, "\n\u202e") {
		t.Fatal("unsafe display controls survived")
	}
	if !strings.Contains(logs.Entries[0].Message, "<script>") {
		t.Fatal("log text unexpectedly executed or replaced")
	}
}
func TestJournalInvalidAndEmptyOutputsRemainDistinct(t *testing.T) {
	now := time.Now().UTC()
	since := now.Add(-time.Minute)
	logs, e := parseJournal(nil, since, now)
	if e != nil || !logs.Available || logs.Entries == nil || len(logs.Entries) != 0 {
		t.Fatal("available empty journal not represented", e)
	}
	for _, body := range [][]byte{[]byte(`{"MESSAGE":[1,2],"PRIORITY":"6","__REALTIME_TIMESTAMP":"1"}`), []byte(`{"MESSAGE":"x","PRIORITY":"9","__REALTIME_TIMESTAMP":"1"}`), []byte(`{"MESSAGE":"x","PRIORITY":"6","__REALTIME_TIMESTAMP":"99999999999999999999999"}`), bytes.Repeat([]byte("x"), 65537)} {
		if _, e = parseJournal(body, since, now); e == nil {
			t.Fatal("invalid journal fabricated a source")
		}
	}
}
