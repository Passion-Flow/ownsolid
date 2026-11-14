package damengbackup

import (
	"github.com/google/uuid"
	"path/filepath"
	"strings"
	"testing"
)

func testOperatorRequest() OperatorRequest {
	return OperatorRequest{1, uuid.NewString(), "backup", "Synthetic original protected backup", "/private/config/platform.yaml", "", "/private/operator/recovery.json", "", "/private/operator/native.json", "", "/private/config", "/private/output/one", "", false, []ImageRootBinding{}}
}
func TestOperatorRequestClosedExplicitAndReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "request.json")
	r := testOperatorRequest()
	writeTest(t, path, jsonBytes(r))
	actual, e := LoadOperatorRequest(path, "")
	if e != nil || actual.OperationID != r.OperationID {
		t.Fatal(e)
	}
	for _, raw := range []string{
		strings.Replace(string(jsonBytes(r)), `"confirm_fresh_target":false`, `"confirm_fresh_target":"false"`, 1),
		strings.Replace(string(jsonBytes(r)), `"confirm_fresh_target":false,`, "", 1),
		strings.Replace(string(jsonBytes(r)), `"kind":"backup"`, `"kind":"restore"`, 1),
		strings.Replace(string(jsonBytes(r)), `"format_version":1`, `"format_version":1,"unknown":1`, 1),
		strings.Replace(string(jsonBytes(r)), `"image_roots":[]`, `"image_roots":null`, 1),
	} {
		writeTest(t, path, []byte(raw))
		if _, e = LoadOperatorRequest(path, ""); e == nil {
			t.Fatal("invalid original request admitted")
		}
	}
	writeTest(t, path, jsonBytes(r))
	replacement := r
	replacement.OperationID = uuid.NewString()
	next := filepath.Join(root, "next.json")
	writeTest(t, next, jsonBytes(replacement))
	env := filepath.Join(root, "operator.env")
	t.Setenv(OperatorRequestEnvironment, next)
	actual, e = LoadOperatorRequest(path, "")
	if e != nil || actual.OperationID != r.OperationID {
		t.Fatal("ambient replaced input")
	}
	writeTest(t, env, []byte(OperatorRequestEnvironment+"="+next+"\n"))
	actual, e = LoadOperatorRequest(path, env)
	if e != nil || actual.OperationID != replacement.OperationID {
		t.Fatal(e)
	}
	writeTest(t, env, []byte(OperatorRequestEnvironment+"="+next+"\n"+OperatorRequestEnvironment+"="+next))
	if _, e = LoadOperatorRequest(path, env); e == nil {
		t.Fatal("duplicate env admitted")
	}
}
func TestOperatorRequestRequiresFreshDistinctIntent(t *testing.T) {
	r := testOperatorRequest()
	r.Kind = "restore"
	r.SourceRoot = "/private/source/one"
	if r.Validate() == nil {
		t.Fatal("missing explicit fresh target admitted")
	}
	r.ConfirmFreshTarget = true
	if e := r.Validate(); e != nil {
		t.Fatal(e)
	}
	r.SourceRoot = r.OutputRoot
	if r.Validate() == nil {
		t.Fatal("source overwritten")
	}
}
