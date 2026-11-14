package mysqlbackup

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBackupManifestPrivateFileRetainsTimestampRoundtrip(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	want := parserManifest()
	policy := parserBackupPolicy()
	if err = writeDocument(root, manifestName, want, policy.MaximumManifestBytes); err != nil {
		t.Fatal(err)
	}
	var actual BackupManifest
	if _, err = readPrivateDocument(filepath.Join(directory, manifestName), policy.MaximumManifestBytes, &actual); err != nil {
		t.Fatal("actual persisted typed manifest could not be reopened", err)
	}
	if !reflect.DeepEqual(actual, want) || actual.validate(policy) != nil {
		t.Fatal("persisted manifest timestamp or contract changed")
	}
}
