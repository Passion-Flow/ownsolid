package mysqloperator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/mysqlstore"
)

func TestRuntimeSelectionLiteralTargetsAndDistinctAuthority(t *testing.T) {
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	ca := filepath.Join(root, "operator-ca.pem")
	otherCA := filepath.Join(root, "runtime-ca.pem")
	// Parser fixtures only; these bytes do not establish an authenticated TLS login.
	for _, p := range []string{ca, otherCA} {
		if e := os.WriteFile(p, []byte("synthetic CA fixture"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	op := mysqlstore.Configuration{Host: "localhost", Port: 3306, Database: "synthetic_test", User: "synthetic_operator", PasswordFile: filepath.Join(root, "operator-password"), CAFile: ca, ServerName: "localhost", ExpectedVersion: "8.4.8", Policy: mysqlstore.DefaultPolicy()}
	runtime := op
	runtime.User = "synthetic_runtime"
	runtime.PasswordFile = filepath.Join(root, "runtime-password")
	runtime.CAFile = otherCA
	if e := sameRuntimeSelection(op, runtime); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*mysqlstore.Configuration){
		func(c *mysqlstore.Configuration) { c.Host = "127.0.0.1" },
		func(c *mysqlstore.Configuration) { c.Port++ },
		func(c *mysqlstore.Configuration) { c.Database = "foreign_test" },
		func(c *mysqlstore.Configuration) { c.ServerName = "foreign.example" },
		func(c *mysqlstore.Configuration) { c.ExpectedVersion = "8.4.7" },
		func(c *mysqlstore.Configuration) { c.User = op.User },
		func(c *mysqlstore.Configuration) { c.PasswordFile = op.PasswordFile },
		func(c *mysqlstore.Configuration) { c.Policy.MaximumOpenConnections = 0 },
	} {
		bad := runtime
		change(&bad)
		if sameRuntimeSelection(op, bad) == nil {
			t.Fatal("foreign or invalid runtime selection accepted")
		}
	}
	if e := os.WriteFile(otherCA, []byte("foreign CA bytes"), 0600); e != nil {
		t.Fatal(e)
	}
	if sameRuntimeSelection(op, runtime) == nil {
		t.Fatal("different trust accepted")
	}
}
