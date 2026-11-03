package configuration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRestoreRequestStrictSourceReasonAndExplicitEnvironment(t *testing.T) {
	r := RestoreRequest{RestoreSourceRequest: RestoreSourceDefaults(), Reason: "Explicit isolated restore"}
	r.BackupRoot = "/synthetic/external/backup"
	r.ManifestSHA256 = strings.Repeat("a", 64)
	raw, _ := json.Marshal(r)
	v, e := LoadRestoreRequest(raw, []byte("PROJECT_ONE_OFFLINE_RESTORE_REASON=Independent isolated recovery\nPROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_BYTES=1048576\n"))
	if e != nil || v.Reason != "Independent isolated recovery" || v.MaximumBytes != 1<<20 || v.MaximumEntries != 100000 {
		t.Fatal(v, e)
	}
	for _, env := range []string{"PROJECT_ONE_OFFLINE_RESTORE_REASON=\n", "PROJECT_ONE_OFFLINE_RESTORE_REASON=x\nPROJECT_ONE_OFFLINE_RESTORE_REASON=y\n", "PROJECT_ONE_OFFLINE_RESTORE_REASON=$HOME\n", "PROJECT_ONE_OFFLINE_RESTORE_REASON=`command`\n", "PROJECT_ONE_OFFLINE_RESTORE_SOURCE_MAXIMUM_BYTES=false\n", "PROJECT_ONE_OFFLINE_RESTORE_EXTRA=true\n"} {
		if _, e := LoadRestoreRequest(raw, []byte(env)); e == nil {
			t.Fatal("invalid explicit restore environment accepted")
		}
	}
	for _, input := range []string{`{"reason":null}`, `{"reason":"a","reason":"b"}`, `{"unknown":true}`} {
		if _, e := LoadRestoreRequest([]byte(input), nil); e == nil {
			t.Fatal("unsafe restore request accepted")
		}
	}
	for _, reason := range []string{"", "  ", "bad\nreason", strings.Repeat("x", 501)} {
		r.Reason = reason
		if r.Validate() == nil {
			t.Fatal("invalid restore reason")
		}
	}
}
