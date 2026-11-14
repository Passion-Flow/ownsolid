package monitoring

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProtectedConfigNondefaultFalseAndStrictJSON(t *testing.T) {
	c := Defaults()
	c.OperationSeconds = 7
	c.LookbackSeconds = 23
	c.MaximumGatewayRows = 17
	c.NodePageSize = 3
	c.ConcurrentSnapshots = 1
	c.HTTP.StructuredLogging = false
	c.HTTP.LatencyMilliseconds = []float64{2, 12, 75}
	raw, _ := json.Marshal(c)
	path := filepath.Join(t.TempDir(), "monitoring.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	got, e := LoadEnvironment(map[string]string{ConfigEnvironment: path})
	if e != nil || got.LookbackSeconds != 23 || got.HTTP.StructuredLogging || len(got.HTTP.LatencyMilliseconds) != 3 {
		t.Fatal(got, e)
	}
	for _, bad := range []string{`{"format_version":1,"unknown":1}`, `{"format_version":1,"format_version":1}`, `{"http":{"structured_logging":"false"}}`, `{"operation_seconds":0}`, `{"maximum_gateway_rows":10001}`, `{"http":{"latency_milliseconds":[2,2]}}`, `null`, `{"http":null}`, `{"operation_seconds":null}`, `{"http":{"structured_logging":null}}`, `{"http":{"latency_milliseconds":null}}`, `{"http":{"maximum_logs_per_second":null}}`} {
		if e = os.WriteFile(path, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = Load(path); e == nil {
			t.Fatal("accepted invalid", bad)
		}
	}
	if _, e = LoadEnvironment(map[string]string{ConfigEnvironment: path, "UNKNOWN": "false"}); e == nil {
		t.Fatal("unknown environment")
	}
}
func TestCanonicalMicrosecondCursorAndValidation(t *testing.T) {
	at := time.Date(2026, 10, 22, 1, 2, 3, 123456000, time.UTC)
	id := "b8450d3f-c9a8-410e-a10a-a024e311ab98"
	raw := encodeCursor(at, id)
	got, gotID, e := decodeCursor(*raw)
	if e != nil || !got.Equal(at) || gotID != id {
		t.Fatal(got, gotID, e)
	}
	for _, bad := range []string{*raw + "=", base64.RawURLEncoding.EncodeToString([]byte(`{"at":"2026-10-22T01:02:03+00:00","id":"` + id + `"}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"at":"2026-10-22T01:02:03.123456789Z","id":"` + id + `"}`))} {
		if _, _, e := decodeCursor(bad); e == nil {
			t.Fatal("noncanonical accepted")
		}
	}
}
