package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestStoreBackupFixedTasksAndOnceOnlyStart(t *testing.T) {
	for _, kind := range []string{ModelFilesExport, DatasetFilesExport, TrainingFilesExport, PreparationFilesExport} {
		t.Run(kind, func(t *testing.T) {
			c, task, state, posts, files := databaseTaskFixture(t, "", kind)
			p, _ := storeBackupTask(kind)
			if !task.valid() || task.SourceOwner.Role != p.role || !strings.Contains(strings.Join(task.command(), " "), "--backup-root /var/lib/projectone/files-backup/private/snapshot --reason Synthetic isolated store snapshot") {
				t.Fatal("fixed snapshot contract")
			}
			object, e := c.CreateDatabaseTask(context.Background(), task)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = c.startDatabaseTask(context.Background(), task, object.ID, files, 30, databaseAdmit); e != nil {
				t.Fatal(e)
			}
			state.Store(3)
			if _, e = c.startDatabaseTask(context.Background(), task, object.ID, files, 30, databaseAdmit); e == nil || posts.Load() != 2 {
				t.Fatal("terminal export replay")
			}
			if e = c.RemoveDatabaseTask(context.Background(), task, object.ID); e != nil {
				t.Fatal(e)
			}
		})
	}
}

func TestStoreBackupOwnershipMountAndOutputGuards(t *testing.T) {
	for _, change := range []func(*DatabaseTask){
		func(v *DatabaseTask) { v.SourceOwner = nil }, func(v *DatabaseTask) { v.SourceOwner.Role = "models" },
		func(v *DatabaseTask) { v.VolumeOwner.OperationID = strings.Repeat("f", 32) }, func(v *DatabaseTask) { v.OutputOwner.OperationID = strings.Repeat("f", 32) },
		func(v *DatabaseTask) { v.VolumeOwner.Role = "operator-config" }, func(v *DatabaseTask) { v.OutputOwner.Role = "datasets" },
		func(v *DatabaseTask) { v.SourceOwner.ScopeID = strings.Repeat("f", 32) }, func(v *DatabaseTask) { v.Reason = "bad\nreason" },
	} {
		_, task, _, _, _ := databaseTaskFixture(t, "", DatasetFilesExport)
		change(&task)
		if task.valid() {
			t.Fatal("unbound export accepted")
		}
	}
	for _, fault := range []string{"backup-foreign-source-consumer", "backup-source-mount", "backup-nonempty", "control-running"} {
		t.Run(fault, func(t *testing.T) {
			c, task, _, posts, files := databaseTaskFixture(t, fault, DatasetFilesExport)
			obj, e := c.CreateDatabaseTask(context.Background(), task)
			if e == nil {
				_, e = c.startDatabaseTask(context.Background(), task, obj.ID, files, 30, databaseAdmit)
			}
			if e == nil || posts.Load() > 1 {
				t.Fatal("unsafe export started", e)
			}
		})
	}
	_, ordinary, _, _, _ := databaseTaskFixture(t, "")
	raw, _ := json.Marshal(ordinary)
	if bytes.Contains(raw, []byte("source_owner")) {
		t.Fatal("old records changed")
	}
	ordinary.SourceOwner = &Ownership{ordinary.Owner.ScopeID, ordinary.Owner.OperationID, "models"}
	if ordinary.valid() {
		t.Fatal("source mount on old task")
	}
}

// Explicit synthetic transport fixture, not real product snapshot semantics.
func storeBackupArchiveFixture(t *testing.T, fault string) (DatabaseTask, []byte) {
	t.Helper()
	_, task, _, _, _ := databaseTaskFixture(t, "", DatasetFilesExport)
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	headers := []tar.Header{{Name: "snapshot", Typeflag: tar.TypeDir, Mode: 0700}, {Name: "snapshot/snapshot.json", Typeflag: tar.TypeReg, Mode: 0600, Size: 2}, {Name: "snapshot/objects", Typeflag: tar.TypeDir, Mode: 0700}, {Name: "snapshot/objects/data", Typeflag: tar.TypeReg, Mode: 0600, Size: 2}}
	switch fault {
	case "owner":
		headers[0].Uid = 1
	case "mode":
		headers[3].Mode = 0644
	case "directory-mode":
		headers[0].Mode = 0755
	case "missing":
		headers[1].Name = "snapshot/other.json"
	case "escape":
		headers[3].Name = "../foreign"
	case "parent":
		headers[3].Name = "snapshot/missing/data"
	case "link":
		headers[3].Typeflag = tar.TypeSymlink
		headers[3].Size = 0
		headers[3].Linkname = "outside"
	case "duplicate":
		headers[3] = headers[1]
	case "pax":
		headers[3].PAXRecords = map[string]string{"comment": "foreign"}
	case "long-path":
		headers[3].Name = "snapshot/objects/" + strings.Repeat("a", 90) + ".jsonl"
		headers[3].PAXRecords = map[string]string{"path": headers[3].Name}
	}
	for _, h := range headers {
		if h.Uid == 0 {
			h.Uid = task.UID
		}
		h.Gid = task.GID
		if e := tw.WriteHeader(&h); e != nil {
			t.Fatal(e)
		}
		if h.Size > 0 {
			tw.Write([]byte("{}"))
		}
	}
	tw.Close()
	b := out.Bytes()
	if fault == "end" {
		b = b[:len(b)-1024]
	}
	if fault == "tail" {
		b = append(b, 1)
	}
	return task, b
}

func TestStoreBackupArchiveSafeBoundedTransport(t *testing.T) {
	task, raw := storeBackupArchiveFixture(t, "")
	var sink bytes.Buffer
	r, e := readStoreBackupArchive(bytes.NewReader(raw), &sink, task, 1<<20, 20)
	hash := sha256.Sum256(raw)
	if e != nil || r.Bytes != int64(len(raw)) || r.SHA256 != hex.EncodeToString(hash[:]) || len(r.Files) != 2 || !bytes.Equal(raw, sink.Bytes()) {
		t.Fatal(r, e)
	}
	for _, fault := range []string{"owner", "mode", "directory-mode", "missing", "escape", "parent", "link", "duplicate", "pax", "end", "tail"} {
		t.Run(fault, func(t *testing.T) {
			task, raw := storeBackupArchiveFixture(t, fault)
			if _, e := readStoreBackupArchive(bytes.NewReader(raw), io.Discard, task, 1<<20, 20); e == nil {
				t.Fatal("unsafe snapshot accepted")
			}
		})
	}
	if _, e := readStoreBackupArchive(bytes.NewReader(raw), io.Discard, task, 1<<20, 3); e == nil {
		t.Fatal("entry limit")
	}
	longTask, longRaw := storeBackupArchiveFixture(t, "long-path")
	if _, e := readStoreBackupArchive(bytes.NewReader(longRaw), io.Discard, longTask, 1<<20, 20); e != nil {
		t.Fatal("safe PAX path refused", e)
	}
	if _, e := readStoreBackupArchive(bytes.NewReader(raw), failedBackupSink{}, task, 1<<20, 20); e == nil {
		t.Fatal("sink failure")
	}
}
