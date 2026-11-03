package scope

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type backupRuntimeReaderOnly struct{ runtimeEngine }

func TestRestoredBackupBindsEffectiveGenerationAndHistoricalAdmission(t *testing.T) {
	// Engine/SQL/store observations are explicit metadata doubles. Actual PKI,
	// protected snapshots, generation plans and journal validation are real.
	c, request, d, w := applicationFixture(t)
	l, e := open(c, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.close()
	ctx := context.Background()
	a, files, e := l.admitRestoredBackup(ctx, d, w)
	if e != nil || a == nil || len(files) == 0 {
		t.Fatal("fresh current backup admission", e)
	}
	v, e := l.verifyRuntimeContext(ctx, d, w, false)
	if e != nil {
		t.Fatal(e)
	}
	baseline := restoreConfigurationDigest(v.inputs.files["control-config"])
	v.inputs.files["control-config"] = files
	r := configuration.BackupDefaults()
	r.DestinationRoot, r.Reason = filepath.Join(t.TempDir(), "backup"), "Explicit synthetic effective backup plan"
	p, _, e := l.makeBackupPlan(v, r, strings.Repeat("a", 32))
	if e == nil {
		e = l.bindRestoredBackupPlan(&p, a)
	}
	if e != nil {
		t.Fatal("effective plan", e)
	}
	model, e := compose.DecodeModel(v.inputs.model, c.Verification.MaximumMetadataBytes)
	if e != nil || l.recheckRestoredBackup(ctx, d, model, p) != nil {
		t.Fatal("current business process and mounted generation required", e)
	}
	if _, _, e = l.admitRestoredBackup(ctx, backupRuntimeReaderOnly{runtimeEngine: d}, w); e == nil || e.Error() != "OFFLINE_SCOPE_BACKUP_CURRENT_CONFIGURATION_READER_REQUIRED" {
		t.Fatal("missing fixed configuration reader accepted", e)
	}
	for _, fault := range []string{"PID", "started-at", "nonzero", "payload"} {
		t.Run("current-"+fault, func(t *testing.T) {
			d.process = func(r *engine.RuntimeContainer) {
				switch fault {
				case "PID":
					r.State.PID++
				case "started-at":
					r.State.StartedAt = "2026-10-18T00:00:00Z"
				case "nonzero":
					r.State.ExitCode = 1
				}
			}
			d.readFault = fault == "payload"
			defer func() { d.process, d.readFault = nil, false }()
			if l.recheckRestoredBackup(ctx, d, model, p) == nil {
				t.Fatal("changed current process/payload accepted")
			}
		})
	}
	if p.RestoredAdmission == nil || p.RuntimeSHA256 != a.Dependency.EffectiveRuntimeSHA256 || p.RuntimeSHA256 == wire.Digest(encode(*runtimeState(l.events).record)) || baseline == restoreConfigurationDigest(files) {
		t.Fatal("original baseline substituted for current generation")
	}
	indexed := map[string]InputFile{}
	for _, item := range p.ConfigurationFiles {
		if item.Role == "control-config" {
			indexed[item.File.Name] = item.File
		}
	}
	for _, f := range files {
		if indexed[f.Name] != (InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)}) {
			t.Fatal("current projected bytes not indexed", f.Name)
		}
	}
	if p.EffectiveControlPayloadSHA256 != restoreConfigurationDigest(files) || p.EffectiveControlPayloadSHA256 == a.PayloadSHA256 {
		t.Fatal("mounted control payload conflated with combined SQL/store verification payload")
	}
	for _, fault := range []string{"omitted", "proof", "payload", "PID", "time", "generation"} {
		t.Run(fault, func(t *testing.T) {
			forged := *a
			candidate := &forged
			switch fault {
			case "omitted":
				candidate = nil
			case "proof":
				forged.DatabaseProofSHA256 = strings.Repeat("f", 64)
			case "payload":
				forged.PayloadSHA256 = strings.Repeat("f", 64)
			case "PID":
				forged.PostgresPID++
			case "time":
				forged.PostgresStartedAt = "2026-10-18T00:00:00Z"
			case "generation":
				forged.Dependency.TLSProofSHA256 = strings.Repeat("f", 64)
			}
			copy := p
			if l.bindRestoredBackupPlan(&copy, candidate) == nil {
				t.Fatal("forged or omitted admission accepted")
			}
		})
	}
	record := BackupRecord{RestoredAdmission: a, AttemptID: p.AttemptID, PlanSHA256: wire.Digest(encode(p)), RequestSHA256: wire.Digest(encode(r)), DestinationRoot: r.DestinationRoot, TotalSteps: len(p.Steps), Phase: "reserve"}
	start := Event{OperationID: strings.Repeat("b", 32), Action: "backup_effect", Outcome: "started", Backup: &record}
	prefix := append([]Event{}, l.events...)
	if e = validateBackupEvent(prefix, start); e != nil {
		t.Fatal("fresh restored reserve refused", e)
	}
	missing := record
	missing.RestoredAdmission = nil
	bad := start
	bad.Backup = &missing
	if validateBackupEvent(prefix, bad) == nil {
		t.Fatal("restored reserve used old nil-admission path")
	}
	if e = l.appendEvent(start); e != nil || l.verifyBackupAdmissions() != nil {
		t.Fatal("durable historical admission", e)
	}
	terminal := start
	terminal.Outcome = "completed"
	if e = validateBackupEvent(l.events, terminal); e != nil {
		t.Fatal(e)
	}
	changed := *a
	changed.PostgresPID++
	changedRecord := record
	changedRecord.RestoredAdmission = &changed
	terminal.Backup = &changedRecord
	if validateBackupEvent(l.events, terminal) == nil {
		t.Fatal("terminal changed immutable admission")
	}
	// Reducer-only suffix: this is deliberately not appended to the journal as
	// a real qualification. Historical reads must use the original prefix.
	view := *l
	view.events = append(append([]Event{}, l.events...), Event{RestoredCurrentStores: &RestoredCurrentStoresRecord{GenerationID: strings.Repeat("f", 32)}})
	if e = view.verifyBackupAdmissions(); e != nil {
		t.Fatal("later suffix replaced original admission", e)
	}
	// Historical metadata remains reconstructible without current private PKI
	// input. A new executing admission must still fail closed.
	if e = os.Rename(request.CertificateFile, request.CertificateFile+".withheld"); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.Rename(request.CertificateFile+".withheld", request.CertificateFile) })
	copy := p
	if e = l.backupAdmissionPrefix(p.AttemptID).bindRestoredBackupPlan(&copy, a); e != nil || !reflect.DeepEqual(copy, p) {
		t.Fatal("historical plan required current certificate", e)
	}
	if _, _, e = l.admitRestoredBackup(ctx, d, w); e == nil {
		t.Fatal("new execution accepted missing current PKI")
	}
}

func TestEffectiveBackupRecoveryReadsVerifiedPrivateSnapshotOnly(t *testing.T) {
	r := configuration.BackupDefaults()
	parent := t.TempDir()
	if e := os.Chmod(parent, 0700); e != nil {
		t.Fatal(e)
	}
	r.DestinationRoot = filepath.Join(parent, "backup")
	bytes := []byte("explicit synthetic projected certificate bytes")
	p := BackupPlan{FormatVersion: 1, AttemptID: strings.Repeat("a", 32), Request: r, ConfigurationFiles: []BackupConfigurationFile{{Role: "control-config", File: InputFile{Name: "server.crt", Bytes: int64(len(bytes)), SHA256: wire.Digest(bytes)}}}}
	files := map[string][]engine.ConfigurationFile{"control-config": {{Name: "server.crt", Contents: bytes}}}
	if e := reserveBackupFiles(p, files); e != nil {
		t.Fatal(e)
	}
	recovered, e := readBackupConfiguration(context.Background(), p, 1<<20)
	if e != nil || !reflect.DeepEqual(recovered, files) {
		t.Fatal("exact private snapshot not recovered", e)
	}
	file := filepath.Join(r.DestinationRoot, "configuration", "control-config", "server.crt")
	if e = os.WriteFile(file, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = readBackupConfiguration(context.Background(), p, 1<<20); e == nil {
		t.Fatal("changed snapshot accepted")
	}
}
