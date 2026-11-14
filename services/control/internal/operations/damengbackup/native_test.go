package damengbackup

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fixtureVersion = "version: protocol-fixture-not-dameng"
const fixtureSecret = "synthetic-password-7"

// The actual test ELF also implements a strict synthetic vendor process. It is
// invoked without test/environment flags. This exercises real exec, private file
// input, process limits and joins; it does not execute a Dameng binary/database.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "ORPHAN_FIXTURE" {
		for {
			time.Sleep(time.Second)
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "HELP" {
		fmt.Println(fixtureVersion + "\nCONFIG_FILE UNIXSOCKET SCHEMAS WITH_UR FILESIZE FILENUM NOLOGFILE DUMMY IGNORE FAST_LOAD")
		os.Exit(0)
	}
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "CONFIG_FILE=") {
		fixtureProcess()
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixtureProcess() {
	options := map[string]string{}
	service, e := os.ReadFile(filepath.Join(os.Getenv("DM_SVC_PATH"), "dm_svc.conf"))
	if e != nil || string(service) != "# synthetic isolated native client configuration\n" {
		os.Exit(46)
	}
	for _, a := range os.Args[1:] {
		k, v, ok := strings.Cut(a, "=")
		if !ok || options[k] != "" || k == "USERID" || k == "PARFILE" {
			os.Exit(31)
		}
		options[k] = v
	}
	if options["IGNORE"] == "Y" || options["DUMMY"] != "N" || options["NOLOGFILE"] != "Y" || options["SCHEMAS"] != "APP" || options["PARALLEL"] != "1" || options["TABLE_EXISTS_ACTION"] != "" {
		os.Exit(32)
	}
	raw, e := os.ReadFile(options["CONFIG_FILE"])
	if e != nil || !bytes.Contains(raw, []byte(fixtureSecret)) {
		os.Exit(33)
	}
	start := strings.LastIndex(string(raw), "@")
	end := strings.Index(string(raw), "#{inet_type=UNIXSOCKET}")
	if start < 0 || end < start {
		os.Exit(34)
	}
	dbRoot := filepath.Dir(string(raw)[start+1 : end])
	count, _ := os.OpenFile(filepath.Join(dbRoot, "calls"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	count.WriteString(filepath.Base(os.Args[0]) + "\n")
	count.Close()
	mode, _ := os.ReadFile(filepath.Join(dbRoot, "mode"))
	// Deliberately print secret-bearing native output: journals must retain only
	// bounded metadata, never these raw bytes or the connection text.
	fmt.Println(string(raw))
	switch string(mode) {
	case "fail":
		os.Exit(35)
	case "hang":
		for {
			time.Sleep(time.Second)
		}
	case "noisy":
		for j := 0; j < 4096; j++ {
			fmt.Println(strings.Repeat("x", 128))
		}
		time.Sleep(time.Second)
	case "orphan":
		child := exec.Command(os.Args[0], "ORPHAN_FIXTURE")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if child.Start() != nil {
			os.Exit(42)
		}
		if os.WriteFile(filepath.Join(dbRoot, "child-pid"), []byte(strconv.Itoa(child.Process.Pid)), 0600) != nil {
			os.Exit(43)
		}
	}
	var state State
	dbFile := filepath.Join(dbRoot, "state.json")
	if raw, e = os.ReadFile(dbFile); e != nil || json.Unmarshal(raw, &state) != nil {
		os.Exit(36)
	}
	archivePath := filepath.Join(options["DIRECTORY"], options["FILE"])
	if filepath.Base(os.Args[0]) == "dexp" {
		if _, e = os.Stat(archivePath); !os.IsNotExist(e) {
			os.Exit(37)
		}
		if os.WriteFile(archivePath, jsonBytes(state), 0644) != nil {
			os.Exit(38)
		}
		if string(mode) == "oversized" {
			if os.WriteFile(archivePath, make([]byte, (1<<20)+1), 0600) != nil {
				os.Exit(45)
			}
		}
	} else {
		if state.Objects != 0 {
			os.Exit(39)
		}
		var source State
		raw, e = os.ReadFile(archivePath)
		if e != nil || json.Unmarshal(raw, &source) != nil {
			os.Exit(40)
		}
		source.ServerID = state.ServerID
		source.EndpointSHA256 = state.EndpointSHA256
		if string(mode) == "partial" {
			source.RowsSHA256 = sha([]byte("partial"))
		}
		if os.WriteFile(dbFile, jsonBytes(source), 0600) != nil {
			os.Exit(41)
		}
		if string(mode) == "apply-then-fail" {
			os.Exit(44)
		}
	}
}

type fixtureCustody struct {
	root string
	lost bool
}

func (f *fixtureCustody) CheckFence(context.Context) error {
	if f.lost {
		return fail("FIXTURE_FENCE_LOST")
	}
	return nil
}
func (f *fixtureCustody) Inspect(context.Context) (State, error) {
	var s State
	raw, e := os.ReadFile(filepath.Join(f.root, "state.json"))
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(raw, &s)
	return s, e
}

type fixture struct {
	n        *Native
	custody  *fixtureCustody
	parent   string
	listener net.Listener
}

func testPolicy() Policy {
	return Policy{OperationSeconds: 60, ProbeSeconds: 10, TerminationGraceSeconds: 1, ObservationMilliseconds: 20, MaximumArchiveBytes: 1 << 20, MaximumOutputBytes: 4096, MinimumFreeBytes: 1}
}
func writeTest(t *testing.T, path string, raw []byte) {
	t.Helper()
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func fileArtifact(t *testing.T, path string) Artifact {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return Artifact{path, sha(raw)}
}
func copyTest(t *testing.T, source, target string) {
	t.Helper()
	f, e := os.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if e != nil {
		t.Fatal(e)
	}
	_, e = io.Copy(out, f)
	ce := out.Close()
	if e != nil || ce != nil {
		t.Fatal(e, ce)
	}
}
func testLibraries(t *testing.T, exe string) []Artifact {
	t.Helper()
	dirs := []string{"/usr/lib/x86_64-linux-gnu", "/usr/lib/aarch64-linux-gnu", "/usr/lib64", "/usr/lib", "/lib64", "/lib"}
	paths := map[string]bool{}
	queue := []string{exe}
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		f, e := elf.Open(path)
		if e != nil {
			t.Fatal(e)
		}
		needed, e := f.ImportedLibraries()
		if e != nil {
			t.Fatal(e)
		}
		// Follow the actual frozen ELF's declared search directories, including
		// the private public-manager prefix used by the real tagged test binary.
		search := append([]string{}, dirs...)
		for _, tag := range []elf.DynTag{elf.DT_RPATH, elf.DT_RUNPATH} {
			values, e := f.DynString(tag)
			if e != nil {
				t.Fatal(e)
			}
			for _, value := range values {
				for _, entry := range strings.Split(value, ":") {
					entry = strings.ReplaceAll(strings.ReplaceAll(entry, "${ORIGIN}", filepath.Dir(path)), "$ORIGIN", filepath.Dir(path))
					if filepath.IsAbs(entry) {
						search = append(search, entry)
					}
				}
			}
		}
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP {
				raw, e := io.ReadAll(p.Open())
				if e != nil {
					t.Fatal(e)
				}
				needed = append(needed, strings.TrimRight(string(raw), "\x00"))
			}
		}
		f.Close()
		for _, name := range needed {
			path := ""
			candidates := []string{name}
			if !filepath.IsAbs(name) {
				candidates = nil
				for _, d := range search {
					candidates = append(candidates, filepath.Join(d, name))
				}
			}
			for _, c := range candidates {
				if p, e := filepath.EvalSymlinks(c); e == nil {
					path = p
					break
				}
			}
			if path == "" {
				t.Fatalf("missing fixture dependency %s", name)
			}
			if !paths[path] {
				paths[path] = true
				queue = append(queue, path)
			}
		}
	}
	// A pure-Go executable may have no dynamic dependency. The mandatory native
	// library inventory is still exercised with the host's real libc ELF.
	if len(paths) == 0 {
		for _, d := range dirs {
			p, e := filepath.EvalSymlinks(filepath.Join(d, "libc.so.6"))
			if e == nil {
				paths[p] = true
				break
			}
		}
	}
	out := []Artifact{}
	for path := range paths {
		out = append(out, fileArtifact(t, path))
	}
	return out
}
func newFixture(t *testing.T, fresh bool) *fixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("actual ELF/socket protocol fixture requires Linux")
	}
	// UNIX pathname sockets have a 107-byte bound. A runner's long TMPDIR plus
	// testing's full case name cannot represent the admitted native profile.
	// This explicit Linux-only private fixture root shares /tmp's admitted host
	// filesystem, is uniquely owned and is removed only after exact readback.
	root, e := os.MkdirTemp("/tmp", "p1-20261103-M29-recovery-")
	if e != nil {
		t.Fatal(e)
	}
	info, e := os.Lstat(root)
	if e != nil {
		t.Fatal(e)
	}
	originalIdentity := identity(info)
	t.Cleanup(func() {
		info, e := os.Lstat(root)
		if e != nil || identity(info) != originalIdentity || noSymlinks(root) != nil {
			t.Errorf("fixture root identity changed; retained %s", root)
			return
		}
		if e = os.RemoveAll(root); e != nil {
			t.Errorf("owned fixture cleanup failed: %v", e)
		}
	})
	t.Logf("owned synthetic fixture root %s", root)
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	db := filepath.Join(root, "db")
	tools := filepath.Join(root, "tools")
	ops := filepath.Join(root, "operations")
	for _, p := range []string{db, tools, ops} {
		if e := os.Mkdir(p, 0700); e != nil {
			t.Fatal(e)
		}
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"dexp", "dimp"} {
		copyTest(t, exe, filepath.Join(tools, name))
	}
	license := filepath.Join(tools, "license.txt")
	review := filepath.Join(tools, "review.txt")
	writeTest(t, license, []byte("synthetic fixture, no vendor license"))
	writeTest(t, review, []byte("fixture only, not lawful native artifact admission"))
	service := filepath.Join(tools, "dm_svc.conf")
	writeTest(t, service, []byte("# synthetic isolated native client configuration\n"))
	i := Inventory{FormatVersion, "linux", runtime.GOARCH, "fixture-server", Tool{fileArtifact(t, filepath.Join(tools, "dexp")), fixtureVersion}, Tool{fileArtifact(t, filepath.Join(tools, "dimp")), fixtureVersion}, testLibraries(t, exe), []Artifact{fileArtifact(t, license)}, fileArtifact(t, review), fileArtifact(t, service)}
	inventory := filepath.Join(root, "inventory.json")
	writeTest(t, inventory, jsonBytes(i))
	password := filepath.Join(root, "password")
	writeTest(t, password, []byte(fixtureSecret))
	socket := filepath.Join(db, "s")
	listener, e := net.Listen("unix", socket)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { listener.Close() })
	if e = os.Chmod(socket, 0600); e != nil {
		t.Fatal(e)
	}
	c := Configuration{FormatVersion, inventory, sha(jsonBytes(i)), Connection{"linux_unixsocket", socket, os.Geteuid(), "APP", "APP", password}, testPolicy()}
	n := &Native{configuration: c, inventory: i}
	endpoint, e := n.EndpointFingerprint()
	if e != nil {
		t.Fatal(e)
	}
	s := State{"source-server", "fixture-server", "APP", "APP", "installation-1", sha([]byte("full-catalog")), sha([]byte("every-row-and-lob")), 3, endpoint, ""}
	if fresh {
		s.ServerID = "target-server"
		s.InstallationID = ""
		s.Objects = 0
		s.CatalogSHA256 = sha(nil)
		s.RowsSHA256 = sha(nil)
		s.FreshMarker = "fresh-isolated-fixture-1"
	}
	writeTest(t, filepath.Join(db, "state.json"), jsonBytes(s))
	return &fixture{n, &fixtureCustody{root: db}, ops, listener}
}
func (f *fixture) backup(t *testing.T) string {
	t.Helper()
	p := filepath.Join(f.parent, "backup")
	if _, e := f.n.Backup(context.Background(), p, "synthetic recovery test", f.custody); e != nil {
		t.Fatal(e)
	}
	return p
}
func errorContains(t *testing.T, e error, code string) {
	t.Helper()
	if e == nil || !strings.Contains(e.Error(), code) {
		t.Fatalf("expected %s, got %v", code, e)
	}
}
func calls(t *testing.T, f *fixture) int {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(f.custody.root, "calls"))
	if os.IsNotExist(e) {
		return 0
	}
	if e != nil {
		t.Fatal(e)
	}
	return strings.Count(string(raw), "\n")
}

func TestDatabaseBackupAndFreshRestore(t *testing.T) {
	f := newFixture(t, false)
	backup := f.backup(t)
	for _, name := range []string{"intent.json", "dispatch.json", "joined.json", "complete.json", "database.dmp"} {
		raw, e := os.ReadFile(filepath.Join(backup, name))
		if e != nil || bytes.Contains(raw, []byte(fixtureSecret)) {
			t.Fatalf("retained file leaked or missing: %s %v", name, e)
		}
	}
	if _, e := os.Stat(filepath.Join(backup, "connection.conf")); !os.IsNotExist(e) {
		t.Fatal("secret file retained")
	}
	before := calls(t, f)
	first, e := f.n.Reconcile(context.Background(), backup, f.custody)
	if e != nil {
		t.Fatal(e)
	}
	again, e := f.n.Reconcile(context.Background(), backup, f.custody)
	if e != nil || again != first || calls(t, f) != before {
		t.Fatal("reconciliation changed/replayed", e)
	}
	// Select a different explicit target connection while retaining the same
	// pinned vendor inventory. The source backup policy need not be the target's.
	target := newFixture(t, true)
	target.n.inventory = f.n.inventory
	target.n.configuration.InventoryFile = f.n.configuration.InventoryFile
	target.n.configuration.InventorySHA256 = f.n.configuration.InventorySHA256
	target.n.configuration.Policy.MaximumArchiveBytes = 2 << 20
	restored, e := target.n.Restore(context.Background(), backup, filepath.Join(target.parent, "restore"), "fresh isolated target", target.custody)
	if e != nil || !restored.DatabaseVerified || restored.Database.ServerID != "target-server" || restored.Database.InstallationID != "installation-1" {
		t.Fatal(restored, e)
	}
	if calls(t, target) != 1 {
		t.Fatal("expected exactly one import")
	}
	if _, e = target.n.Reconcile(context.Background(), filepath.Join(target.parent, "restore"), target.custody); e != nil || calls(t, target) != 1 {
		t.Fatal("restore reconcile replayed", e)
	}
}
func TestOriginalRootAndUnknownOutcomes(t *testing.T) {
	for _, mode := range []string{"fail", "hang", "noisy"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			writeTest(t, filepath.Join(f.custody.root, "mode"), []byte(mode))
			root := filepath.Join(f.parent, "attempt")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if mode == "hang" {
				done := make(chan struct{})
				defer func() { cancel(); <-done }()
				go func() {
					defer close(done)
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-ticker.C:
							if _, e := os.Stat(filepath.Join(f.custody.root, "calls")); e == nil {
								cancel()
								return
							}
						}
					}
				}()
			}
			_, e := f.n.Backup(ctx, root, "fault fixture", f.custody)
			errorContains(t, e, "OUTCOME_UNKNOWN")
			if _, e = os.Stat(filepath.Join(root, "complete.json")); !os.IsNotExist(e) {
				t.Fatal("published unknown result")
			}
			before := calls(t, f)
			_, e = f.n.Reconcile(context.Background(), root, f.custody)
			errorContains(t, e, "OUTCOME_UNKNOWN")
			if calls(t, f) != before {
				t.Fatal("replayed uncertain operation")
			}
			_, e = f.n.Backup(context.Background(), root, "do not replay", f.custody)
			errorContains(t, e, "FRESH_ROOT_REQUIRED")
			if calls(t, f) != before {
				t.Fatal("reused original root")
			}
		})
	}
}
func TestReconcileRequiresOriginalProofs(t *testing.T) {
	for _, name := range []string{"intent.json", "dispatch.json", "joined.json", "database.dmp", "complete.json"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, false)
			root := f.backup(t)
			before := calls(t, f)
			writeTest(t, filepath.Join(root, name), []byte("tampered"))
			_, e := f.n.Reconcile(context.Background(), root, f.custody)
			if e == nil {
				t.Fatal("tampering accepted")
			}
			if calls(t, f) != before {
				t.Fatal("replay")
			}
		})
	}
}
func TestReconcilePublishesOnlyJoinedVerifiedResult(t *testing.T) {
	f := newFixture(t, false)
	root := f.backup(t)
	if e := os.Remove(filepath.Join(root, "complete.json")); e != nil {
		t.Fatal(e)
	}
	before := calls(t, f)
	if _, e := f.n.Reconcile(context.Background(), root, f.custody); e != nil || calls(t, f) != before {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(root, "connection.conf"), []byte("retained crash credential"))
	_, e := f.n.Reconcile(context.Background(), root, f.custody)
	errorContains(t, e, "ACTIVE_CONSUMER")
}
func TestRestoreRefusesOccupiedSameOrPartialTarget(t *testing.T) {
	for _, mode := range []string{"occupied", "same", "partial"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			backup := f.backup(t)
			target := newFixture(t, true)
			target.n.inventory = f.n.inventory
			target.n.configuration.InventoryFile = f.n.configuration.InventoryFile
			target.n.configuration.InventorySHA256 = f.n.configuration.InventorySHA256
			state, _ := target.custody.Inspect(context.Background())
			if mode == "occupied" {
				state.Objects = 1
			}
			if mode == "same" {
				state.ServerID = "source-server"
			}
			writeTest(t, filepath.Join(target.custody.root, "state.json"), jsonBytes(state))
			if mode == "partial" {
				writeTest(t, filepath.Join(target.custody.root, "mode"), []byte(mode))
			}
			_, e := target.n.Restore(context.Background(), backup, filepath.Join(target.parent, "restore"), "negative fixture", target.custody)
			if e == nil {
				t.Fatal("unsafe restoration accepted")
			}
			expected := 0
			if mode == "partial" {
				expected = 1
			}
			if calls(t, target) != expected {
				t.Fatal("unexpected import count")
			}
		})
	}
}
func TestFenceAndCredentialChangesRefuseCompletion(t *testing.T) {
	for _, mode := range []string{"fence", "credential", "source"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			root := f.backup(t)
			before := calls(t, f)
			switch mode {
			case "fence":
				f.custody.lost = true
			case "credential":
				writeTest(t, f.n.configuration.Connection.PasswordFile, []byte("replacement-password"))
			case "source":
				s, _ := f.custody.Inspect(context.Background())
				s.RowsSHA256 = sha([]byte("new-row"))
				writeTest(t, filepath.Join(f.custody.root, "state.json"), jsonBytes(s))
			}
			_, e := f.n.Reconcile(context.Background(), root, f.custody)
			if e == nil {
				t.Fatal("changed state accepted")
			}
			if calls(t, f) != before {
				t.Fatal("replayed")
			}
		})
	}
}
func TestArtifactVersionAndLicenseAdmission(t *testing.T) {
	for _, mode := range []string{"tool", "version", "license", "library", "architecture", "setuid", "service-override"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			switch mode {
			case "tool":
				writeTest(t, f.n.inventory.Dexp.Artifact.Path, []byte("not-native"))
			case "version":
				f.n.inventory.Dexp.VersionLine = "version: different"
			case "license":
				writeTest(t, f.n.inventory.Licenses[0].Path, []byte("changed"))
			case "library":
				f.n.inventory.Libraries = nil
			case "architecture":
				f.n.inventory.Architecture = "invalid"
			case "setuid":
				if e := os.Chmod(f.n.inventory.Dexp.Artifact.Path, 0700|os.ModeSetuid); e != nil {
					t.Fatal(e)
				}
			case "service-override":
				writeTest(t, f.n.inventory.ServiceConfiguration.Path, []byte("DUMP_CONN_STR=(unexpected)\n"))
				f.n.inventory.ServiceConfiguration = fileArtifact(t, f.n.inventory.ServiceConfiguration.Path)
			}
			if e := f.n.verify(context.Background()); e == nil {
				t.Fatal("invalid native admission")
			}
			if calls(t, f) != 0 {
				t.Fatal("database effect before admission")
			}
		})
	}
}
func TestPrivateFileAndConfigurationBoundaries(t *testing.T) {
	f := newFixture(t, false)
	c := f.n.configuration
	c.Connection.SocketOwnerUID = 1000
	if c.Validate() != nil {
		t.Fatal(c.Validate())
	}
	path := filepath.Join(filepath.Dir(f.parent), "configuration.json")
	writeTest(t, path, jsonBytes(c))
	if _, e := LoadConfiguration(path, ""); e != nil {
		t.Fatal(e)
	}
	for _, raw := range []string{strings.TrimSpace(string(jsonBytes(c))) + " {}", `{"format_version":1,"format_version":1}`, `{"format_version":null}`, strings.Replace(string(jsonBytes(c)), `"operation_seconds":60`, `"operation_seconds":false`, 1), strings.Replace(string(jsonBytes(c)), `"operation_seconds":60`, `"operation_seconds":0`, 1)} {
		writeTest(t, path, []byte(raw))
		if _, e := LoadConfiguration(path, ""); e == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
	writeTest(t, path, jsonBytes(c))
	env := filepath.Join(filepath.Dir(f.parent), "recovery.env")
	writeTest(t, env, []byte(ConfigurationEnvironment+"="+path+"\n"))
	if _, e := LoadConfiguration(path, env); e != nil {
		t.Fatal(e)
	}
	for _, line := range []string{"UNKNOWN=value", ConfigurationEnvironment + "=false", ConfigurationEnvironment + "=" + path + "\n" + ConfigurationEnvironment + "=" + path} {
		writeTest(t, env, []byte(line))
		if _, e := LoadConfiguration(path, env); e == nil {
			t.Fatal("invalid env accepted")
		}
	}
	if e := os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadConfiguration(path, ""); e == nil {
		t.Fatal("public credential/config accepted")
	}
	link := filepath.Join(filepath.Dir(f.parent), "link")
	if e := os.Symlink(c.Connection.PasswordFile, link); e != nil {
		t.Fatal(e)
	}
	if _, e := readPrivate(link, maximumPasswordBytes); e == nil {
		t.Fatal("symlink accepted")
	}
	hard := filepath.Join(filepath.Dir(f.parent), "hard")
	if e := os.Link(c.Connection.PasswordFile, hard); e != nil {
		t.Fatal(e)
	}
	if _, e := readPrivate(hard, maximumPasswordBytes); e == nil {
		t.Fatal("hardlink accepted")
	}
}
func TestCancelledProcessNeverStarts(t *testing.T) {
	p := testPolicy()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, _, e := runProcess(ctx, "/does/not/exist", nil, "/", nil, p, nil)
	if e == nil || r.Started || !r.Cancelled {
		t.Fatal(r, e)
	}
}

func TestAppliedImportFailureNeverReplays(t *testing.T) {
	f := newFixture(t, false)
	backup := f.backup(t)
	target := newFixture(t, true)
	target.n.inventory = f.n.inventory
	target.n.configuration.InventoryFile = f.n.configuration.InventoryFile
	target.n.configuration.InventorySHA256 = f.n.configuration.InventorySHA256
	writeTest(t, filepath.Join(target.custody.root, "mode"), []byte("apply-then-fail"))
	root := filepath.Join(target.parent, "restore")
	_, e := target.n.Restore(context.Background(), backup, root, "unknown completed SQL fixture", target.custody)
	errorContains(t, e, "OUTCOME_UNKNOWN")
	state, e := target.custody.Inspect(context.Background())
	if e != nil || state.InstallationID != "installation-1" || state.Objects != 3 {
		t.Fatal("fixture did not apply before failure", state, e)
	}
	_, e = target.n.Reconcile(context.Background(), root, target.custody)
	errorContains(t, e, "OUTCOME_UNKNOWN")
	_, e = target.n.Restore(context.Background(), backup, root, "forbidden replay", target.custody)
	if e == nil || calls(t, target) != 1 {
		t.Fatal("uncertain native import replayed", e)
	}
}
func TestDescendantProcessIsStoppedBeforeCleanup(t *testing.T) {
	f := newFixture(t, false)
	writeTest(t, filepath.Join(f.custody.root, "mode"), []byte("orphan"))
	root := filepath.Join(f.parent, "attempt")
	_, e := f.n.Backup(context.Background(), root, "inherited pipe fixture", f.custody)
	errorContains(t, e, "OUTCOME_UNKNOWN")
	r, e := openPrivateRoot(root)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	var joined Joined
	if _, e = readDocument(r, "joined.json", &joined); e != nil || !joined.Execution.ProcessGroupStopped || !processGroupStopped(joined.Execution.ProcessGroupID) {
		t.Fatal("native descendants remain active", e, joined)
	}
	if _, e = os.Stat(filepath.Join(root, "connection.conf")); !os.IsNotExist(e) {
		t.Fatal("secret not cleaned after group stopped")
	}
}
func TestNativeConstructorKeepsNonrootGuard(t *testing.T) {
	f := newFixture(t, false)
	c := f.n.configuration
	if os.Geteuid() == 0 {
		c.Connection.SocketOwnerUID = 1000
		_, e := New(context.Background(), c)
		errorContains(t, e, "NONROOT_LINUX_REQUIRED")
	}
}

func TestArchiveAndCapacityBudgets(t *testing.T) {
	for _, mode := range []string{"oversized", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			if mode == "oversized" {
				writeTest(t, filepath.Join(f.custody.root, "mode"), []byte(mode))
			} else {
				f.n.configuration.Policy.MinimumFreeBytes = ^uint64(0)
			}
			_, e := f.n.Backup(context.Background(), filepath.Join(f.parent, "attempt"), "resource refusal fixture", f.custody)
			if e == nil {
				t.Fatal("over-budget execution accepted")
			}
			if mode == "capacity" && calls(t, f) != 0 {
				t.Fatal("effect before capacity admission")
			}
		})
	}
}
func TestChangedOriginalBytesAndRootIdentity(t *testing.T) {
	for _, mode := range []string{"intent-whitespace", "root-inode"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			root := f.backup(t)
			if mode == "intent-whitespace" {
				path := filepath.Join(root, "intent.json")
				raw, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				writeTest(t, path, append(raw, ' '))
			} else {
				retained := root + "-original"
				if e := os.Rename(root, retained); e != nil {
					t.Fatal(e)
				}
				if e := os.Mkdir(root, 0700); e != nil {
					t.Fatal(e)
				}
				entries, e := os.ReadDir(retained)
				if e != nil {
					t.Fatal(e)
				}
				for _, entry := range entries {
					raw, e := os.ReadFile(filepath.Join(retained, entry.Name()))
					if e != nil {
						t.Fatal(e)
					}
					writeTest(t, filepath.Join(root, entry.Name()), raw)
				}
			}
			_, e := f.n.Reconcile(context.Background(), root, f.custody)
			if e == nil || calls(t, f) != 1 {
				t.Fatal("changed original binding accepted/replayed", e)
			}
		})
	}
}

func TestDeclaredSONAMERequiresExactPinnedTarget(t *testing.T) {
	f := newFixture(t, false)
	var library, soname string
	for _, a := range f.n.inventory.Libraries {
		e, err := elf.Open(a.Path)
		if err != nil {
			t.Fatal(err)
		}
		names, err := e.DynString(elf.DT_SONAME)
		e.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(names) == 1 && names[0] != "" && filepath.Base(names[0]) == names[0] {
			library, soname = a.Path, names[0]
			break
		}
	}
	if library == "" {
		t.Fatal("real host ELF fixture has no SONAME")
	}
	dir := filepath.Join(f.parent, "alias-libraries")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	pinned := filepath.Join(dir, "library.pinned")
	copyTest(t, library, pinned)
	alias := filepath.Join(dir, soname)
	if e := os.Symlink("library.pinned", alias); e != nil {
		t.Fatal(e)
	}
	n := &Native{inventory: Inventory{Libraries: []Artifact{fileArtifact(t, pinned)}}}
	bindings, _, e := n.libraryBindings()
	if e != nil || bindings[soname] != pinned {
		t.Fatal("verified versioned DSO alias refused", e)
	}
	other := filepath.Join(dir, "library.unpinned")
	copyTest(t, library, other)
	if e = os.Remove(alias); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("library.unpinned", alias); e != nil {
		t.Fatal(e)
	}
	if _, _, e = n.libraryBindings(); e == nil {
		t.Fatal("same bytes through an unpinned different alias target admitted")
	}
	if e = os.Remove(alias); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink("library.pinned", alias); e != nil {
		t.Fatal(e)
	}
	shadowDir := filepath.Join(f.parent, "shadow-libraries")
	if e = os.Mkdir(shadowDir, 0700); e != nil {
		t.Fatal(e)
	}
	shadow := filepath.Join(shadowDir, "library.shadow")
	copyTest(t, library, shadow)
	if e = os.Symlink("library.shadow", filepath.Join(shadowDir, soname)); e != nil {
		t.Fatal(e)
	}
	n.inventory.Libraries = append(n.inventory.Libraries, fileArtifact(t, shadow))
	if _, _, e = n.libraryBindings(); e == nil {
		t.Fatal("ambiguous SONAME accepted across declared loader directories")
	}
}

func TestNativeLaunchPreservesVerifiedLibraryPathAndRejectsCWDShadow(t *testing.T) {
	f := newFixture(t, false)
	parent := filepath.Join(f.parent, "client-launch")
	bin := filepath.Join(parent, "bin")
	for _, path := range []string{parent, bin, filepath.Join(parent, "release"), filepath.Join(bin, "dependencies")} {
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
	}
	n := *f.n
	n.workingDirectory = bin
	configDir := filepath.Dir(n.inventory.ServiceConfiguration.Path)
	n.launchEnvironment = map[string]string{"LD_LIBRARY_PATH": bin, "DM_SVC_PATH": configDir, "ODBCSYSINI": configDir, "ODBCINI": filepath.Join(configDir, "odbc.ini"), "ODBCINSTINI": "odbcinst.ini"}
	if e := n.verifyLaunch(); e != nil {
		t.Fatal(e)
	}
	found := ""
	for _, entry := range n.environment() {
		if strings.HasPrefix(entry, "LD_LIBRARY_PATH=") {
			found = strings.TrimPrefix(entry, "LD_LIBRARY_PATH=")
		}
	}
	if found != bin {
		t.Fatal("typed kernel launch environment was silently replaced by inventory directories")
	}
	if _, _, e := n.libraryBindings(); e != nil {
		t.Fatal(e)
	}
	library := n.inventory.Libraries[0]
	copyTest(t, library.Path, filepath.Join(bin, filepath.Base(library.Path)))
	if _, _, e := n.libraryBindings(); e == nil {
		t.Fatal("relative RPATH working directory supplied an unpinned shadow library")
	}
}
