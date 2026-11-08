package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func privateDirectory(t *testing.T) string {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	return dir
}

func TestCertificateStateRoundTrips(t *testing.T) {
	dir := privateDirectory(t)
	if _, found, e := LoadCertificate(dir); e != nil || found {
		t.Fatal(found, e)
	}
	want := Certificate{PrivateKey: "key", Certificate: "cert", PendingKey: "next", Confirmed: true, RenewAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}
	if e := SaveCertificate(dir, want); e != nil {
		t.Fatal(e)
	}
	got, found, e := LoadCertificate(dir)
	if e != nil || !found || got != want {
		t.Fatal(got, found, e)
	}
	info, e := os.Stat(filepath.Join(dir, certificateFile))
	if e != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info.Mode(), e)
	}
}

func TestCertificateStateRefusesBadContent(t *testing.T) {
	dir := privateDirectory(t)
	if e := SaveCertificate(dir, Certificate{}); e == nil {
		t.Fatal("a state without a key was saved")
	}
	for name, content := range map[string]string{"not json": "garbage", "unknown field": `{"private_key":"k","other":1}`, "no key": `{"certificate":"c"}`, "two values": `{"private_key":"k"} {}`} {
		if e := os.WriteFile(filepath.Join(dir, certificateFile), []byte(content), 0o600); e != nil {
			t.Fatal(e)
		}
		if _, _, e := LoadCertificate(dir); e == nil || e.Error() != "CERTIFICATE_STATE_INVALID" {
			t.Errorf("%s: %v", name, e)
		}
	}
	if e := os.Chmod(filepath.Join(dir, certificateFile), 0o644); e != nil {
		t.Fatal(e)
	}
	if _, _, e := LoadCertificate(dir); e == nil {
		t.Error("a key readable by others was accepted")
	}
}
