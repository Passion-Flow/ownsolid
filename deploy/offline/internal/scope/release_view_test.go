package scope

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"strings"
	"testing"
)

func TestReleaseViewPublicationRestartAndDirtyRepair(t *testing.T) {
	c := fixture(t)
	ctx := context.Background()
	initial := c.Fingerprint()
	if _, e := Create(ctx, c); e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(c.ScopeRoot, view.RelativeDirectory)
	raw, e := os.ReadFile(filepath.Join(root, view.SnapshotFile))
	if e != nil {
		t.Fatal(e)
	}
	var snapshot view.Snapshot
	if wire.StrictJSON(raw, &snapshot) != nil || snapshot.Validate() != nil || snapshot.Sequence != 3 {
		t.Fatal("actual committed projection", string(raw))
	}
	for _, forbidden := range []string{c.ScopeRoot, c.BundleFile, "bootstrap_credential_file", "configuration_file", "password"} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Fatal("secret or path projected")
		}
	}
	journal, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	head, _ := os.ReadFile(filepath.Join(c.ScopeRoot, headName))
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = l.beginReleaseView(); e != nil {
		t.Fatal(e)
	}
	l.close()
	// Retain an interrupted snapshot write. Reconcile replaces it from the original journal.
	if e = os.WriteFile(filepath.Join(root, view.PendingFile), []byte("incomplete"), 0444); e != nil {
		t.Fatal(e)
	}
	os.Chmod(filepath.Join(root, view.PendingFile), 0444)
	result, e := ReconcileReleaseView(ctx, c)
	if e != nil || result.HeadSHA256 != snapshot.HeadSHA256 {
		t.Fatal("rebuild", e)
	}
	if _, e = os.Lstat(filepath.Join(root, view.DirtyFile)); !os.IsNotExist(e) {
		t.Fatal("dirty remained")
	}
	after, _ := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	afterHead, _ := os.ReadFile(filepath.Join(c.ScopeRoot, headName))
	if !bytes.Equal(journal, after) || !bytes.Equal(head, afterHead) || c.Fingerprint() != initial {
		t.Fatal("repair replayed an event or changed original binding")
	}
	if _, e = Read(ctx, c); e != nil {
		t.Fatal("original readonly restart", e)
	}
}
func TestReleaseViewBoundAndForeignContentRefusal(t *testing.T) {
	c := fixture(t)
	if _, e := Create(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	// Projection-only fixture: no invented journal acceptance. Exercise bound independently.
	events := l.events
	for i := len(events); i < view.MaximumEvents+20; i++ {
		v := events[0]
		v.Sequence = i + 1
		l.events = append(l.events, v)
	}
	s := l.releaseView()
	if len(s.Events) != view.MaximumEvents || s.FirstSequence != 21 || s.Sequence != view.MaximumEvents+20 {
		t.Fatal("silent full-history claim")
	}
	l.events = events
	l.close()
	p := filepath.Join(c.ScopeRoot, view.RelativeDirectory, view.SnapshotFile)
	if e = os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = open(c, false); e == nil {
		t.Fatal("writable public payload admitted")
	}
	os.Chmod(p, 0444)
	if e = os.Link(p, filepath.Join(c.ScopeRoot, view.RelativeDirectory, "foreign")); e != nil {
		t.Fatal(e)
	}
	if _, e = ReconcileReleaseView(context.Background(), c); e == nil || !strings.HasPrefix(e.Error(), "OFFLINE_SCOPE_") {
		t.Fatal("foreign content adopted", e)
	}
}

func TestReleaseViewRepairsOnlyExactEmptyInterruptedDirectories(t *testing.T) {
	c := fixture(t)
	if _, e := Create(context.Background(), c); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(c.ScopeRoot, view.RelativeDirectory)
	journal, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
	if e != nil {
		t.Fatal(e)
	}
	for _, private := range []bool{false, true} {
		if e = os.Remove(filepath.Join(dir, view.SnapshotFile)); e != nil {
			t.Fatal(e)
		}
		if private {
			e = os.Chmod(dir, 0700)
		} else {
			e = os.Remove(dir)
		}
		if e != nil {
			t.Fatal(e)
		}
		if _, e = ReconcileReleaseView(context.Background(), c); e != nil {
			t.Fatal("exact interrupted directory repair", e)
		}
		after, e := os.ReadFile(filepath.Join(c.ScopeRoot, journalName))
		if e != nil || !bytes.Equal(after, journal) {
			t.Fatal("directory repair changed journal", e)
		}
	}
	if e = os.Remove(filepath.Join(dir, view.SnapshotFile)); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "unknown"), []byte("not adopted"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReconcileReleaseView(context.Background(), c); e == nil {
		t.Fatal("unknown private directory content adopted")
	}
}
