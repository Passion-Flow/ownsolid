package scope

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/composeprofile"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func composePrivateFixture(t *testing.T) composeprofile.PrivateConfiguration {
	t.Helper()
	base, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	source, target := filepath.Join(base, "source"), filepath.Join(base, "target")
	for path, mode := range map[string]os.FileMode{source: 0700, target: 0755} {
		if e = os.Mkdir(path, mode); e != nil {
			t.Fatal(e)
		}
		if e = os.Chmod(path, mode); e != nil {
			t.Fatal(e)
		}
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid = 10033
	}
	if gid == 0 {
		gid = 10034
	}
	raw := []byte("synthetic private configuration\n")
	if e = os.WriteFile(filepath.Join(source, "control.yaml"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	return composeprofile.PrivateConfiguration{FormatVersion: 1, ScopeID: strings.Repeat("a", 32), OperationID: strings.Repeat("b", 32), VolumeName: "projectone-" + strings.Repeat("a", 32) + "-control-config", SourceRoot: source, TargetRoot: target, SourceUID: os.Geteuid(), SourceGID: os.Getegid(), UID: uid, GID: gid, MaximumFileBytes: 1024, MaximumTotalBytes: 4096, TimeoutSeconds: 30, Files: []composeprofile.PrivateConfigurationFile{{Name: "control.yaml", Bytes: int64(len(raw)), SHA256: wire.Digest(raw)}}}
}

func TestComposePrivateConfigurationPublicationAndReadback(t *testing.T) {
	p := composePrivateFixture(t)
	ctx := context.Background()
	result, e := runComposePrivateConfiguration(ctx, p, "install")
	if e != nil || result.Status != "verified_private_configuration" || result.Files != 1 {
		t.Fatal(result, e)
	}
	for _, action := range []string{"inspect", "recover"} {
		result, e = runComposePrivateConfiguration(ctx, p, action)
		expected := "verified_private_configuration"
		if action == "inspect" {
			expected = "observed_private_configuration"
		}
		if e != nil || result.Status != expected {
			t.Fatal(action, result, e)
		}
	}
	if _, e = runComposePrivateConfiguration(ctx, p, "install"); e == nil {
		t.Fatal("repeated installation accepted")
	}
	lock, e := os.Open(filepath.Join(p.TargetRoot, composePrivateJournal, "lock"))
	if e != nil {
		t.Fatal(e)
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		t.Fatal(e)
	}
	_, e = runComposePrivateConfiguration(ctx, p, "inspect")
	syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	lock.Close()
	if e == nil || e.Error() != "OFFLINE_SCOPE_PRIVATE_CONFIGURATION_LOCKED" {
		t.Fatal("inspection ignored held journal lock", e)
	}
	other := p
	other.OperationID = strings.Repeat("c", 32)
	if _, e = runComposePrivateConfiguration(ctx, other, "recover"); e == nil {
		t.Fatal("wrong intent recovered")
	}
	if e = os.RemoveAll(p.SourceRoot); e != nil {
		t.Fatal(e)
	}
	if _, e = runComposePrivateConfiguration(ctx, p, "inspect"); e != nil {
		t.Fatal("inspection depended on old source", e)
	}
}

func TestComposePrivateConfigurationRejectsUnsafeSources(t *testing.T) {
	for _, fault := range []string{"symlink", "hardlink", "fifo", "mode", "hash", "extra", "target-foreign", "source-link", "cancel"} {
		t.Run(fault, func(t *testing.T) {
			p := composePrivateFixture(t)
			name := filepath.Join(p.SourceRoot, "control.yaml")
			ctx := context.Background()
			switch fault {
			case "symlink":
				original := filepath.Join(filepath.Dir(p.SourceRoot), "original-config")
				os.Rename(name, original)
				os.Symlink(original, name)
			case "hardlink":
				os.Link(name, filepath.Join(filepath.Dir(p.SourceRoot), "other"))
			case "fifo":
				os.Remove(name)
				if e := syscall.Mkfifo(name, 0600); e != nil {
					t.Fatal(e)
				}
			case "mode":
				os.Chmod(name, 0644)
			case "hash":
				raw, _ := os.ReadFile(name)
				raw[0] ^= 1
				os.WriteFile(name, raw, 0600)
			case "extra":
				os.WriteFile(filepath.Join(p.SourceRoot, "extra"), []byte("x"), 0600)
			case "target-foreign":
				os.WriteFile(filepath.Join(p.TargetRoot, "foreign"), []byte("preserve"), 0600)
			case "source-link":
				original := p.SourceRoot
				os.Rename(original, original+"-old")
				os.Symlink(original+"-old", original)
			case "cancel":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, e := runComposePrivateConfiguration(ctx, p, "install"); e == nil {
				t.Fatal("accepted unsafe source")
			}
			if _, e := os.Lstat(filepath.Join(p.TargetRoot, "private")); !os.IsNotExist(e) {
				t.Fatal("published failed input")
			}
		})
	}
}

func TestComposePrivateConfigurationExplicitCrashRecovery(t *testing.T) {
	for _, phase := range []string{"ready", "renamed", "incomplete", "tampered", "foreign"} {
		t.Run(phase, func(t *testing.T) {
			p := composePrivateFixture(t)
			ctx := context.Background()
			if _, e := runComposePrivateConfiguration(ctx, p, "install"); e != nil {
				t.Fatal(e)
			}
			journal := filepath.Join(p.TargetRoot, composePrivateJournal)
			if e := os.Remove(filepath.Join(journal, "committed.json")); e != nil {
				t.Fatal(e)
			}
			if phase != "renamed" {
				if e := os.Rename(filepath.Join(p.TargetRoot, "private"), filepath.Join(journal, "stage")); e != nil {
					t.Fatal(e)
				}
			}
			switch phase {
			case "incomplete":
				os.Remove(filepath.Join(journal, "ready.json"))
			case "tampered":
				os.WriteFile(filepath.Join(journal, "stage/control.yaml"), []byte("changed"), 0600)
			case "foreign":
				os.Mkdir(filepath.Join(p.TargetRoot, "private"), 0700)
			}
			result, e := runComposePrivateConfiguration(ctx, p, "inspect")
			if phase == "ready" || phase == "renamed" || phase == "incomplete" {
				if e != nil {
					t.Fatal(e)
				}
				if result.Status == "verified_private_configuration" {
					t.Fatal("uncommitted state accepted")
				}
			}
			if phase == "ready" {
				if _, e := os.Lstat(filepath.Join(p.TargetRoot, "private")); !os.IsNotExist(e) {
					t.Fatal("inspection published staging")
				}
			}
			result, e = runComposePrivateConfiguration(ctx, p, "recover")
			if phase == "ready" || phase == "renamed" {
				if e != nil || result.Status != "verified_private_configuration" {
					t.Fatal(result, e)
				}
			} else if e == nil {
				t.Fatal("unsafe recovery accepted")
			}
		})
	}
}

func TestComposePrivateConfigurationIntentContainsNoPayload(t *testing.T) {
	p := composePrivateFixture(t)
	if _, e := runComposePrivateConfiguration(context.Background(), p, "install"); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(p.TargetRoot, composePrivateIntent))
	if e != nil {
		t.Fatal(e)
	}
	var value composeprofile.PrivateConfiguration
	if json.Unmarshal(raw, &value) != nil || value.Fingerprint() != p.Fingerprint() || strings.Contains(string(raw), "synthetic private configuration") {
		t.Fatal("invalid intent")
	}
}

type privateCommitSyncFault struct {
	fail               string
	markerSeen         bool
	failed             bool
	files, directories []string
}

func (f *privateCommitSyncFault) File(file *os.File) error {
	name := filepath.Base(filepath.Clean(file.Name()))
	f.files = append(f.files, name)
	if name == "committed.json" {
		f.markerSeen = true
		if f.fail == "marker" {
			f.failed = true
			return os.ErrInvalid
		}
	}
	return file.Sync()
}
func (f *privateCommitSyncFault) Directory(file *os.File) error {
	name := filepath.Base(filepath.Clean(file.Name()))
	f.directories = append(f.directories, name)
	if name == composePrivateJournal && f.markerSeen && f.fail == "journal" {
		f.failed = true
		return os.ErrInvalid
	}
	return file.Sync()
}

func TestComposePrivateConfigurationRecoversVisibleUnsyncedCommit(t *testing.T) {
	for _, phase := range []string{"marker", "journal"} {
		t.Run(phase, func(t *testing.T) {
			p := composePrivateFixture(t)
			ctx := context.Background()
			fault := &privateCommitSyncFault{fail: phase}
			if _, e := runComposePrivateConfigurationWithDurability(ctx, p, "install", fault); e == nil || !fault.failed {
				t.Fatal("injected durability failure was not retained", e)
			}
			marker := filepath.Join(p.TargetRoot, composePrivateCommitted)
			before, e := os.Stat(marker)
			if e != nil {
				t.Fatal("marker must be visible after failed fsync", e)
			}
			privateBefore, e := os.Stat(filepath.Join(p.TargetRoot, "private"))
			if e != nil {
				t.Fatal(e)
			}
			raw, e := os.ReadFile(marker)
			if e != nil || string(raw) != p.Fingerprint()+"\n" {
				t.Fatal("expected hash-correct visible marker", e)
			}
			observer := &privateCommitSyncFault{fail: phase}
			result, e := runComposePrivateConfigurationWithDurability(ctx, p, "inspect", observer)
			if e != nil || result.Status != "observed_private_configuration" || len(observer.files) != 0 || len(observer.directories) != 0 {
				t.Fatal("inspection claimed durability or performed sync", result, e)
			}
			retryFault := &privateCommitSyncFault{fail: phase}
			if _, e = runComposePrivateConfigurationWithDurability(ctx, p, "recover", retryFault); e == nil || !retryFault.failed {
				t.Fatal("recovery skipped failed marker/journal sync", e)
			}
			recovery := &privateCommitSyncFault{}
			result, e = runComposePrivateConfigurationWithDurability(ctx, p, "recover", recovery)
			if e != nil || result.Status != "verified_private_configuration" || len(recovery.files) != 1 || recovery.files[0] != "committed.json" || len(recovery.directories) != 3 {
				t.Fatal("explicit recovery did not confirm durable marker and parents", result, recovery, e)
			}
			after, e := os.Stat(marker)
			if e != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
				t.Fatal("recovery rewrote committed marker", e)
			}
			privateAfter, e := os.Stat(filepath.Join(p.TargetRoot, "private"))
			if e != nil || !os.SameFile(privateBefore, privateAfter) {
				t.Fatal("recovery replayed private publication", e)
			}
		})
	}
}
