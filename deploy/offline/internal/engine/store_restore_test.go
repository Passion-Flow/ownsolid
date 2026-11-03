package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestStoreRestoreArgumentsConfirmOnlyFixedTrainingAndPreparationTargets(t *testing.T) {
	const reason = "Explicit isolated restore argument regression"
	for _, test := range []struct {
		kind string
		args []string
	}{
		{ModelFilesImport, []string{"--backup-root", "/var/lib/projectone/files-restore/snapshot", "--reason", reason}},
		{DatasetFilesImport, []string{"--backup-root", "/var/lib/projectone/files-restore/snapshot", "--reason", reason}},
		{TrainingFilesImport, []string{"--backup-root", "/var/lib/projectone/files-restore/snapshot", "--training-store-root", "/var/lib/projectone/training/private", "--confirm-empty-training-store", "--reason", reason}},
		{PreparationFilesImport, []string{"--backup-root", "/var/lib/projectone/files-restore/snapshot", "--preparation-store-root", "/var/lib/projectone/preparations/private", "--confirm-empty-preparation-store", "--reason", reason}},
		{ModelFilesVerify, []string{}},
		{DatasetFilesVerify, []string{}},
		{TrainingFilesVerify, []string{"--reason", reason}},
		{PreparationFilesVerify, []string{"--reason", reason}},
	} {
		t.Run(test.kind, func(t *testing.T) {
			task := storeRestoreTaskFixture(t, test.kind)
			if p, _ := storeRestoreTask(test.kind); p.reason {
				task.Reason = reason
			}
			if got := task.storeRestoreArguments(); !reflect.DeepEqual(got, test.args) {
				t.Fatalf("fixed backend arguments: got %q, want %q", got, test.args)
			}
			command := task.command()
			if len(command) < len(test.args) || !reflect.DeepEqual(command[len(command)-len(test.args):], test.args) {
				t.Fatalf("fixed arguments missing from actual task command: %q", command)
			}
		})
	}
}

// These fixtures exercise transport/task guards only. Genuine backend imports
// and restored SQL/namespace/audit behavior require the separate actual gate.
func storeRestoreTaskFixture(t *testing.T, kind string) DatabaseTask {
	t.Helper()
	_, base, _, _, _ := databaseTaskFixture(t, "", DatasetFilesExport)
	p, ok := storeRestoreTask(kind)
	if !ok {
		t.Fatal("fixed test kind")
	}
	base.Kind, base.Owner.Role = kind, "database-"+kind
	base.VolumeOwner.Role = StoreRestoreConfigRole + "-" + base.Owner.OperationID
	base.OutputOwner = &Ownership{base.Owner.ScopeID, strings.Repeat("f", 32), p.role}
	base.SourceOwner = nil
	base.Reason = ""
	if p.reason {
		base.Reason = "Synthetic isolated store restoration"
	}
	if p.importFiles {
		base.SourceOwner = &Ownership{base.Owner.ScopeID, base.Owner.OperationID, StoreRestoreArchiveRole + "-" + p.role + "-" + base.Owner.OperationID}
		base.StoreRestore = &StoreRestoreProfile{Artifact: StoreRestoreArtifact{Role: p.role, UID: base.UID, GID: base.GID, Bytes: 3072, SHA256: strings.Repeat("a", 64), MaximumEntries: 100}, FilesSHA256: strings.Repeat("b", 64), DirectoriesSHA256: strings.Repeat("c", 64)}
	}
	return base
}

func TestStoreRestoreFixedKindsIdentityMountsAndLegacyEncoding(t *testing.T) {
	for _, kind := range []string{ModelFilesImport, DatasetFilesImport, TrainingFilesImport, PreparationFilesImport, ModelFilesVerify, DatasetFilesVerify, TrainingFilesVerify, PreparationFilesVerify} {
		t.Run(kind, func(t *testing.T) {
			task := storeRestoreTaskFixture(t, kind)
			if !task.valid() || task.ValidateRestoredStoreTask() != nil {
				t.Fatal("fixed restore refused")
			}
			p, _ := storeRestoreTask(kind)
			command := task.command()
			if command[0] != kind || strings.Contains(strings.Join(command, " "), "/bin/sh") {
				t.Fatal("nonfixed command")
			}
			if p.importFiles {
				if !strings.Contains(strings.Join(command, " "), "--backup-root "+StoreRestoreSourceDestination+"/snapshot") {
					t.Fatal("source path override")
				}
				mounts := task.databaseMountProfiles()
				if len(mounts) != 3 || mounts[2].target != StoreRestoreSourceDestination || !mounts[2].readOnly || mounts[1].readOnly {
					t.Fatal("unsafe import mounts")
				}
			} else if len(task.databaseMountProfiles()) != 2 || task.SourceOwner != nil {
				t.Fatal("verification source mount")
			}
		})
	}
	_, old, _, _, _ := databaseTaskFixture(t, "", DatasetFilesExport)
	raw, _ := json.Marshal(old)
	if bytes.Contains(raw, []byte("store_restore")) {
		t.Fatal("old record encoding changed")
	}
	old.StoreRestore = &StoreRestoreProfile{}
	if old.valid() {
		t.Fatal("restore descriptor on export")
	}
	changes := map[string]func(*DatabaseTask){
		"source-missing":    func(v *DatabaseTask) { v.SourceOwner = nil },
		"source-generation": func(v *DatabaseTask) { v.SourceOwner.OperationID = strings.Repeat("e", 32) },
		"source-live-role":  func(v *DatabaseTask) { v.SourceOwner.Role = "datasets" },
		"target-wrong-role": func(v *DatabaseTask) { v.OutputOwner.Role = "models" },
		"config-role":       func(v *DatabaseTask) { v.VolumeOwner.Role = "operator-config" },
		"config-generation": func(v *DatabaseTask) { v.VolumeOwner.OperationID = strings.Repeat("e", 32) },
		"source-uid":        func(v *DatabaseTask) { v.StoreRestore.Artifact.UID++ },
		"directory-hash":    func(v *DatabaseTask) { v.StoreRestore.DirectoriesSHA256 = "" },
		"foreign-target":    func(v *DatabaseTask) { v.OutputOwner.ScopeID = strings.Repeat("e", 32) },
		"bad-reason":        func(v *DatabaseTask) { v.Reason = "bad\nreason" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			task := storeRestoreTaskFixture(t, DatasetFilesImport)
			change(&task)
			if task.valid() {
				t.Fatal("unsafe restore context")
			}
		})
	}
}

func TestStoreRestoreProtectedDescriptionAndUnsafeArchiveRefusal(t *testing.T) {
	for _, fault := range []string{"", "owner", "mode", "directory-mode", "missing", "escape", "parent", "link", "duplicate", "pax", "end", "tail"} {
		t.Run(fault, func(t *testing.T) {
			task, raw := storeBackupArchiveFixture(t, fault)
			name := filepath.Join(t.TempDir(), "source.tar")
			if os.WriteFile(name, raw, 0600) != nil {
				t.Fatal("fixture")
			}
			f, e := os.Open(name)
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			r := StoreRestoreArtifact{Role: "datasets", UID: task.UID, GID: task.GID, Bytes: int64(len(raw)), SHA256: wire.Digest(raw), MaximumEntries: 100}
			profile, e := DescribeStoreRestoreArchive(context.Background(), f, r)
			if fault != "" {
				if e == nil {
					t.Fatal("unsafe artifact accepted")
				}
				return
			}
			if e != nil || !profile.valid() {
				t.Fatal("safe description", e)
			}
			r.SHA256 = strings.Repeat("f", 64)
			if _, e = DescribeStoreRestoreArchive(context.Background(), f, r); e == nil {
				t.Fatal("wrong independent digest")
			}
			r = profile.Artifact
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, e = DescribeStoreRestoreArchive(ctx, f, r); e == nil {
				t.Fatal("canceled read")
			}
			os.Chmod(name, 0644)
			if _, e = DescribeStoreRestoreArchive(context.Background(), f, r); e == nil {
				t.Fatal("public input")
			}
			os.Chmod(name, 0600)
			if os.Link(name, name+"-linked") != nil {
				t.Fatal("fixture")
			}
			if _, e = DescribeStoreRestoreArchive(context.Background(), f, r); e == nil {
				t.Fatal("linked input")
			}
		})
	}
}

func TestStoreRestoreCopiedDirectoriesAreBoundAlongsideFiles(t *testing.T) {
	task, raw := storeBackupArchiveFixture(t, "")
	r := StoreRestoreArtifact{Role: "datasets", UID: task.UID, GID: task.GID, Bytes: int64(len(raw)), SHA256: wire.Digest(raw), MaximumEntries: 100}
	_, profile, e := readStoreRestoreSnapshot(bytes.NewReader(raw), r)
	if e != nil {
		t.Fatal(e)
	}
	for _, mutation := range []string{"unchanged", "extra-directory", "file-mode"} {
		var copied bytes.Buffer
		tw := tar.NewWriter(&copied)
		tw.WriteHeader(&tar.Header{Name: "files-restore", Typeflag: tar.TypeDir, Mode: 0755})
		tr := tar.NewReader(bytes.NewReader(raw))
		for {
			h, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				t.Fatal(e)
			}
			h.Name = "files-restore/" + h.Name
			if mutation == "file-mode" && h.Name == "files-restore/snapshot/objects/data" {
				h.Mode = 0444
			}
			if tw.WriteHeader(h) != nil {
				t.Fatal("header")
			}
			if _, e = io.Copy(tw, tr); e != nil {
				t.Fatal(e)
			}
		}
		if mutation == "extra-directory" {
			tw.WriteHeader(&tar.Header{Name: "files-restore/snapshot/extra-empty", Typeflag: tar.TypeDir, Mode: 0700, Uid: task.UID, Gid: task.GID})
		}
		tw.Close()
		dirs := []string{}
		modes := map[string]int64{}
		archive, e := readStoreSnapshotArchive(bytes.NewReader(copied.Bytes()), io.Discard, r.Role, r.UID, r.GID, 1<<20, r.MaximumEntries, &dirs, modes, "files-restore")
		if e != nil {
			t.Fatal(e)
		}
		f, d := storeRestoreInventoryDigests(archive.Files, dirs, modes)
		if (f == profile.FilesSHA256) != (mutation != "file-mode") || (d == profile.DirectoriesSHA256) != (mutation != "extra-directory") {
			t.Fatal("directory or file-mode mutation not distinguished from identical file bytes")
		}
	}
}
