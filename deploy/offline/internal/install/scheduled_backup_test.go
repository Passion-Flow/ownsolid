package install

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestScheduledBackupRetentionRequiresAnOwnedArchive(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const prefix = "ownsolid-scheduled-fixture-"
	owner := []byte("test installation ownership")
	older, newest := prefix+"20261108T000001Z", prefix+"20261108T000004Z"
	unowned, partial := prefix+"20261108T000002Z", prefix+"20261108T000003Z.partial"
	manual := backupPrefix + "20261108T000000Z"
	for _, name := range []string{older, newest, unowned, partial, manual} {
		if err := root.Mkdir(name, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{older, newest, partial} {
		if err := root.WriteFile(filepath.Join(name, scheduleOwnerFile), owner, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	count, err := expireScheduledBackups(t.Context(), root, prefix, newest, 1, owner)
	if err != nil || count != 1 {
		t.Fatalf("expected one expired backup, got %d: %v", count, err)
	}
	if _, err := root.Stat(older); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the older owned archive remains: %v", err)
	}
	for _, name := range []string{newest, unowned, partial, manual} {
		if _, err := root.Stat(name); err != nil {
			t.Fatalf("the scheduler removed %s: %v", name, err)
		}
	}
}

func TestScheduledBackupConfigurationIsValidated(t *testing.T) {
	base := validYAML(t, filepath.Join(shortTempDir(t), "install"))
	for _, test := range []string{
		"backup_schedule: {directory: relative, retain: 2}",
		"backup_schedule: {directory: /private/backups, retain: 0}",
		"backup_schedule: {directory: /private/backups, retain: 1001}",
	} {
		if _, err := Decode([]byte(base + test + "\n")); err == nil || !strings.Contains(err.Error(), "backup_schedule") {
			t.Fatalf("invalid configuration was accepted: %v", err)
		}
	}
}

func scheduledArchives(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "ownsolid-scheduled-") {
			result = append(result, entry.Name())
		}
	}
	return result
}

type failScheduledDump struct{ ExecRunner }

func (r failScheduledDump) Stream(ctx context.Context, input io.Reader, output io.Writer, name string, args ...string) (string, error) {
	if name == "docker" && slices.Contains(args, "pg_dump") {
		return "injected PostgreSQL dump failure", errors.New("dump failed")
	}
	return r.ExecRunner.Stream(ctx, input, output, name, args...)
}
