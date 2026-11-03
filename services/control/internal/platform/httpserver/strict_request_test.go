package httpserver

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequiredObjectDecoderSeparatesEmptyNullUnknownDuplicateAndExplicitValues(t *testing.T) {
	type request struct {
		Theme            string `json:"theme"`
		ExpectedRevision int64  `json:"expected_revision"`
	}
	decode := func(raw []byte, media string, maximum int64, value any) error {
		r := httptest.NewRequest("PATCH", "/api/v1/preferences", bytes.NewReader(raw))
		r.Header.Set("Content-Type", media)
		return DecodeRequiredObject(r, value, maximum)
	}
	var empty struct{}
	if e := decode([]byte(`{}`), "application/json", 1024, &empty); e != nil {
		t.Fatal("explicit empty mutation object", e)
	}
	var value request
	if e := decode([]byte(`{"theme":"dark","expected_revision":1}`), "application/json; charset=utf-8", 1024, &value); e != nil || value.Theme != "dark" || value.ExpectedRevision != 1 {
		t.Fatal("exact required values", e)
	}
	for name, raw := range map[string]string{"null": "null", "array": "[]", "missing": `{"theme":"light"}`, "unknown": `{"theme":"light","expected_revision":1,"user_id":"other"}`, "case": `{"Theme":"light","expected_revision":1}`, "duplicate": `{"theme":"light","theme":"dark","expected_revision":1}`, "null_field": `{"theme":null,"expected_revision":1}`, "fractional_revision": `{"theme":"light","expected_revision":1.5}`, "trailing": `{"theme":"light","expected_revision":1}{}`, "empty": `{}`} {
		t.Run(name, func(t *testing.T) {
			var value request
			if e := decode([]byte(raw), "application/json", 1024, &value); e == nil {
				t.Fatal("ambiguous object accepted")
			}
		})
	}
	for _, raw := range []string{"null", "[]", `{"recipient_id":"other"}`} {
		if e := decode([]byte(raw), "application/json", 1024, &empty); e == nil {
			t.Fatal("empty-object route accepted untrusted input")
		}
	}
	if e := decode([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}, "application/json", 1024, &empty); e == nil {
		t.Fatal("invalid Unicode accepted")
	}
	if e := decode([]byte(strings.Repeat(" ", 1025)), "application/json", 1024, &empty); e == nil || e.Error() != "PAYLOAD_TOO_LARGE" {
		t.Fatal("body limit", e)
	}
	if e := decode([]byte(`{}`), "text/plain", 1024, &empty); e == nil || e.Error() != "UNSUPPORTED_MEDIA_TYPE" {
		t.Fatal("media contract", e)
	}
}
