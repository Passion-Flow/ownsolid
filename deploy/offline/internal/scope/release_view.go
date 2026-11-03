package scope

import (
	"context"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	view "github.com/Passion-Flow/ownsolid/api/release/view"
	"syscall"
)

// This projection is deliberately separate from Status: no credential filename,
// host path, engine response, raw task input or nested recovery object is copied.
func (l *locked) releaseView() view.Snapshot {
	start := len(l.events) - view.MaximumEvents
	if start < 0 {
		start = 0
	}
	events := make([]view.Event, 0, len(l.events)-start)
	for _, e := range l.events[start:] {
		events = append(events, view.Event{Sequence: e.Sequence, OperationID: e.OperationID, Action: e.Action, Outcome: e.Outcome, Code: e.Code, Time: e.Time})
	}
	m := l.marker
	s := view.Snapshot{Format: view.Format, ScopeID: m.ScopeID, ReleaseID: m.ReleaseID, Profile: m.Profile, ArchiveSHA256: m.ArchiveSHA256, ManifestSHA256: m.ManifestSHA256, ConfigurationSHA256: m.ConfigurationSHA256, Sequence: len(l.events), HeadSHA256: l.lastSHA, JournalBytes: l.journalBytes, RecordedAt: events[len(events)-1].Time, FirstSequence: start + 1, Events: events}
	if current := cumulativeUpgradeState(l.events); current.activated && current.record.Installed != nil {
		installed := *current.record.Installed
		s.InstalledRelease = &installed
	}
	return s
}
func syncDirectory(root *os.Root, name string) error {
	f, e := root.Open(name)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// An interruption between the two directory creations leaves only an empty,
// private data directory. It contains no release state and may be completed by
// the fixed repair or next journal append; unknown content remains a refusal.
func (l *locked) verifyReleaseViewInventory() error {
	if _, e := l.root.Lstat(view.RelativeDirectory); os.IsNotExist(e) {
		f, e := l.root.Open("data")
		if e != nil {
			return e
		}
		names, e := f.Readdirnames(-1)
		f.Close()
		if e != nil || len(names) != 0 {
			return failure("OFFLINE_SCOPE_RELEASE_VIEW_DIRECTORY_INVALID")
		}
		return nil
	}
	if info, e := l.root.Lstat(view.RelativeDirectory); e == nil && info.Mode() == os.ModeDir|0700 && owned(info) {
		return l.emptyReleaseViewDirectory()
	}
	r, e := l.releaseViewDirectory(false)
	if e == nil {
		r.Close()
	}
	return e
}
func (l *locked) emptyReleaseViewDirectory() error {
	f, e := l.root.Open(view.RelativeDirectory)
	if e != nil {
		return e
	}
	names, e := f.Readdirnames(-1)
	f.Close()
	if e != nil || len(names) != 0 {
		return failure("OFFLINE_SCOPE_RELEASE_VIEW_DIRECTORY_INVALID")
	}
	return nil
}

// Only this fixed public projection subtree is traversable by a distinct control
// UID. The original scope and data parent remain 0700. Compose mounts this exact
// directory read-only; the control process never receives the private scope.
func (l *locked) releaseViewDirectory(create bool) (*os.Root, error) {
	for _, item := range []struct {
		name string
		mode os.FileMode
	}{{"data", 0700}, {view.RelativeDirectory, 0755}} {
		info, e := l.root.Lstat(item.name)
		if os.IsNotExist(e) && create {
			if e = l.root.Mkdir(item.name, item.mode); e != nil {
				return nil, e
			}
			if e = l.root.Chmod(item.name, item.mode); e != nil {
				return nil, e
			}
			if e = syncDirectory(l.root, filepath.Dir(item.name)); e != nil {
				return nil, e
			}
			info, e = l.root.Lstat(item.name)
		}
		if create && item.name == view.RelativeDirectory && e == nil && info.Mode() == os.ModeDir|0700 && owned(info) {
			if e = l.emptyReleaseViewDirectory(); e != nil {
				return nil, e
			}
			if e = l.root.Chmod(item.name, item.mode); e != nil {
				return nil, e
			}
			if e = syncDirectory(l.root, filepath.Dir(item.name)); e != nil {
				return nil, e
			}
			info, e = l.root.Lstat(item.name)
		}
		if e != nil || info.Mode() != os.ModeDir|item.mode || !owned(info) {
			return nil, failure("OFFLINE_SCOPE_RELEASE_VIEW_DIRECTORY_INVALID")
		}
	}
	data, e := l.root.OpenRoot("data")
	if e != nil {
		return nil, e
	}
	defer data.Close()
	f, e := data.Open(".")
	if e != nil {
		return nil, e
	}
	names, e := f.Readdirnames(-1)
	f.Close()
	if e != nil || len(names) != 1 || names[0] != "release-view" {
		return nil, failure("OFFLINE_SCOPE_RELEASE_VIEW_DIRECTORY_INVALID")
	}
	r, e := l.root.OpenRoot(view.RelativeDirectory)
	if e != nil {
		return nil, e
	}
	f, e = r.Open(".")
	if e != nil {
		r.Close()
		return nil, e
	}
	names, e = f.Readdirnames(-1)
	f.Close()
	if e != nil {
		r.Close()
		return nil, e
	}
	for _, name := range names {
		info, e := r.Lstat(name)
		var links uint64
		if e == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				links = uint64(st.Nlink)
			}
		}
		if e != nil || links != 1 || (name != view.SnapshotFile && name != view.DirtyFile && name != view.PendingFile) || !owned(info) || info.Mode() != 0444 {
			r.Close()
			return nil, failure("OFFLINE_SCOPE_RELEASE_VIEW_CONTENT_INVALID")
		}
	}
	return r, nil
}
func publicWriteNew(r *os.Root, name string, raw []byte) error {
	f, e := r.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0444)
	if e != nil {
		return e
	}
	if e = f.Chmod(0444); e != nil {
		f.Close()
		return e
	}
	n, we := f.Write(raw)
	se := f.Sync()
	ce := f.Close()
	if we != nil || se != nil || ce != nil || n != len(raw) {
		return failure("OFFLINE_SCOPE_RELEASE_VIEW_DURABILITY_UNCERTAIN")
	}
	return nil
}
func (l *locked) beginReleaseView() error {
	r, e := l.releaseViewDirectory(true)
	if e != nil {
		return e
	}
	defer r.Close()
	if _, e = r.Lstat(view.DirtyFile); !os.IsNotExist(e) {
		return failure("OFFLINE_SCOPE_RELEASE_VIEW_RECONCILE_REQUIRED")
	}
	if e = publicWriteNew(r, view.DirtyFile, []byte(l.marker.ScopeID+"\n")); e != nil {
		return e
	}
	return syncDirectory(r, ".")
}
func (l *locked) publishReleaseView() error {
	s := l.releaseView()
	raw := encode(s)
	if s.Validate() != nil || len(raw) > view.MaximumBytes {
		return failure("OFFLINE_SCOPE_RELEASE_VIEW_INVALID")
	}
	r, e := l.releaseViewDirectory(false)
	if e != nil {
		return e
	}
	defer r.Close()
	// A stale pending file is never reused as state. The journal/head above have
	// already been verified under the exclusive scope lock.
	if e = r.Remove(view.PendingFile); e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = publicWriteNew(r, view.PendingFile, raw); e != nil {
		return e
	}
	if e = r.Rename(view.PendingFile, view.SnapshotFile); e != nil {
		return e
	}
	if e = syncDirectory(r, "."); e != nil {
		return e
	}
	if e = r.Remove(view.DirtyFile); e != nil {
		return e
	}
	return syncDirectory(r, ".")
}

// ReconcileReleaseView never executes an Engine/database/restore operation and
// never changes an original receipt or journal event. Invalid heads still refuse.
func ReconcileReleaseView(ctx context.Context, c configuration.Config) (view.Snapshot, error) {
	if ctx == nil || ctx.Err() != nil {
		return view.Snapshot{}, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	l, e := open(c, true)
	if e != nil {
		return view.Snapshot{}, e
	}
	defer l.close()
	r, e := l.releaseViewDirectory(true)
	if e != nil {
		return view.Snapshot{}, e
	}
	_, e = r.Lstat(view.DirtyFile)
	r.Close()
	if os.IsNotExist(e) {
		e = l.beginReleaseView()
	}
	if e != nil {
		return view.Snapshot{}, e
	}
	if e = l.publishReleaseView(); e != nil {
		return view.Snapshot{}, failure("OFFLINE_SCOPE_RELEASE_VIEW_DURABILITY_UNCERTAIN")
	}
	return l.releaseView(), nil
}
