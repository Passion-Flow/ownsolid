package scope

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func TestBackupJournalQualificationOrderNoResendAndAbort(t *testing.T) {
	// Explicit existing scope/SQL doubles; no backup artifact or process is
	// fabricated as actual integration evidence by this transition test.
	c, d, w := bootstrapScopeFixture(t)
	ctx := context.Background()
	for _, kind := range []string{engine.DatabaseBootstrap, engine.DatabaseBootstrapReconcile} {
		if _, e := createDatabaseTask(ctx, c, d, w, kind, false); e != nil {
			t.Fatal(e)
		}
		if _, e := databaseTaskAction(ctx, c, d, w, "execute", false, kind); e != nil {
			t.Fatal(e)
		}
		if _, e := databaseTaskAction(ctx, c, d, w, "removed", false, kind); e != nil {
			t.Fatal(e)
		}
	}
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	base := append([]Event{}, l.events...)
	r := BackupRecord{AttemptID: strings.Repeat("a", 32), PlanSHA256: strings.Repeat("b", 64), RequestSHA256: strings.Repeat("c", 64), DestinationRoot: "/synthetic/external/new-backup", TotalSteps: 3, Phase: "reserve"}
	start := Event{OperationID: strings.Repeat("d", 32), Action: "backup_effect", Outcome: "started", Backup: &r}
	if e := validateBackupEvent(nil, start); e == nil {
		t.Fatal("unqualified backup admitted")
	}
	if e := validateImageEvent(base, start); e != nil {
		t.Fatal(e)
	}
	events := append(base, start)
	if e := validateImageEvent(events, Event{Action: "process_start"}); e == nil {
		t.Fatal("runtime mutation during backup")
	}
	if e := validateBackupEvent(events, start); e == nil {
		t.Fatal("uncertain effect replay")
	}
	wrong := start
	wrong.Outcome = "completed"
	wrong.OperationID = strings.Repeat("e", 32)
	if e := validateBackupEvent(events, wrong); e == nil {
		t.Fatal("wrong terminal authority")
	}
	uncertain := start
	uncertain.Outcome = "uncertain"
	uncertain.Code = "OFFLINE_SCOPE_BACKUP_EFFECT_UNRESOLVED"
	if e := validateBackupEvent(events, uncertain); e != nil {
		t.Fatal(e)
	}
	events = append(events, uncertain)
	reconcile := start
	reconcile.Action = "backup_reconcile"
	reconcile.OperationID = strings.Repeat("e", 32)
	reconcile.ReferenceOperationID = start.OperationID
	reconcile.Outcome = "completed"
	if e := validateBackupEvent(events, reconcile); e != nil {
		t.Fatal(e)
	}
	events = append(events, reconcile)
	skip := r
	skip.Step = 2
	skip.Phase = "complete"
	wrong = start
	wrong.Backup = &skip
	if e := validateBackupEvent(events, wrong); e == nil {
		t.Fatal("step skipped")
	}
	next := r
	next.Step = 1
	next.Phase = "manifest-publish"
	start.Backup = &next
	start.OperationID = strings.Repeat("f", 32)
	if e := validateBackupEvent(events, start); e != nil {
		t.Fatal(e)
	}
	events = append(events, start)
	abort := Event{OperationID: strings.Repeat("9", 32), ReferenceOperationID: start.OperationID, Action: "backup_abort", Outcome: "started", Backup: &next}
	if e := validateBackupEvent(events, abort); e != nil {
		t.Fatal(e)
	}
	events = append(events, abort)
	if e := validateBackupEvent(events, reconcile); e == nil {
		t.Fatal("reconciliation bypassed abort")
	}
	finished := next
	finished.ArtifactSHA256 = strings.Repeat("8", 64)
	finished.ArtifactBytes = 99
	abort.Backup = &finished
	abort.Outcome = "completed"
	if e := validateBackupEvent(events, abort); e != nil {
		t.Fatal(e)
	}
	events = append(events, abort)
	s := backupState(events)
	if !s.terminal || !s.aborted || s.pending != "" {
		t.Fatal(s)
	}
	l.events = events
	if result := l.backupResult(); result.ManifestSHA256 != "" || result.Status != "backup_aborted_partial_evidence_retained" {
		t.Fatal("aborted backup reported complete", result)
	}
	old := Event{Action: "legacy"}
	raw, _ := json.Marshal(old)
	if bytes.Contains(raw, []byte("backup")) {
		t.Fatal("legacy event encoding changed")
	}
}

func TestBackupFilesPrivateInventoryDurabilityAndTamperGuards(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	if os.Chmod(parent, 0700) != nil {
		t.Fatal("private parent")
	}
	r := configuration.BackupDefaults()
	r.DestinationRoot = filepath.Join(parent, "new")
	r.Reason = "Explicit synthetic manifest fixture"
	r.MaximumBytes = 16 << 20
	p := BackupPlan{FormatVersion: 1, AttemptID: strings.Repeat("a", 32), ScopeID: strings.Repeat("b", 32), ReleaseID: "synthetic", Profile: "synthetic", ConfigurationSHA256: strings.Repeat("c", 64), ManifestSHA256: strings.Repeat("d", 64), Request: r, Jobs: map[string]BackupJob{"models": {Store: &engine.DatabaseTask{}}, "postgres": {Postgres: &engine.PostgresBackupTask{}}}, ConfigurationFiles: []BackupConfigurationFile{{Role: "control-config", File: InputFile{Name: "synthetic-key", Bytes: 9, SHA256: wire.Digest([]byte("synthetic"))}}}}
	files := map[string][]engine.ConfigurationFile{"control-config": {{Name: "synthetic-key", Contents: []byte("synthetic")}}}
	if e := reserveBackupFiles(p, files); e != nil {
		t.Fatal(e)
	}
	if e := reserveBackupFiles(p, files); e == nil {
		t.Fatal("destination overwritten")
	}
	root, e := openBackupDirectory(r.DestinationRoot)
	if e != nil {
		t.Fatal(e)
	}
	defer root.Close()
	if e = verifyBackupReservation(ctx, root, p); e != nil {
		t.Fatal(e)
	}
	// Synthetic transport bytes test hash/inventory rules only. Real pg_dump
	// and backend snapshots are exclusively in the tagged actual journey.
	for _, role := range []string{"models", "postgres"} {
		var archive bytes.Buffer
		writer := tar.NewWriter(&archive)
		prefix := "snapshot"
		names := []string{"objects/", "model-files.json"}
		if role == "postgres" {
			prefix = "postgresql"
			names = []string{"private/", "private/database.dump", "private/contents.list", "private/source.json", "private/tools.txt"}
		}
		writer.WriteHeader(&tar.Header{Name: prefix, Typeflag: tar.TypeDir, Mode: 0700})
		for _, name := range names {
			header := tar.Header{Name: prefix + "/" + strings.TrimSuffix(name, "/"), Mode: 0600, Typeflag: tar.TypeReg, Size: 2}
			if strings.HasSuffix(name, "/") {
				header.Mode = 0700
				header.Typeflag = tar.TypeDir
				header.Size = 0
			}
			writer.WriteHeader(&header)
			if header.Size > 0 {
				writer.Write([]byte("{}"))
			}
		}
		writer.Close()
		data := archive.Bytes()
		if e = writeBackupFile(root, role+".tar", data); e != nil {
			t.Fatal(e)
		}
		if role == "models" {
			e = writeBackupFile(root, role+".json", encode(engine.StoreBackupArchive{Files: []engine.BackupArchiveFile{}, Status: "verified_exited_store_snapshot_archive_bytes", Role: role, Bytes: int64(len(data)), SHA256: wire.Digest(data)}))
		} else {
			e = writeBackupFile(root, role+".json", encode(engine.PostgresBackupArchive{Files: []engine.BackupArchiveFile{}, Status: "verified_exited_postgres_custom_archive_bytes", Bytes: int64(len(data)), SHA256: wire.Digest(data), Source: engine.PostgresBackupSource{SchemaVersion: 16}}))
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	m, e := p.buildManifest(ctx, root)
	if e != nil {
		t.Fatal(e)
	}
	if e = writeBackupFile(root, backupManifestFile, encode(m)); e != nil {
		t.Fatal(e)
	}
	if _, _, e = inspectBackupFiles(ctx, root, p); e != nil {
		t.Fatal(e)
	}
	if entries, e := remainingBackupEntries(ctx, root, p); e != nil || entries != p.Request.MaximumEntries-backupHostEntries(p)-9 {
		t.Fatal("aggregate host/store/database entry accounting", entries, e)
	}
	limited := p
	limited.Request.MaximumEntries = backupHostEntries(p) + 8
	if _, e = remainingBackupEntries(ctx, root, limited); e == nil || e.Error() != "OFFLINE_SCOPE_BACKUP_ENTRY_LIMIT" {
		t.Fatal("aggregate cap bypass", e)
	}
	if e = root.Mkdir("unknown", 0700); e != nil {
		t.Fatal(e)
	}
	if _, _, e = inspectBackupFiles(ctx, root, p); e == nil {
		t.Fatal("undeclared directory")
	}
	root.Remove("unknown")
	if e = root.Link("models.tar", "models-hardlink"); e != nil {
		t.Fatal(e)
	}
	if _, e = backupHashFile(ctx, root, "models.tar", r.MaximumBytes); e == nil {
		t.Fatal("hardlinked host artifact")
	}
	root.Remove("models-hardlink")
	if e = root.Symlink("models.tar", "linked"); e != nil {
		t.Fatal(e)
	}
	if _, e = backupHashFile(ctx, root, "linked", r.MaximumBytes); e == nil {
		t.Fatal("symlink host artifact")
	}
	root.Remove("linked")
	f, e := root.OpenFile("models.tar", os.O_WRONLY, 0)
	if e != nil {
		t.Fatal(e)
	}
	f.WriteAt([]byte("X"), 0)
	f.Sync()
	f.Close()
	if _, _, e = inspectBackupFiles(ctx, root, p); e == nil {
		t.Fatal("changed archive")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, e = backupHashFile(cancelled, root, "models.tar", r.MaximumBytes); e == nil {
		t.Fatal("cancellation ignored")
	}
}
