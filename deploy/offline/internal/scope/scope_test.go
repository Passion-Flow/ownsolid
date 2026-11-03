package scope

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// All keys and signed payloads here are explicit synthetic contract fixtures.
// The separate tagged CLI journey uses the actual manufacturer executable.
func fixture(t *testing.T) configuration.Config {
	return fixtureWithAssets(t, nil, nil)
}
func fixtureWithAssets(t *testing.T, extra []wire.Asset, extraPayloads [][]byte) configuration.Config {
	t.Helper()
	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private fixture unavailable")
	}
	observeTemporaryScopeStorage(t, root)
	mount := root
	for !mounted(mount) {
		parent := filepath.Dir(mount)
		if parent == mount {
			t.Fatal("actual external mounted TMPDIR required")
		}
		mount = parent
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{43}, 32))
	public := key.Public().(ed25519.PublicKey)
	trust := filepath.Join(root, "trust.json")
	put(t, trust, encode(map[string]any{"keys": map[string]string{wire.Digest(public): base64.StdEncoding.EncodeToString(public)}}))
	payloads := [][]byte{[]byte("Synthetic test license"), []byte("Synthetic offline scope payload")}
	m := wire.Manifest{FormatVersion: 1, ReleaseID: "synthetic-scope-1", Profile: wire.FixtureProfile, SourceSHA256: strings.Repeat("a", 64), SchemaVersion: 16, MinimumPredecessor: 15, MaximumPredecessor: 16, Capabilities: []string{"M01"}, Prerequisites: []string{}, Assets: []wire.Asset{{ID: "license", Path: "license", Kind: "license", Size: int64(len(payloads[0])), SHA256: wire.Digest(payloads[0]), Licenses: []string{}}, {ID: "payload", Path: "nested/payload", Kind: "operator", Size: int64(len(payloads[1])), SHA256: wire.Digest(payloads[1]), Licenses: []string{"license"}}}}
	m.Assets = append(m.Assets, extra...)
	payloads = append(payloads, extraPayloads...)
	raw := encode(m)
	signature := encode(wire.Signature{Algorithm: wire.SignatureAlgorithm, KeyID: wire.Digest(public), ManifestSHA256: wire.Digest(raw), Value: base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte("projectone-offline-release-v1\n"), raw...)))})
	var output bytes.Buffer
	gz := gzip.NewWriter(&output)
	archive := tar.NewWriter(gz)
	write := func(name string, raw []byte) {
		if e := archive.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(raw)), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}); e != nil {
			t.Fatal(e)
		}
		if _, e := archive.Write(raw); e != nil {
			t.Fatal(e)
		}
	}
	write("manifest.json", raw)
	write("signature.json", signature)
	for i, a := range m.Assets {
		write(a.Path, payloads[i])
	}
	archive.Close()
	gz.Close()
	bundlePath := filepath.Join(root, "fixture.tar.gz")
	put(t, bundlePath, output.Bytes())
	c := configuration.Defaults()
	c.Verification.MountRoot = mount
	c.Verification.TrustFile = trust
	c.Verification.AllowTestFixture = true
	c.BundleFile = bundlePath
	c.ExpectedArchiveSHA256 = wire.Digest(output.Bytes())
	c.ExpectedManifestSHA256 = wire.Digest(raw)
	c.ScopeRoot = filepath.Join(root, "scope")
	c.MaximumJournalBytes = 131072
	return c
}
func put(t *testing.T, name string, raw []byte) {
	t.Helper()
	if e := os.WriteFile(name, raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestActualScopeCreatePersistedIdentityAndReadonlyRestart(t *testing.T) {
	c := fixture(t)
	s, e := Create(context.Background(), c)
	if e != nil || s.Status != "isolated_scope_prepared" || !identityPattern.MatchString(s.ScopeID) || s.Events != 3 || s.Qualification != "synthetic_isolated_scope_preparation_only_never_application_installation_or_target" {
		t.Fatal(s, e)
	}
	before, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if e != nil {
		t.Fatal(e)
	}
	again, e := Read(context.Background(), c)
	if e != nil || again.ScopeID != s.ScopeID || again.ConfigurationSHA256 != c.Fingerprint() {
		t.Fatal(again, e)
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("readonly status modified state")
	}
	if _, e = Create(context.Background(), c); e == nil {
		t.Fatal("scope initialization repeated")
	}
	after, _ = os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if !bytes.Equal(before, after) {
		t.Fatal("repeat changed scope")
	}
}
func TestScopeRejectsUnknownAndConflictingContext(t *testing.T) {
	for _, fault := range []string{"existing-empty", "existing-data", "existing-symlink", "overlap", "wrong-archive", "unmounted"} {
		t.Run(fault, func(t *testing.T) {
			c := fixture(t)
			switch fault {
			case "existing-empty":
				os.Mkdir(c.ScopeRoot, 0700)
			case "existing-data":
				os.Mkdir(c.ScopeRoot, 0700)
				put(t, filepath.Join(c.ScopeRoot, "unknown"), []byte("preserve"))
			case "existing-symlink":
				os.Symlink(filepath.Dir(c.ScopeRoot), c.ScopeRoot)
			case "overlap":
				c.DestinationRoot = filepath.Dir(c.ScopeRoot)
			case "wrong-archive":
				c.ExpectedArchiveSHA256 = strings.Repeat("b", 64)
			case "unmounted":
				c.Verification.MountRoot = filepath.Dir(c.ScopeRoot)
			}
			if _, e := Create(context.Background(), c); e == nil {
				t.Fatal("unsafe scope accepted")
			}
			if fault == "existing-data" {
				raw, e := os.ReadFile(filepath.Join(c.ScopeRoot, "unknown"))
				if e != nil || string(raw) != "preserve" {
					t.Fatal("unknown data changed")
				}
			}
		})
	}
}
func TestScopeStatusIntegrityContextAndJournalHead(t *testing.T) {
	for _, fault := range []string{"configuration", "marker-identity", "journal-truncated", "journal-sequence", "journal-previous", "journal-tail", "journal-duplicate", "head-missing", "head-digest", "pending-head", "payload", "extra-root", "lock-link"} {
		t.Run(fault, func(t *testing.T) {
			c := fixture(t)
			if _, e := Create(context.Background(), c); e != nil {
				t.Fatal(e)
			}
			edit := func(name string, f func([]byte) []byte) {
				p := filepath.Join(c.ScopeRoot, name)
				raw, e := os.ReadFile(p)
				if e != nil {
					t.Fatal(e)
				}
				put(t, p, f(raw))
			}
			switch fault {
			case "configuration":
				c.TimeoutSeconds++
			case "marker-identity":
				edit(markerName, func(raw []byte) []byte {
					var m Marker
					json.Unmarshal(raw, &m)
					m.ScopeID = strings.Repeat("b", 32)
					return encode(m)
				})
			case "journal-truncated":
				edit(journalName, func(raw []byte) []byte { return raw[:len(raw)-1] })
			case "journal-sequence":
				edit(journalName, func(raw []byte) []byte { return bytes.Replace(raw, []byte(`"sequence":2`), []byte(`"sequence":1`), 1) })
			case "journal-previous":
				edit(journalName, func(raw []byte) []byte {
					return bytes.Replace(raw, []byte(`"previous_event_sha256":"`), []byte(`"previous_event_sha256":"a`), 1)
				})
			case "journal-tail":
				edit(journalName, func(raw []byte) []byte {
					lines := bytes.Split(bytes.TrimSuffix(raw, []byte("\n")), []byte("\n"))
					var e Event
					json.Unmarshal(lines[2], &e)
					e.Time = "2026-10-17T00:00:00Z"
					lines[2] = bytes.TrimSuffix(encode(e), []byte("\n"))
					return append(bytes.Join(lines, []byte("\n")), '\n')
				})
			case "journal-duplicate":
				edit(journalName, func(raw []byte) []byte {
					return bytes.Replace(raw, []byte(`"sequence":1`), []byte(`"sequence":1,"sequence":1`), 1)
				})
			case "head-missing":
				os.Remove(filepath.Join(c.ScopeRoot, headName))
			case "head-digest":
				edit(headName, func(raw []byte) []byte {
					var h journalHead
					json.Unmarshal(raw, &h)
					h.EventSHA256 = strings.Repeat("c", 64)
					return encode(h)
				})
			case "pending-head":
				put(t, filepath.Join(c.ScopeRoot, pendingHeadName), []byte("preserved uncertain head"))
			case "payload":
				edit("release/nested/payload", func(raw []byte) []byte { raw[0] ^= 1; return raw })
			case "extra-root":
				put(t, filepath.Join(c.ScopeRoot, "unknown"), []byte("preserve"))
			case "lock-link":
				os.Link(filepath.Join(c.ScopeRoot, lockName), filepath.Join(filepath.Dir(c.ScopeRoot), "unexpected-lock-link"))
			}
			if _, e := Read(context.Background(), c); e == nil {
				t.Fatal("bad context/ledger/prepared bytes adopted")
			}
			if _, e := os.Lstat(c.ScopeRoot); e != nil {
				t.Fatal("failed read deleted scope")
			}
		})
	}
}
func TestScopeReadAndExclusiveLockAdmission(t *testing.T) {
	c := fixture(t)
	if _, e := Create(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	exclusive, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Read(context.Background(), c); e == nil || e.Error() != "OFFLINE_SCOPE_BUSY" {
		t.Fatal("concurrent exclusive lock ignored", e)
	}
	exclusive.close()
	first, e := open(c, false)
	if e != nil {
		t.Fatal(e)
	}
	second, e := open(c, false)
	if e != nil {
		first.close()
		t.Fatal("shared readers incorrectly excluded", e)
	}
	second.close()
	first.close()
	if _, e = Read(context.Background(), c); e != nil {
		t.Fatal(e)
	}
}
func TestConcurrentScopeInitializationSingleWinner(t *testing.T) {
	c := fixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; _, e := Create(context.Background(), c); results <- e }()
	}
	close(start)
	passed := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			passed++
		}
	}
	if passed != 1 {
		t.Fatal("scope not exclusively initialized", passed)
	}
	if _, e := Read(context.Background(), c); e != nil {
		t.Fatal(e)
	}
}
func TestScopeCancelledBeforeMutation(t *testing.T) {
	c := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Create(ctx, c); e == nil {
		t.Fatal("cancelled initialization passed")
	}
	if _, e := os.Lstat(c.ScopeRoot); !os.IsNotExist(e) {
		t.Fatal("cancelled scope created")
	}
	if _, e := Read(ctx, c); e == nil {
		t.Fatal("cancelled read passed")
	}
}

func TestScopeFailedUncertainAndDeceptivePreparationNeverCompletes(t *testing.T) {
	for _, fault := range []string{"failed", "uncertain", "deceptive-success"} {
		t.Run(fault, func(t *testing.T) {
			c := fixture(t)
			_, e := create(context.Background(), c, func(context.Context, configuration.Config, string) error {
				switch fault {
				case "failed":
					return errors.New("RELEASE_EXPLICIT_SYNTHETIC_PREPARATION_FAILURE")
				case "uncertain":
					return errors.New("RELEASE_EXPLICIT_SYNTHETIC_PREPARATION_UNCERTAIN")
				default:
					return nil
				}
			})
			if e == nil {
				t.Fatal("failure or false adapter success was accepted")
			}
			status, e := Read(context.Background(), c)
			expected := "isolated_scope_failed"
			if fault == "uncertain" {
				expected = "isolated_scope_action_uncertain"
			}
			if e != nil || status.Status != expected || status.Events != 3 {
				t.Fatal("failure evidence not readable", status, e)
			}
			if _, e = Create(context.Background(), c); e == nil {
				t.Fatal("failed or uncertain scope blindly retried")
			}
		})
	}
}

// Explicit test context injects a response deadline after the actual completed head.
// Every preparation/signature/file/directory/journal operation still runs for real.
type deadlineAtCompletedHead struct {
	context.Context
	root string
}

func (c deadlineAtCompletedHead) Err() error {
	raw, _ := os.ReadFile(filepath.Join(c.root, headName))
	var head journalHead
	if json.Unmarshal(raw, &head) == nil && head.Sequence == 3 {
		return context.DeadlineExceeded
	}
	return c.Context.Err()
}
func TestScopeLateResponseDeadlineIsUncertainAndReadonlyReconcilesActualFiles(t *testing.T) {
	c := fixture(t)
	ctx := deadlineAtCompletedHead{Context: context.Background(), root: c.ScopeRoot}
	if _, e := Create(ctx, c); e == nil || e.Error() != "OFFLINE_SCOPE_COMMIT_UNCERTAIN" {
		t.Fatal("late deadline misreported as success", e)
	}
	observed, e := Read(context.Background(), c)
	if e != nil || observed.Status != "isolated_scope_prepared" {
		t.Fatal("explicit readonly actual-outcome reconciliation failed", observed, e)
	}
	if _, e = Create(context.Background(), c); e == nil {
		t.Fatal("uncertain response blindly replayed")
	}
}
