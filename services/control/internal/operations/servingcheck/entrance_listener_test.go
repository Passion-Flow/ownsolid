package servingcheck

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestPortalListenerExpectedIntentPreservesOmissionAndExactBinding(t *testing.T) {
	c := syntheticServingConfiguration(t, config.TransportHTTP)
	legacy := syntheticExpected(c)
	if err := Verify(c, legacy); err != nil {
		t.Fatal(err)
	}
	c.Server.PortalListenAddress = "127.0.0.1:29443"
	servingCheckFailure(t, Verify(c, legacy), transportMismatch)
	expected := legacy
	expected.PortalListenAddress = c.Server.PortalListenAddress
	raw := syntheticExpectedBytes(t, expected)
	parsed, err := ParseExpected(raw)
	if err != nil || !reflect.DeepEqual(parsed, expected) {
		t.Fatal("explicit split intent changed", err)
	}
	if err = Verify(c, parsed); err != nil {
		t.Fatal("exact split intent refused", err)
	}
	c.Server.PortalListenAddress = "127.0.0.1:30443"
	servingCheckFailure(t, Verify(c, parsed), transportMismatch)
	c.Server.PortalListenAddress = ""
	servingCheckFailure(t, Verify(c, parsed), transportMismatch)
	for _, value := range []any{nil, "", false, []string{}, "127.0.0.1:0", legacy.ListenAddress} {
		var fields map[string]any
		if json.Unmarshal(syntheticExpectedBytes(t, legacy), &fields) != nil {
			t.Fatal("fixture intent")
		}
		fields["portal_listen_address"] = value
		invalid, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseExpected(invalid)
		servingCheckFailure(t, err, expectationInvalid)
	}
	duplicated := bytes.Replace(raw, []byte(`"portal_listen_address":"127.0.0.1:29443"`), []byte(`"portal_listen_address":"127.0.0.1:29443","portal_listen_address":"127.0.0.1:29443"`), 1)
	_, err = ParseExpected(duplicated)
	servingCheckFailure(t, err, expectationInvalid)
}
