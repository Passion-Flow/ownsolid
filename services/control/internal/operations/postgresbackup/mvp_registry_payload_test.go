package postgresbackup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/postgresmaintenance"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestMVPSupplementDoesNotBroadenLegacyArchiveInventory(t *testing.T) {
	path := privateTestRoot(t)
	for _, name := range []string{"stores", "configuration"} {
		if e := os.Mkdir(filepath.Join(path, name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"models", "datasets", "training", "preparations"} {
		if e := os.Mkdir(filepath.Join(path, "stores", name), 0700); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{candidateName, archiveName, effectiveName} {
		if e := os.WriteFile(filepath.Join(path, name), []byte("Synthetic inventory only\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	root, e := openPrivateRoot(path)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e = expectedRoot(root, false); e != nil {
		t.Fatal("historical inventory changed", e)
	}
	if e = os.Mkdir(filepath.Join(path, mvpArtifactsDirectory), 0700); e != nil {
		t.Fatal(e)
	}
	if e = expectedRoot(root, false); e == nil {
		t.Fatal("supplement injected into legacy archive")
	}
	if e = expectedRootProfile(root, false, postgresmaintenance.MVPApplicationProfile); e != nil {
		t.Fatal("explicit MVP artifact boundary missing", e)
	}
	m := mvpBindingManifestFixture()
	m.Request.BackupRoot = path
	if e = verifyMVPRegistryPayload(t.Context(), m); e != nil {
		t.Fatal("explicit disabled/empty registry boundary", e)
	}
	m.MVPSource.RegistryConfigured = true
	if e = verifyMVPRegistryPayload(t.Context(), m); e == nil || e.Error() != "POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED" {
		t.Fatal("enabled registry metadata completed without payload", e)
	}
}

func TestRegistrySupplementCannotEnterLegacyRestoreOrAliasDomainStore(t *testing.T) {
	i := RestoreIntent{Request: restoreRequestFixture()}
	f := RestoreFiles{Registry: &imageregistry.OperatorSnapshotImportReceipt{Complete: true}}
	if e := readbackMVPRegistryImport(context.Background(), config.Config{}, Manifest{}, i, f); e == nil || e.Error() != "POSTGRES_RESTORE_MVP_REGISTRY_RECORD_MISMATCH" {
		t.Fatal("supplement injected into historical restore", e)
	}
	fixture := newRestoreInputFixture(t)
	m := mvpBindingManifestFixture()
	m.MVPSource.Registry = &MVPRegistryPayload{}
	fixture.request.ApplicationProfile = postgresmaintenance.MVPApplicationProfile
	fixture.runtime.ImageRegistry = &imageregistry.Config{StoreRoot: fixture.runtime.Models.ArtifactRoot}
	if _, e := mvpRegistryTargetRoot(fixture.runtime, m, fixture.request, true); e == nil || e.Error() != "POSTGRES_RESTORE_RELATED_PATHS_REFUSED" {
		t.Fatal("registry artifact overwrote a domain store", e)
	}
}

func TestMVPSupplementSharesOriginalAggregatePayloadBudget(t *testing.T) {
	m := mvpBindingManifestFixture()
	m.Request.Policy.MaximumStoreBytes = 20
	m.Request.Policy.MaximumStoreFiles = 5
	m.Stores = []StoreReadback{{Kind: "models", Tree: TreeReadback{Bytes: 12, Files: 3}}}
	m.MVPSource.Registry = &MVPRegistryPayload{Tree: TreeReadback{Bytes: 8, Files: 2}}
	if e := mvpRegistryPayloadLimits(m); e != nil {
		t.Fatal("exact shared budget", e)
	}
	m.MVPSource.Registry.Tree.Bytes = 9
	if e := mvpRegistryPayloadLimits(m); e == nil {
		t.Fatal("supplement exceeded aggregate byte budget")
	}
	m.MVPSource.Registry.Tree.Bytes = 8
	m.MVPSource.Registry.Tree.Files = 3
	if e := mvpRegistryPayloadLimits(m); e == nil {
		t.Fatal("supplement exceeded aggregate file budget")
	}
}

func TestMVPSupplementRequiresFreshDistinctPinnedTargetDirectory(t *testing.T) {
	f := newRestoreInputFixture(t)
	f.request.ApplicationProfile = postgresmaintenance.MVPApplicationProfile
	path := privateTestRoot(t)
	f.runtime.ImageRegistry = &imageregistry.Config{StoreRoot: path}
	m := mvpBindingManifestFixture()
	m.MVPSource.RegistryConfigured = true
	m.MVPSource.Registry = &MVPRegistryPayload{Receipt: imageregistry.OperatorSnapshotReceipt{SourceRoot: imageregistry.OperatorSnapshotRoot{Path: "/synthetic/source-registry", Device: 1, Inode: 1}}}
	first, e := mvpRegistryTargetRoot(f.runtime, m, f.request, true)
	if e != nil || first == nil || first.Path != path || first.Inode == 0 {
		t.Fatal("fresh independent target root", e)
	}
	if e = os.WriteFile(filepath.Join(path, "existing-partial-data"), []byte("retained recovery state\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = mvpRegistryTargetRoot(f.runtime, m, f.request, true); e == nil || e.Error() != "POSTGRES_RESTORE_MVP_FRESH_REGISTRY_ROOT_REQUIRED" {
		t.Fatal("partial target was admitted for a new import", e)
	}
	current, e := mvpRegistryTargetRoot(f.runtime, m, f.request, false)
	if e != nil || current == nil || *current != *first {
		t.Fatal("read-only observation lost original directory identity", e)
	}
	m.MVPSource.Registry.Receipt.SourceRoot.Device = first.Device
	m.MVPSource.Registry.Receipt.SourceRoot.Inode = first.Inode
	if _, e = mvpRegistryTargetRoot(f.runtime, m, f.request, false); e == nil || e.Error() != "POSTGRES_RESTORE_MVP_SOURCE_REGISTRY_ALIAS" {
		t.Fatal("source directory identity reused by target", e)
	}
}

func TestMVPSupplementCannotInventAConfiguredRegistryOwner(t *testing.T) {
	m := mvpBindingManifestFixture()
	m.MVPSource.Registry = &MVPRegistryPayload{}
	if e := validMVPSourceBinding(m); e == nil || e.Error() != "POSTGRES_BACKUP_MVP_REGISTRY_PAYLOAD_OWNER_REQUIRED" {
		t.Fatal("payload supplied without an actual configured source owner", e)
	}
}
