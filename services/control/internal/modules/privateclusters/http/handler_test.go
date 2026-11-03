package http

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrivateClusterMutationRejectsAmbiguousEnvelope(t *testing.T) {
	for _, v := range []struct {
		body    string
		binding bool
	}{
		{`{"reason":"audit"}`, false},
		{`{"Reason":"audit","expected_revision":1}`, false},
		{`{"reason":"audit","expected_revision":1,"Reason":"other"}`, false},
		{`{"reason":"audit","reason":"other","expected_revision":1}`, false},
		{`{"reason":"audit","expected_revision":null}`, false},
		{`{"reason":"audit","expected_revision":1,"profile_id":"other"}`, false},
		{`{"reason":"audit","expected_revision":0}`, true},
		{`{"profile_id":"one","reason":"audit","expected_revision":"0"}`, true},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(v.body))
		r.Header.Set("Content-Type", "application/json")
		if _, e := decodeMutation(r, v.binding); e == nil {
			t.Fatalf("ambiguous body accepted: %s", v.body)
		}
	}
	for _, v := range []struct {
		body     string
		binding  bool
		revision int64
	}{
		{`{"reason":"audit","expected_revision":1}`, false, 1},
		{`{"profile_id":"one","reason":"audit","expected_revision":0}`, true, 0},
	} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(v.body))
		r.Header.Set("Content-Type", "application/json")
		got, e := decodeMutation(r, v.binding)
		if e != nil || got.Reason != "audit" || got.ExpectedRevision != v.revision {
			t.Fatalf("exact envelope refused: %v", e)
		}
	}
}
