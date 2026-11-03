package configuration

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBackupRequestExplicitNondefaultMappingAndClosedBounds(t *testing.T) {
	raw := []byte(`{"format_version":1,"destination_root":"/synthetic/external/backup","reason":"Synthetic recovery"}`)
	r, e := LoadBackupRequest(raw, []byte("PROJECT_ONE_OFFLINE_BACKUP_MAXIMUM_BYTES=2097152\nPROJECT_ONE_OFFLINE_BACKUP_MAXIMUM_ENTRIES=47\nPROJECT_ONE_OFFLINE_BACKUP_STREAM_SECONDS=50\nPROJECT_ONE_OFFLINE_BACKUP_OBSERVE_SECONDS=70\nPROJECT_ONE_OFFLINE_BACKUP_POLL_MILLISECONDS=100\n"))
	if e != nil || r.MaximumBytes != 2097152 || r.MaximumEntries != 47 || r.StreamSeconds != 50 || r.ObserveSeconds != 70 || r.PollMilliseconds != 100 {
		t.Fatal(r, e)
	}
	for _, env := range []string{"UNKNOWN=secret_value", "PROJECT_ONE_OFFLINE_BACKUP_REASON=\n", "PROJECT_ONE_OFFLINE_BACKUP_MAXIMUM_BYTES=false", "PROJECT_ONE_OFFLINE_BACKUP_MAXIMUM_BYTES=1048575", "PROJECT_ONE_OFFLINE_BACKUP_MAXIMUM_ENTRIES=1000001", "PROJECT_ONE_OFFLINE_BACKUP_MAXIMUM_ENTRIES=9223372036854775807", "PROJECT_ONE_OFFLINE_BACKUP_POLL_MILLISECONDS=0", "PROJECT_ONE_OFFLINE_BACKUP_STREAM_SECONDS=7201", "PROJECT_ONE_OFFLINE_BACKUP_REASON=first\nPROJECT_ONE_OFFLINE_BACKUP_REASON=second", "PROJECT_ONE_OFFLINE_BACKUP_REASON=$(secret_value)"} {
		if _, e := LoadBackupRequest(raw, []byte(env)); e == nil || strings.Contains(e.Error(), "secret_value") {
			t.Fatal("bad environment or leaked input", e)
		}
	}
	for _, raw := range []string{`{"destination_root":"relative","reason":"x"}`, `{"destination_root":"/synthetic/../escape","reason":"x"}`, `{"destination_root":"/synthetic","reason":"x","unknown":"secret_value"}`, `{"destination_root":"/synthetic","reason":"x","reason":"other"}`} {
		if _, e := LoadBackupRequest([]byte(raw), nil); e == nil || strings.Contains(e.Error(), "secret_value") {
			t.Fatal("invalid request", e)
		}
	}
	encoded, _ := json.Marshal(r)
	again, e := LoadBackupRequest(encoded, nil)
	if e != nil || again != r {
		t.Fatal("roundtrip", e)
	}
	for _, v := range []BackupRequest{func() BackupRequest {
		x := r
		x.MaximumBytes = 1 << 40
		x.MaximumEntries = 1000000
		x.StreamSeconds = 7200
		x.ObserveSeconds = 7200
		x.PollMilliseconds = 5000
		return x
	}(), func() BackupRequest {
		x := r
		x.MaximumBytes = 1 << 20
		x.MaximumEntries = 4
		x.StreamSeconds = 5
		x.ObserveSeconds = 5
		x.PollMilliseconds = 50
		return x
	}()} {
		if e := v.Validate(); e != nil {
			t.Fatal("valid boundary refused", e)
		}
	}
}
