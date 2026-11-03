package mysqlbackup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func TestNativeVersionParserRequiresExactOfficialToolVersionShape(t *testing.T) {
	for _, text := range []string{"mysql  Ver 8.4.8 for Linux on aarch64 (MySQL Community Server - GPL)", "/private/native/mysql  Ver 8.4.8 for macos15.0 on arm64 (Homebrew)"} {
		if !nativeVersionValid(text, "mysql", "8.4.8") {
			t.Fatal("actual supported version shape rejected")
		}
	}
	for _, text := range []string{"mysql  Ver 8.4.7 for Linux on aarch64 (GPL)", "mysqldump  Ver 8.4.8 for Linux on aarch64 (GPL)", "mysql  Ver 8.4.8-debug for Linux on aarch64 (GPL)", "mysql  Ver 8.4.8 for Linux on aarch64 (GPL)\nforeign", "mysql  Ver 8.4.8"} {
		if nativeVersionValid(text, "mysql", "8.4.8") {
			t.Fatal("foreign tool/version output accepted")
		}
	}
}

func TestNativeOptionsKeepSecretsQuotedInPrivateFileAndBindActualSession(t *testing.T) {
	c := mysqlstore.Configuration{Host: "localhost", ServerName: "localhost", Port: 3306, Database: "synthetic_test", User: "synthetic_operator", PasswordFile: "/private/synthetic/password", CAFile: "/private/synthetic/ca.pem", ExpectedVersion: "8.4.8", Policy: mysqlstore.DefaultPolicy()}
	target := ConnectionTarget{ServerUUID: "8f33cc41-789e-418d-a12b-d7698609eafa", Database: c.Database, Principal: "synthetic_operator@localhost", Version: c.ExpectedVersion}
	initial, e := initCommand(target, 147)
	if e != nil {
		t.Fatal(e)
	}
	for _, expected := range []string{"@@server_uuid='" + target.ServerUUID + "'", "CURRENT_USER()='" + target.Principal + "'", "IS_USED_LOCK(", "=147", "MYSQL_NATIVE_TARGET_MISMATCH"} {
		if !strings.Contains(initial, expected) {
			t.Fatal("native connection not bound to real installation target")
		}
	}
	raw, e := optionDocument(c, "synthetic #\"\\\t secret", "/private/synthetic/copied-ca.pem", initial)
	if e != nil {
		t.Fatal(e)
	}
	for _, expected := range []string{"protocol=TCP\n", "ssl-mode=VERIFY_IDENTITY\n", "tls-version=TLSv1.2,TLSv1.3\n", "password=\"synthetic #\\\"\\\\\\t secret\"\n", "init-command="} {
		if !strings.Contains(string(raw), expected) {
			t.Fatal("private native option escaping or TLS contract lost")
		}
	}
	for _, bad := range []string{"bad\n!include /private/foreign", "bad\x00secret", "bad\rsecret", string([]byte{255})} {
		if _, e := optionDocument(c, bad, c.CAFile, initial); e == nil {
			t.Fatal("unsafe option content accepted")
		}
	}
	c.ServerName = "foreign.example.invalid"
	if _, e := optionDocument(c, "synthetic secret", c.CAFile, initial); e == nil {
		t.Fatal("native identity verification target silently rewritten")
	}
	target.Database = "db'; DROP DATABASE db"
	if _, e := initCommand(target, 147); e == nil {
		t.Fatal("unsafe target inserted into native initialization statement")
	}
}

func TestBackupStreamingBoundsCancellationAndDistinctRows(t *testing.T) {
	input := []byte("Synthetic UTF8 \u4e16\u754c binary\x00payload")
	var output bytes.Buffer
	sha, n, e := hashStream(t.Context(), &output, bytes.NewReader(input), int64(len(input)))
	if e != nil || n != int64(len(input)) || !bytes.Equal(output.Bytes(), input) || len(sha) != 64 {
		t.Fatal("actual stream changed")
	}
	if sha, n, e := hashStream(t.Context(), nil, bytes.NewReader(input), int64(len(input)-1)); e == nil || sha != "" || n != 0 {
		t.Fatal("oversize stream projected success")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if sha, n, e := hashStream(ctx, nil, bytes.NewReader(input), int64(len(input))); e == nil || sha != "" || n != 0 {
		t.Fatal("cancelled stream projected success")
	}
	first, _ := json.Marshal([]any{nil, []byte{}, []byte{0}, "\u4e16\u754c", int64(1)})
	second, _ := json.Marshal([]any{[]byte{}, nil, []byte{0}, "\u4e16\u754c", int64(1)})
	if bytes.Equal(first, second) {
		t.Fatal("NULL/empty or column order collapsed")
	}
	h := &boundedHash{hash: sha256.New(), remaining: 2}
	if _, e := h.Write([]byte("abc")); e == nil || h.remaining != 2 {
		t.Fatal("failed bounded write adopted partial success")
	}
}

func TestNativeExecutableHeaderRejectsForeignOrMalformedBinary(t *testing.T) {
	// The test executable proves only this OS/architecture parser; it is not a
	// MySQL binary, GPL provenance, native process or actual backup acceptance.
	path, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	file, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer file.Close()
	if e = executablePlatform(file); e != nil {
		t.Fatal("current test executable platform", e)
	}
	bad, e := os.CreateTemp(t.TempDir(), "not-native")
	if e != nil {
		t.Fatal(e)
	}
	defer bad.Close()
	if _, e = bad.Write([]byte("#!/bin/sh\nsynthetic refusal\n")); e != nil {
		t.Fatal(e)
	}
	if executablePlatform(bad) == nil {
		t.Fatal("script accepted as native pinned executable")
	}
}
