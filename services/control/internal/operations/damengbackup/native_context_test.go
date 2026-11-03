package damengbackup

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNativeContextExplicitReplacementAndClosedPolicy(t *testing.T) {
	root := t.TempDir()
	base := NativeContextConfiguration{FormatVersion: 1, ConnectorConfigurationFile: "/opt/projectone/owner.json", SchemaConfigurationFile: "/opt/projectone/schema.json", Snapshot: rowTestPolicy()}
	replacement := base
	replacement.Snapshot.PageRows = 31
	replacement.Snapshot.LOBChunkSize = 4096
	a, b, env := filepath.Join(root, "base.json"), filepath.Join(root, "replacement.json"), filepath.Join(root, "operator.env")
	writeTest(t, a, jsonBytes(base))
	writeTest(t, b, jsonBytes(replacement))
	t.Setenv(NativeContextEnvironment, b)
	got, e := LoadNativeContext(a, "")
	if e != nil || !reflect.DeepEqual(got, base) {
		t.Fatal("ambient environment changed base", e)
	}
	writeTest(t, env, []byte("# explicit operator input\n"+NativeContextEnvironment+"="+b+"\n"))
	got, e = LoadNativeContext(a, env)
	if e != nil || !reflect.DeepEqual(got, replacement) {
		t.Fatal("nondefault whole-file replacement lost", e)
	}
	for _, raw := range []string{
		strings.Replace(string(jsonBytes(base)), `"page_rows":2`, `"page_rows":false`, 1),
		strings.Replace(string(jsonBytes(base)), `"lob_chunk_size":256`, `"lob_chunk_size":8192`, 1),
		strings.Replace(string(jsonBytes(base)), `"format_version":1`, `"format_version":1,"format_version":1`, 1),
		strings.Replace(string(jsonBytes(base)), `"snapshot":{`, `"snapshot":{"unknown":1,`, 1),
		strings.Replace(string(jsonBytes(base)), `"connector_configuration_file":"/opt/projectone/owner.json"`, `"connector_configuration_file":null`, 1),
	} {
		writeTest(t, a, []byte(raw))
		if _, e = LoadNativeContext(a, ""); e == nil {
			t.Fatal("invalid closed policy admitted", raw)
		}
	}
	writeTest(t, a, jsonBytes(base))
	for _, raw := range []string{NativeContextEnvironment + "=false", NativeContextEnvironment + "=", "UNKNOWN=" + b, NativeContextEnvironment + "=" + b + "\n" + NativeContextEnvironment + "=" + b} {
		writeTest(t, env, []byte(raw))
		if _, e = LoadNativeContext(a, env); e == nil {
			t.Fatal("invalid env input admitted")
		}
	}
	if e = os.Chmod(a, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = LoadNativeContext(a, ""); e == nil {
		t.Fatal("public context admitted")
	}
}
