package http

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
)

func TestPluginRequestDecoderPreservesExplicitFalseAndRejectsUntrustedFields(t *testing.T) {
	body := "{\"enabled\":false,\"expected_revision\":1,\"reason\":\"Caf\u00e9 synthetic revocation\"}"
	var input plugins.GrantUpdate
	decodeBody := func(raw, media string, maximum int64) error {
		r := httptest.NewRequest("PATCH", "/", strings.NewReader(raw))
		r.Header.Set("Content-Type", media)
		return decode(r, &input, maximum)
	}
	if e := decodeBody(body, "application/json", 65536); e != nil || input.Enabled || input.ExpectedRevision != 1 {
		t.Fatal("explicit false", e)
	}
	for _, raw := range []string{
		strings.Replace(body, `"enabled":false,`, "", 1),
		strings.Replace(body, `"enabled":false`, `"enabled":null`, 1),
		strings.Replace(body, `"enabled":false`, `"Enabled":false`, 1),
		strings.Replace(body, `"enabled":false`, `"enabled":false,"enabled":true`, 1),
		strings.Replace(body, `"enabled":false`, `"enabled":"false"`, 1),
		strings.Replace(body, `"expected_revision":1`, `"expected_revision":1.5`, 1),
		strings.Replace(body, `"reason":`, `"private_key":`, 1),
		body + "{}", `null`, `[]`,
	} {
		if e := decodeBody(raw, "application/json", 65536); e == nil {
			t.Fatal("unsafe plugin request accepted", raw)
		}
	}
	if e := decodeBody(body, "text/plain", 65536); e == nil || e.Error() != "UNSUPPORTED_MEDIA_TYPE" {
		t.Fatal(e)
	}
	if e := decodeBody(body, "application/json", 2); e == nil || e.Error() != "PAYLOAD_TOO_LARGE" {
		t.Fatal(e)
	}
	for _, values := range [][]string{{}, {""}, {"not-a-uuid"}, {uuid.NewString(), uuid.NewString()}} {
		r := httptest.NewRequest("POST", "/", nil)
		for _, value := range values {
			r.Header.Add("Idempotency-Key", value)
		}
		if _, e := intent(r); e == nil {
			t.Fatal("invalid mutation intent accepted")
		}
	}
	r := httptest.NewRequest("POST", "/", nil)
	key := uuid.NewString()
	r.Header.Set("Idempotency-Key", key)
	if got, e := intent(r); e != nil || got != key {
		t.Fatal("explicit UUID intent", e)
	}
}
