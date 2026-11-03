package mysqloperator

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateIntentDurabilityExclusiveAndStrictReadback(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(parent, "intent.json")
	type document struct {
		Format string `json:"format"`
	}
	raw := []byte("{\"format\":\"synthetic.v1\"}\n")
	var out document
	if e := WriteDocument(path, raw, &out); e != nil || out.Format != "synthetic.v1" {
		t.Fatal("exclusive durable readback", e)
	}
	if e := WriteDocument(path, []byte("replacement"), &out); e == nil {
		t.Fatal("overwrote recovery evidence")
	}
	actual, e := ReadDocument(path, MaximumIntentBytes, &out)
	if e != nil || !bytes.Equal(actual, raw) {
		t.Fatal("original intent changed", e)
	}
	for _, invalid := range [][]byte{[]byte(`{"format":"one","format":"two"}`), []byte(`{"format":"one","foreign":true}`), []byte(`{"format":null}`), append(raw, []byte(`{}`)...)} {
		if e = os.WriteFile(path, invalid, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = ReadDocument(path, MaximumIntentBytes, &out); e == nil {
			t.Fatal("invalid private document accepted")
		}
	}
}

func TestPrivateIntentRefusesLinksModesAndNoncanonicalParents(t *testing.T) {
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(parent, "secret")
	if e := os.WriteFile(path, []byte("synthetic"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, mode := range []os.FileMode{0640, 0644, 0400} {
		if e := os.Chmod(path, mode); e != nil {
			t.Fatal(e)
		}
		if _, e := ReadPrivateBytes(path, 64); e == nil {
			t.Fatal("unsafe mode accepted", mode)
		}
	}
	if e := os.Chmod(path, 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(parent, "linked")
	if e := os.Link(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadPrivateBytes(path, 64); e == nil {
		t.Fatal("multiply linked secret accepted")
	}
	if e := os.Remove(link); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadPrivateBytes(link, 64); e == nil {
		t.Fatal("symlink accepted")
	}
	if e := os.Chmod(parent, 0755); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadPrivateBytes(path, 64); e == nil {
		t.Fatal("shared parent accepted")
	}
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	for _, maximum := range []int64{0, 1, maximumPrivateBytes + 1} {
		if _, e := ReadPrivateBytes(path, maximum); e == nil {
			t.Fatal("unbounded/truncated secret accepted", maximum)
		}
	}
}

func TestMySQLTargetRejectsMissingOrForeignIdentity(t *testing.T) {
	base := Target{Format: "projectone.mysql.operator-target.v1", ServerUUID: "11111111-1111-1111-1111-111111111111", InstallationID: "22222222-2222-2222-2222-222222222222", Database: "synthetic_test", Principal: "operator@localhost", Version: "8.4.8"}
	if !base.valid() {
		t.Fatal("declared target refused")
	}
	for _, change := range []func(*Target){func(v *Target) { v.Format = "postgresql" }, func(v *Target) { v.ServerUUID = "" }, func(v *Target) { v.InstallationID = "00000000-0000-0000-0000-000000000000" }, func(v *Target) { v.Principal = "operator" }, func(v *Target) { v.Database = "" }, func(v *Target) { v.Version = "" }} {
		v := base
		change(&v)
		if v.valid() {
			t.Fatal("invalid target shape accepted")
		}
	}
	var absent *Context
	if absent.Verify(t.Context()) == nil {
		t.Fatal("nil proof accepted")
	}
}
