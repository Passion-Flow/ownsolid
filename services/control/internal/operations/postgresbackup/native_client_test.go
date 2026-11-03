package postgresbackup

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// This helper is present only in the test executable. Fixed copied test-binary
// basenames intercept --version before testing flag parsing, without a shell,
// environment discovery or a second production command implementation.
func init() {
	if len(os.Args) != 2 || os.Args[1] != "--version" {
		return
	}
	name := filepath.Base(os.Args[0])
	if name != "pg_dump" && name != "pg_restore" {
		return
	}
	root := filepath.Dir(os.Args[0])
	raw, e := os.ReadFile(filepath.Join(root, "synthetic-version.json"))
	var value struct{ Format, Dump, Restore string }
	if e != nil || json.Unmarshal(raw, &value) != nil || value.Format != "projectone.synthetic.native-client-version.v1" {
		os.Exit(91)
	}
	f, e := os.OpenFile(filepath.Join(root, "synthetic-executions"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if e != nil {
		os.Exit(92)
	}
	if _, e = f.WriteString(name + "\n"); e != nil || f.Sync() != nil || f.Close() != nil {
		os.Exit(93)
	}
	output := value.Dump
	if name == "pg_restore" {
		output = value.Restore
	}
	if _, e = os.Stdout.WriteString(output); e != nil {
		os.Exit(94)
	}
	os.Exit(0)
}

func TestPostgresClientDistributionExactNativeProcessesAndArtifactChecks(t *testing.T) {
	root := privateTestRoot(t)
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	p := policyFixture()
	p.ScratchRoot = root
	for _, name := range []string{"pg_dump", "pg_restore"} {
		source, e := os.Open(self)
		if e != nil {
			t.Fatal(e)
		}
		target, e := os.OpenFile(filepath.Join(root, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
		if e != nil {
			source.Close()
			t.Fatal(e)
		}
		_, e = io.Copy(target, source)
		se, te := source.Close(), target.Close()
		if e != nil || se != nil || te != nil {
			t.Fatal("private actual executable copy", e)
		}
	}
	p.DumpFile = filepath.Join(root, "pg_dump")
	p.RestoreFile = filepath.Join(root, "pg_restore")
	for path, out := range map[string]*string{p.DumpFile: &p.DumpSHA256, p.RestoreFile: &p.RestoreSHA256} {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		*out = digest(raw)
	}
	version := "18.6 (Debian 18.6-1.pgdg12+2)"
	for _, test := range []struct {
		name, selection, dump, restore string
		accepted                       bool
	}{
		{"legacy", "", "18.6", "18.6", true}, {"explicit-upstream", release.PostgreSQLClientUpstream, "18.6", "18.6", true}, {"approved-pgdg", release.PostgreSQLClientPGDGBookworm, version, version, true},
		{"default-rejects-pgdg", "", version, version, false}, {"pgdg-rejects-upstream", release.PostgreSQLClientPGDGBookworm, "18.6", "18.6", false}, {"changed-build", release.PostgreSQLClientPGDGBookworm, "18.6 (Debian 18.6-3.pgdg12+1)", version, false}, {"suffix", release.PostgreSQLClientPGDGBookworm, version + " extra", version, false}, {"changed-release", release.PostgreSQLClientPGDGBookworm, "19", version, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := p
			p.ClientDistribution = test.selection
			value := struct{ Format, Dump, Restore string }{"projectone.synthetic.native-client-version.v1", "pg_dump (PostgreSQL) " + test.dump + "\n", "pg_restore (PostgreSQL) " + test.restore + "\n"}
			raw, _ := json.Marshal(value)
			if e := os.WriteFile(filepath.Join(root, "synthetic-version.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			e := verifyTools(context.Background(), p, false)
			if (e == nil) != test.accepted {
				t.Fatal("exact native version result", e, test.accepted)
			}
		})
	}
	before, e := os.ReadFile(filepath.Join(root, "synthetic-executions"))
	if e != nil {
		t.Fatal(e)
	}
	p.ClientDistribution = release.PostgreSQLClientPGDGBookworm
	p.RestoreSHA256 = strings.Repeat("0", 64)
	if e := verifyTools(context.Background(), p, false); e == nil {
		t.Fatal("selector bypassed artifact SHA")
	}
	after, e := os.ReadFile(filepath.Join(root, "synthetic-executions"))
	if e != nil || string(before) != string(after) {
		t.Fatal("bad artifact executed before refusal", e)
	}
}

func TestPostgresClientDistributionUsesExistingExplicitPolicyOverlay(t *testing.T) {
	for _, prefix := range []string{"PROJECT_ONE_POSTGRES_BACKUP_", "PROJECT_ONE_POSTGRES_RESTORE_"} {
		p := policyFixture()
		raw := []byte(prefix + "CLIENT_DISTRIBUTION=" + release.PostgreSQLClientPGDGBookworm + "\n")
		if e := applyPolicyEnvironment(&p, raw, prefix); e != nil || p.NativeClientVersion() != "18.6 (Debian 18.6-1.pgdg12+2)" {
			t.Fatal("omitempty field is not an unused env setting", e)
		}
		for _, value := range []string{"", "false", "skip", "pgdg", "pgdg-bookworm-18.6-3.pgdg12+1"} {
			p := policyFixture()
			if e := applyPolicyEnvironment(&p, []byte(prefix+"CLIENT_DISTRIBUTION="+value+"\n"), prefix); e == nil {
				t.Fatal("invalid named build accepted", value)
			}
		}
		p = policyFixture()
		if e := applyPolicyEnvironment(&p, append(raw, raw...), prefix); e == nil {
			t.Fatal("duplicate selector accepted")
		}
	}
}
