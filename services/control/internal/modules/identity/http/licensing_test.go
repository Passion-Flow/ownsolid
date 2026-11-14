package http

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

func TestM19RecoveryRouteAndBodyInterpretation(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	component := "/api/v1/admin/nodes/" + id + "/components/node-exporter/actions"
	for _, tc := range []struct {
		body    string
		safe    bool
		invalid bool
	}{
		{`{"action":"stop","reason":"Synthetic"}`, true, false},
		{`{"Action":"remove","reason":"Synthetic"}`, true, false},
		{`{"action":"stop","Action":"start","reason":"Synthetic"}`, false, false},
		{`{"action":"start","Action":"stop","reason":"Synthetic"}`, true, false},
		{`{"action":"start","reason":"Synthetic"}`, false, false},
		{`{"action":"stop","action":"start","reason":"Synthetic"}`, false, true},
		{`{"action":"stop","reason":null}`, false, true},
		{`{"action":"stop","reason":"Synthetic","unknown":true}`, false, true},
		{`{"action":"stop"} {}`, false, true},
	} {
		r := httptest.NewRequest("POST", component, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		safe, e := recoveryMutation(r)
		if tc.invalid {
			if e == nil {
				t.Fatal("invalid body accepted", tc.body)
			}
			continue
		}
		if e != nil || safe != tc.safe {
			t.Fatal("gate interpretation", tc.body, safe, e)
		}
		var actual components.Request
		if e = httpserver.DecodeJSON(r, &actual); e != nil || (actual.Action == "stop" || actual.Action == "remove") != safe {
			t.Fatal("handler interpreted different action", actual, e)
		}
	}
	for _, path := range []string{component + "/", component + "/stop", strings.Replace(component, id, "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", 1), "/api/v1/future/" + id + "/cancellations", "/api/v1/admin/license/anything"} {
		r := httptest.NewRequest("POST", path, bytes.NewReader(nil))
		if safe, e := recoveryMutation(r); safe || e != nil {
			t.Fatal("unknown route exempt", path, e)
		}
	}
	r := httptest.NewRequest("POST", component, strings.NewReader(strings.Repeat("x", httpserver.MaxJSONBytes+1)))
	r.Header.Set("Content-Type", "application/json")
	if _, e := recoveryMutation(r); e == nil || e.Error() != "PAYLOAD_TOO_LARGE" {
		t.Fatal("unbounded body", e)
	}
	r = httptest.NewRequest("POST", component, strings.NewReader(`{"action":"stop","reason":"Synthetic"}`))
	r.Header.Set("Content-Type", "application/json")
	r.URL.RawPath = r.URL.Path
	if safe, e := recoveryMutation(r); safe || e != nil {
		t.Fatal("noncanonical raw path exempt", e)
	}
	r = httptest.NewRequest("POST", component, strings.NewReader(`{"action":"stop","reason":"Synthetic"}`))
	r.Header.Set("Content-Type", "application/json")
	if _, e := recoveryMutation(r); e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(r.Body)
	if !bytes.Contains(raw, []byte(`"action":"stop"`)) {
		t.Fatal("body not restored")
	}
}
