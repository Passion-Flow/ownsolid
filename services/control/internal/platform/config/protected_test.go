package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestProtectedConfigurationActualPrivateSnapshotAndUnsafeFileRefusal(t *testing.T) {
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private root")
	}
	var c config.Config
	c.Server.ListenAddress = "127.0.0.1:19443"
	c.Server.PortalOrigin = "https://portal.example.invalid"
	c.Server.AdminOrigin = "https://admin.example.invalid"
	c.Database.MaxOpenConnections = 4
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.SMTP.Host = "smtp.example.invalid"
	c.SMTP.Port = 587
	c.SMTP.FromAddress = "platform@example.invalid"
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 5
	c.SMTP.OperationTimeoutSeconds = 20
	c.Models.ArtifactRoot = filepath.Join(root, "nondefault-models")
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "control.yaml")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("private config")
	}
	actual, e := config.LoadProtected(path)
	if e != nil || actual.Models.ArtifactRoot != c.Models.ArtifactRoot {
		t.Fatal("typed nondefault private config", e)
	}
	for _, mode := range []os.FileMode{0400, 0640, 0644} {
		os.Chmod(path, mode)
		if _, e := config.LoadProtected(path); e == nil {
			t.Fatal("unsafe config permissions")
		}
	}
	os.Chmod(path, 0600)
	link := filepath.Join(root, "link.yaml")
	if os.Symlink(path, link) != nil {
		t.Fatal("link fixture")
	}
	if _, e := config.LoadProtected(link); e == nil {
		t.Fatal("config symlink")
	}
	os.Remove(link)
	if os.Link(path, link) != nil {
		t.Fatal("hardlink fixture")
	}
	if _, e := config.LoadProtected(path); e == nil {
		t.Fatal("config hardlink")
	}
	os.Remove(link)
	if syscall.Mkfifo(link, 0600) != nil {
		t.Fatal("fifo fixture")
	}
	if _, e := config.LoadProtected(link); e == nil {
		t.Fatal("config special file")
	}
	os.Remove(link)
	for _, bad := range [][]byte{nil, bytes.Repeat([]byte("x"), config.MaximumProtectedConfigurationBytes+1), []byte("SYNTHETIC_PRIVATE_CONFIGURATION: true\n")} {
		if os.WriteFile(path, bad, 0600) != nil {
			t.Fatal("invalid fixture")
		}
		if _, e := config.LoadProtected(path); e == nil || strings.Contains(e.Error(), "SYNTHETIC_PRIVATE") {
			t.Fatal("invalid config accepted or leaked input")
		}
	}
	os.WriteFile(path, raw, 0600)
	os.Chmod(root, 0755)
	if _, e := config.LoadProtected(path); e == nil {
		t.Fatal("public parent")
	}
	os.Chmod(root, 0700)
	for _, path := range []string{"relative.yaml", root + "/../control.yaml", filepath.Join(root, "missing.yaml")} {
		if _, e := config.LoadProtected(path); e == nil {
			t.Fatal("noncanonical/missing config")
		}
	}
}
