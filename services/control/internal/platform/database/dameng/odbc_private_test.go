package dameng

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// These tests use disposable OS fixtures and explicit synthetic owner metadata.
// No account is changed, no driver is loaded and no database/target is exercised.
func odbcPrivateFixture(t *testing.T) (string, []byte) {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	parent := filepath.Join(base, "private-input")
	if e = os.Mkdir(parent, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(parent, "synthetic-secret")
	body := []byte("ODBC_PRIVATE_SYNTHETIC_SECRET")
	if e = os.WriteFile(path, body, 0600); e != nil {
		t.Fatal(e)
	}
	return path, body
}
func odbcAssertPrivateRefusal(t *testing.T, path string, bound int64) {
	t.Helper()
	raw, e := readODBCPrivateInput(path, bound)
	if e == nil || raw != nil || e.Error() != odbcPrivateInputInvalid {
		t.Fatal("invalid private input was projected")
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", e, e, e), path) || strings.Contains(e.Error(), "SYNTHETIC_SECRET") {
		t.Fatal("private input error leaked")
	}
}
func TestODBCPrivateInputValidOwnedSnapshot(t *testing.T) {
	path, body := odbcPrivateFixture(t)
	raw, e := readODBCPrivateInput(path, int64(len(body)))
	if e != nil || !bytes.Equal(raw, body) {
		t.Fatal("valid owned private input refused")
	}
	snapshot, e := openODBCPrivateInput(path, 128)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", snapshot, snapshot), path) {
		t.Fatal("snapshot formatted private path")
	}
	if e = snapshot.close(); e != nil {
		t.Fatal(e)
	}
	if snapshot.file != nil || snapshot.root != nil {
		t.Fatal("owned descriptors retained after close")
	}
	if e = snapshot.close(); e != nil {
		t.Fatal("idempotent owned close failed")
	}
}
func TestODBCPrivateInputRejectsPathModeLinkAndBounds(t *testing.T) {
	cases := map[string]func(*testing.T, string) string{
		"relative": func(t *testing.T, p string) string { return "relative-secret" },
		"unclean":  func(t *testing.T, p string) string { return filepath.Dir(p) + "/../private-input/" + filepath.Base(p) },
		"nul":      func(t *testing.T, p string) string { return p + "\x00suffix" },
		"newline":  func(t *testing.T, p string) string { return p + "\n" },
		"symlink_file": func(t *testing.T, p string) string {
			link := p + "-link"
			if e := os.Symlink(p, link); e != nil {
				t.Fatal(e)
			}
			return link
		},
		"symlink_parent": func(t *testing.T, p string) string {
			link := filepath.Dir(p) + "-link"
			if e := os.Symlink(filepath.Dir(p), link); e != nil {
				t.Fatal(e)
			}
			return filepath.Join(link, filepath.Base(p))
		},
		"hardlink": func(t *testing.T, p string) string {
			if e := os.Link(p, p+"-link"); e != nil {
				t.Fatal(e)
			}
			return p
		},
		"file_group_read": func(t *testing.T, p string) string {
			if e := os.Chmod(p, 0640); e != nil {
				t.Fatal(e)
			}
			return p
		},
		"file_owner_read_only": func(t *testing.T, p string) string {
			if e := os.Chmod(p, 0400); e != nil {
				t.Fatal(e)
			}
			return p
		},
		"parent_group_access": func(t *testing.T, p string) string {
			if e := os.Chmod(filepath.Dir(p), 0750); e != nil {
				t.Fatal(e)
			}
			return p
		},
		"empty": func(t *testing.T, p string) string {
			if e := os.WriteFile(p, nil, 0600); e != nil {
				t.Fatal(e)
			}
			return p
		},
		"directory": func(t *testing.T, p string) string { return filepath.Dir(p) },
		"fifo": func(t *testing.T, p string) string {
			named := p + "-fifo"
			if e := syscall.Mkfifo(named, 0600); e != nil {
				t.Fatal(e)
			}
			return named
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			path, _ := odbcPrivateFixture(t)
			path = mutate(t, path)
			odbcAssertPrivateRefusal(t, path, 128)
		})
	}
	for _, bound := range []int64{-1, 0, 1, MaximumConfigurationBytes + 1} {
		t.Run(fmt.Sprint("bound_", bound), func(t *testing.T) { path, _ := odbcPrivateFixture(t); odbcAssertPrivateRefusal(t, path, bound) })
	}
}

type odbcSyntheticInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
	mode os.FileMode
}

func (i odbcSyntheticInfo) Sys() any          { return i.stat }
func (i odbcSyntheticInfo) Mode() os.FileMode { return i.mode }
func TestODBCPrivateOwnerMetadataRefusesWrongIdentityAndSpecialBits(t *testing.T) {
	path, _ := odbcPrivateFixture(t)
	info, e := os.Lstat(path)
	if e != nil {
		t.Fatal(e)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("native stat fixture unavailable")
	}
	wrong := *stat
	wrong.Uid ^= 1
	if odbcPrivateOwned(odbcSyntheticInfo{info, &wrong, 0600}, false) {
		t.Fatal("synthetic wrong owner accepted")
	}
	linked := *stat
	linked.Nlink = 2
	if odbcPrivateOwned(odbcSyntheticInfo{info, &linked, 0600}, false) {
		t.Fatal("synthetic multiple links accepted")
	}
	for _, mode := range []os.FileMode{0600 | os.ModeSetuid, 0600 | os.ModeSetgid, 0600 | os.ModeSticky, 0600 | os.ModeSymlink, 0600 | os.ModeNamedPipe} {
		if odbcPrivateOwned(odbcSyntheticInfo{info, stat, mode}, false) {
			t.Fatal("special file mode accepted")
		}
	}
	if odbcPrivateOwned(nil, false) {
		t.Fatal("nil metadata accepted")
	}
}
func TestODBCPrivatePinnedReadRejectsDeterministicReplacements(t *testing.T) {
	cases := map[string]func(*testing.T, string, *odbcPrivateSnapshot){
		"file_replacement": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			if e := os.Rename(p, p+"-old"); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(p, []byte("ODBC_PRIVATE_SYNTHETIC_SECRET"), 0600); e != nil {
				t.Fatal(e)
			}
		},
		"parent_replacement": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			parent := filepath.Dir(p)
			if e := os.Rename(parent, parent+"-old"); e != nil {
				t.Fatal(e)
			}
			if e := os.Mkdir(parent, 0700); e != nil {
				t.Fatal(e)
			}
			if e := os.WriteFile(p, []byte("ODBC_PRIVATE_SYNTHETIC_SECRET"), 0600); e != nil {
				t.Fatal(e)
			}
		},
		"chmod_file": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			if e := os.Chmod(p, 0640); e != nil {
				t.Fatal(e)
			}
		},
		"chmod_parent": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			if e := os.Chmod(filepath.Dir(p), 0750); e != nil {
				t.Fatal(e)
			}
		},
		"extra_link": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			if e := os.Link(p, p+"-extra"); e != nil {
				t.Fatal(e)
			}
		},
		"mtime": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			changed := s.before.ModTime().Add(2 * time.Second)
			if e := os.Chtimes(p, changed, changed); e != nil {
				t.Fatal(e)
			}
		},
		"growth": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			f, e := os.OpenFile(p, os.O_WRONLY|os.O_APPEND, 0)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.Write([]byte("additional")); e != nil {
				t.Fatal(e)
			}
			if e = f.Close(); e != nil {
				t.Fatal(e)
			}
		},
		"shrink": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			if e := os.Truncate(p, 1); e != nil {
				t.Fatal(e)
			}
		},
		"closed_descriptor": func(t *testing.T, p string, s *odbcPrivateSnapshot) {
			if e := s.file.Close(); e != nil {
				t.Fatal(e)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			path, _ := odbcPrivateFixture(t)
			snapshot, e := openODBCPrivateInput(path, 128)
			if e != nil {
				t.Fatal(e)
			}
			defer snapshot.close()
			mutate(t, path, snapshot)
			raw, e := snapshot.read()
			if e == nil || raw != nil || e.Error() != odbcPrivateInputInvalid {
				t.Fatal("changed pinned snapshot projected bytes")
			}
		})
	}
}
func odbcPrivatePolicyFixture(t *testing.T, body []byte) string {
	t.Helper()
	path, _ := odbcPrivateFixture(t)
	if e := os.WriteFile(path, body, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func TestODBCPrivatePolicyRejectsFalseUnknownMissingAndDuplicate(t *testing.T) {
	config := ODBCConfig{FormatVersion: 2, ConnectionFile: "/run/owned-private/connection", LoginSeconds: 3, ConnectionSeconds: 7, QuerySeconds: 11, MaximumParameterBytes: 8192, MaximumCellBytes: 16384, MaximumRows: 31, MaximumColumns: 19, MaximumResultBytes: 32768, MaximumSQLBytes: 4096, MaximumOpenConnections: 2, MaximumPhysicalConnections: 2}
	raw, e := json.Marshal(config)
	if e != nil {
		t.Fatal(e)
	}
	got, e := LoadODBCConfig(odbcPrivatePolicyFixture(t, raw))
	if e != nil || got != config {
		t.Fatal("nondefault private policy refused")
	}
	source := string(raw)
	invalid := []string{
		strings.Replace(source, `"login_seconds":3`, `"login_seconds":false`, 1),
		strings.Replace(source, `"login_seconds":3`, `"login_seconds":"3"`, 1),
		strings.Replace(source, `"login_seconds":3`, `"login_seconds":null`, 1),
		strings.Replace(source, `"login_seconds":3,`, "", 1),
		strings.Replace(source, `"login_seconds":3`, `"login_seconds":3,"login_seconds":7`, 1),
		strings.TrimSuffix(source, "}") + `,"unknown":true}`,
		strings.Replace(source, `"maximum_result_bytes":32768`, `"maximum_result_bytes":16`, 1),
		strings.Replace(source, `"maximum_open_connections":2`, `"maximum_open_connections":0`, 1),
	}
	for i, body := range invalid {
		t.Run(fmt.Sprint("invalid_policy_", i), func(t *testing.T) {
			got, e := LoadODBCConfig(odbcPrivatePolicyFixture(t, []byte(body)))
			if e == nil || got != (ODBCConfig{}) || e.Error() != "DAMENG_ODBC_CONFIGURATION_INVALID" {
				t.Fatal("invalid policy accepted or projected")
			}
		})
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", config, config), config.ConnectionFile) {
		t.Fatal("private configuration formatted path")
	}
}
