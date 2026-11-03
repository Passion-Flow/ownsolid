package bootstrap

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestMySQLIntentRetainsExactRequestCredentialAndTarget(t *testing.T) {
	r := request()
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	i := MySQLIntent{Format: mysqlIntentFormat, Request: r, RequestSHA256: digest(raw), TargetSHA256: digest([]byte("synthetic actual target")), Password: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, passwordBytes))}
	if e = i.validate(r, raw, i.TargetSHA256); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*MySQLIntent){func(v *MySQLIntent) { v.Format = "projectone.postgresql.bootstrap-intent.v1" }, func(v *MySQLIntent) { v.Request.AdministratorEmail = "other@example.invalid" }, func(v *MySQLIntent) { v.RequestSHA256 = digest(append(raw, '\n')) }, func(v *MySQLIntent) { v.TargetSHA256 = digest([]byte("another actual installation")) }, func(v *MySQLIntent) { v.Password = "not-base64" }, func(v *MySQLIntent) { v.Password = base64.RawURLEncoding.EncodeToString([]byte{7}) }} {
		v := i
		change(&v)
		if v.validate(r, raw, i.TargetSHA256) == nil {
			t.Fatal("foreign/changed retained intent accepted")
		}
	}
	if _, e := RunMySQL(t.Context(), nil, nil, "bootstrap-admin-file", "", " "); e == nil {
		t.Fatal("missing actual owner accepted")
	}
	if status, e := RunMySQL(t.Context(), nil, nil, "invalid", "", ""); e == nil || status != "" {
		t.Fatal("invalid operator exposed status")
	}
}
