package middleware

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLObservationRequiresActualTypedFacts(t *testing.T) {
	o := Observation{ObservedAt: time.Now().UTC(), Database: "synthetic_test", User: "readonly", ServerAddress: "127.0.0.1", ServerPort: 5432, VersionNumber: 180006, TLSVersion: "TLSv1.3", TLSCipher: "TLS_AES_256_GCM_SHA384"}
	raw, _ := json.Marshal(o)
	var decoded Observation
	if json.Unmarshal(raw, &decoded) != nil || decoded != o {
		t.Fatal("explicit false and zero observations must roundtrip")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		t.Fatal("test JSON required")
	}
	for name := range fields {
		t.Run("missing_"+name, func(t *testing.T) {
			var bad map[string]json.RawMessage
			_ = json.Unmarshal(raw, &bad)
			delete(bad, name)
			value, _ := json.Marshal(bad)
			decoded := o
			if json.Unmarshal(value, &decoded) == nil || decoded != (Observation{}) {
				t.Fatal("missing fact accepted or stale successful projection retained")
			}
		})
	}
	for _, change := range []struct{ old, new string }{
		{`"in_recovery":false`, `"in_recovery":"false"`},
		{`"superuser":false`, `"superuser":null`},
		{`"table_count":0`, `"table_count":-1`},
		{`"table_count":0`, `"table_count":9007199254740992`},
		{`"server_port":5432`, `"server_port":0`},
		{`"version_number":180006`, `"version_number":190000`},
		{`"server_address":"127.0.0.1"`, `"server_address":"not-an-ip"`},
		{`"tls_version":"TLSv1.3"`, `"tls_version":"plaintext"`},
		{`"tls_cipher":"TLS_AES_256_GCM_SHA384"`, `"tls_cipher":""`},
		{`"write_any_table":false`, `"write_any_table":false,"password":"private"`},
		{`"write_any_table":false`, `"write_any_table":false,"write_any_table":true`},
	} {
		decoded = o
		if json.Unmarshal([]byte(strings.Replace(string(raw), change.old, change.new, 1)), &decoded) == nil || decoded != (Observation{}) {
			t.Fatal("invalid fact accepted", change.old)
		}
	}
	for _, tenant := range []string{"11111111-1111-1111-1111-111111111111", ""} {
		value := strings.TrimSuffix(string(raw), "}") + `,"platform_tenant_id":"` + tenant + `"}`
		decoded = Observation{}
		e := json.Unmarshal([]byte(value), &decoded)
		if (e == nil) != (tenant != "") {
			t.Fatal("invalid optional platform identity accepted")
		}
	}
}

func TestPostgreSQLProbeNilContextReturnsZeroObservation(t *testing.T) {
	if out, e := Probe(nil, &CapturedInputs{}, DefaultPolicy()); e == nil || out != (Observation{}) {
		t.Fatal("invalid probe returned successful observation")
	}
}

func TestPostgreSQLObservationRetainsOriginalInetHostMask(t *testing.T) {
	o := Observation{ObservedAt: time.Now().UTC(), Database: "synthetic_test", User: "readonly", ServerPort: 5432, VersionNumber: 180006, TLSVersion: "TLSv1.3", TLSCipher: "TLS_AES_256_GCM_SHA384"}
	for _, address := range []string{"127.0.0.1", "127.0.0.1/32", "172.17.0.2/32", "::1", "::1/128", "::ffff:127.0.0.1/128"} {
		o.ServerAddress = address
		raw, _ := json.Marshal(o)
		var decoded Observation
		if e := json.Unmarshal(raw, &decoded); e != nil || decoded.ServerAddress != address {
			t.Fatal("original PostgreSQL host-address representation rejected or rewritten", address, e)
		}
	}
	for _, address := range []string{"127.0.0.1/24", "::1/64", "127.0.0.1/128", "::1/32", "[::1]", "::1%lo0", "postgres.example.invalid", "127.0.0.1/32\n"} {
		o.ServerAddress = address
		raw, _ := json.Marshal(o)
		decoded := o
		if json.Unmarshal(raw, &decoded) == nil || decoded != (Observation{}) {
			t.Fatal("non-host address accepted", address)
		}
	}
}
