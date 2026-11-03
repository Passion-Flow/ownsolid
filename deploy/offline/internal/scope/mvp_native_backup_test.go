package scope

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestMVPNativeReservationPreservesOriginalInputsAndUncertainPartial(t *testing.T) {
	p := mvpNativeSourcePlanFixture()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	p.Request.DestinationRoot = filepath.Join(dir, "backup")
	release := []byte("synthetic source declaration only\n")
	p.ReleaseManifestSHA256 = wire.Digest(release)
	p.NativeRequest.SourceManifestSHA256 = p.ReleaseManifestSHA256
	p.ReleaseViewBinding.ManifestSHA256 = p.ReleaseManifestSHA256
	contents := []byte("original keyring bytes\n")
	p.ConfigurationFiles = []BackupConfigurationFile{{Role: "control-config", File: InputFile{Name: "mail-keyring.json", Bytes: int64(len(contents)), SHA256: wire.Digest(contents)}}}
	descriptor := encode(InputsDescriptor{FormatVersion: 1, ScopeID: p.ScopeID, ConfigurationSHA256: p.ConfigurationSHA256, StorageProfile: namedVolumeStorage, Roles: []InputRole{{Key: "operator-config", UID: p.NativeUID, GID: p.NativeGID}}})
	p.DescriptorSHA256 = wire.Digest(descriptor)
	p.Storage, _ = makeMVPNativeBackupStorage(p, 4096, 1024, 1<<20, 1<<20, 1<<20, 1<<20)
	files := map[string][]engine.ConfigurationFile{"control-config": {{Name: "mail-keyring.json", Contents: contents}}}
	if e := reserveMVPNativeBackupFiles(p, files, release, []byte("synthetic signature declaration only\n"), descriptor); e != nil {
		t.Fatal(e)
	}
	root, e := openBackupDirectory(p.Request.DestinationRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	original, e := regular(root, backupPlanFile, 1<<20)
	var retained mvpNativeBackupPlan
	if e != nil || wire.StrictJSON(original, &retained) != nil || !reflect.DeepEqual(retained, p) {
		t.Fatal(e)
	}
	if _, e := root.Lstat(mvpNativeDirectory); !os.IsNotExist(e) {
		t.Fatal("reservation fabricated native completion/child")
	}
	if e := reserveMVPNativeBackupFiles(p, files, release, []byte("signature"), descriptor); e == nil {
		t.Fatal("uncertain reservation replayed")
	}
	after, e := regular(root, "configuration/control-config/mail-keyring.json", 1<<20)
	if e != nil || wire.Digest(after) != wire.Digest(contents) {
		t.Fatal("original bytes changed", e)
	}
	if _, e := publishMVPNativeBackupManifest(context.Background(), root, p, configuration.Defaults(), []byte("metadata-only")); e == nil {
		t.Fatal("metadata-only unverified source published")
	}
	if _, e := root.Lstat(backupManifestFile); !os.IsNotExist(e) {
		t.Fatal("missing original proof repaired by publication")
	}
}

func TestMVPNativePublicationConfirmsExactOriginalButNeverRepairsChangedBytes(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	root, e := os.OpenRoot(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	raw := []byte("original private stdout with exact final LF\n")
	if e := confirmMVPNativePublication(root, mvpNativeObservationFile, raw, 4096); e != nil {
		t.Fatal(e)
	}
	before, e := root.Stat(mvpNativeObservationFile)
	if e != nil {
		t.Fatal(e)
	}
	if e := confirmMVPNativePublication(root, mvpNativeObservationFile, raw, 4096); e != nil {
		t.Fatal(e)
	}
	after, e := root.Stat(mvpNativeObservationFile)
	if e != nil || !os.SameFile(before, after) {
		t.Fatal("original identity replaced", e)
	}
	if e := confirmMVPNativePublication(root, mvpNativeObservationFile, []byte("fresh different observation\n"), 4096); e == nil {
		t.Fatal("new observation replaced immutable original")
	}
	os.WriteFile(filepath.Join(dir, "partial"), []byte("partial"), 0600)
	if e := confirmMVPNativePublication(root, "partial", raw, 4096); e == nil {
		t.Fatal("partial uncertain publication repaired")
	}
	retained, _ := os.ReadFile(filepath.Join(dir, "partial"))
	if string(retained) != "partial" {
		t.Fatal("partial evidence lost")
	}
}
