package scope

import (
	"context"
	"reflect"
	"sort"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func validBackupAdmissionShape(a *RestoredApplicationAdmission) bool {
	if a == nil || a.PostgresPID <= 0 {
		return false
	}
	started, e := time.Parse(time.RFC3339Nano, a.PostgresStartedAt)
	if e != nil || started.IsZero() {
		return false
	}
	for _, value := range []string{a.Dependency.ReplacementProofSHA256, a.Dependency.TLSProofSHA256, a.Dependency.EffectiveRuntimeSHA256, a.Dependency.ConfigurationPlanSHA256, a.DatabaseProofSHA256, a.DatabasePlanSHA256, a.StoresProofSHA256, a.StoresPlanSHA256, a.PayloadSHA256, a.ContextSHA256} {
		if !wire.ValidSHA256(value) {
			return false
		}
	}
	return true
}

// This admission binds backup consistency only, never application activation.
func validRestoredBackupAdmission(events []Event, admission *RestoredApplicationAdmission) bool {
	process := processState(events)
	if !validBackupAdmissionShape(admission) || !databaseCreationProcessReady(events) || process.record == nil || dependencyAdmission(events) == nil {
		return false
	}
	control, found := process.record.After["control"]
	if !found || control.ExitCode != 0 || control.State != "created" && control.State != "exited" {
		return false
	}
	r := ProcessRecord{CreationSHA256: process.record.CreationSHA256, Target: "running", SelectedServices: []string{"control"}, Before: process.record.After, RestoredApplication: admission}
	return validApplicationProcess(events, r)
}

func (l *locked) historicalBackupAdmission() (*RestoredApplicationAdmission, error) {
	stores := currentStoresState(l.events)
	proof, ok := currentStoresProof(l.events)
	if !ok || stores.record == nil {
		return nil, failure("OFFLINE_SCOPE_BACKUP_RESTORED_ADMISSION_REQUIRED")
	}
	p, e := l.readCurrentStoresPlan(*stores.record)
	if e != nil {
		return nil, e
	}
	a := applicationAdmissionFromPlan(p, proof, stores.record.PlanSHA256)
	if !validRestoredBackupAdmission(l.events, &a) {
		return nil, failure("OFFLINE_SCOPE_BACKUP_RESTORED_ADMISSION_CHANGED")
	}
	return &a, nil
}

func (l *locked) admitRestoredBackup(ctx context.Context, c runtimeEngine, w runtimeOperator) (*RestoredApplicationAdmission, []engine.ConfigurationFile, error) {
	reader, supported := c.(restoredApplicationEngine)
	if !supported {
		return nil, nil, failure("OFFLINE_SCOPE_BACKUP_CURRENT_CONFIGURATION_READER_REQUIRED")
	}
	a, e := l.admitRestoredApplication(ctx, reader, w)
	if e != nil {
		return nil, nil, e
	}
	if !validRestoredBackupAdmission(l.events, a) {
		return nil, nil, failure("OFFLINE_SCOPE_BACKUP_RESTORED_ADMISSION_CHANGED")
	}
	_, files, e := l.currentControlReplacementFiles(ctx)
	return a, files, e
}

// Historical plan reconstruction uses frozen indexes, not today's certificate
// validity or current private certificate/key inputs. Executing admission above
// separately verifies actual mounted bytes and current PKI.
func (l *locked) bindRestoredBackupPlan(p *BackupPlan, admission *RestoredApplicationAdmission) error {
	if admission == nil {
		if restoreState(l.events).reserved || dependencyAdmission(l.events) != nil {
			return failure("OFFLINE_SCOPE_BACKUP_RESTORED_ADMISSION_REQUIRED")
		}
		return nil
	}
	expected, e := l.historicalBackupAdmission()
	if e != nil || !reflect.DeepEqual(expected, admission) || p.RuntimeSHA256 != admission.Dependency.EffectiveRuntimeSHA256 {
		return failure("OFFLINE_SCOPE_BACKUP_RESTORED_ADMISSION_CHANGED")
	}
	projection := tlsProjectionState(l.events)
	if projection.record == nil {
		return failure("OFFLINE_SCOPE_BACKUP_RESTORED_ADMISSION_CHANGED")
	}
	tls, e := l.readTLSProjectionPlan(*projection.record)
	if e != nil {
		return e
	}
	if wire.Digest(encode(tls.ConfigurationFiles)) != tls.PayloadSHA256 {
		return failure("OFFLINE_SCOPE_BACKUP_RESTORED_PAYLOAD_INDEX_CHANGED")
	}
	index := []BackupConfigurationFile{}
	for _, file := range p.ConfigurationFiles {
		if file.Role != "control-config" {
			index = append(index, file)
		}
	}
	for _, file := range tls.ConfigurationFiles {
		index = append(index, BackupConfigurationFile{Role: "control-config", File: file})
	}
	sort.Slice(index, func(i, j int) bool {
		if index[i].Role != index[j].Role {
			return index[i].Role < index[j].Role
		}
		return index[i].File.Name < index[j].File.Name
	})
	p.ConfigurationFiles, p.RestoredAdmission, p.EffectiveControlPayloadSHA256 = index, admission, tls.PayloadSHA256
	return nil
}

func (l *locked) backupAdmissionPrefix(attempt string) *locked {
	view := *l
	for i, e := range l.events {
		if e.Backup != nil && e.Backup.AttemptID == attempt {
			view.events = l.events[:i]
			break
		}
	}
	return &view
}

func (l *locked) verifyBackupAdmissions() error {
	seen := map[string]bool{}
	for _, e := range l.events {
		if e.Backup == nil || e.Backup.RestoredAdmission == nil || seen[e.Backup.AttemptID] {
			continue
		}
		seen[e.Backup.AttemptID] = true
		expected, err := l.backupAdmissionPrefix(e.Backup.AttemptID).historicalBackupAdmission()
		if err != nil || !reflect.DeepEqual(expected, e.Backup.RestoredAdmission) {
			return failure("OFFLINE_SCOPE_BACKUP_RESTORED_HISTORY_CHANGED")
		}
	}
	return nil
}

// Resume may observe an already submitted actor, but before continuing the
// workflow it must retain the originally qualified business process and mounted
// generation. Generated backup actors are checked by their own fixed SDK paths;
// they are not treated as ordinary Compose peers here.
func (l *locked) recheckRestoredBackup(ctx context.Context, c runtimeEngine, model compose.Model, p BackupPlan) error {
	if p.RestoredAdmission == nil {
		return nil
	}
	job := p.Jobs["postgres"].Postgres
	if job == nil {
		return failure("OFFLINE_SCOPE_BACKUP_RESTORED_ADMISSION_CHANGED")
	}
	for name, id := range job.Peers {
		object, found, e := c.InspectRuntime(ctx, p.ScopeID, name)
		if e != nil || !found || object.ID != id || object.State.ExitCode != 0 || object.State.OOMKilled || object.State.Paused || object.State.Restarting || object.State.Dead {
			return failure("OFFLINE_SCOPE_BACKUP_CURRENT_PROCESS_CHANGED")
		}
		if name == "postgres" {
			if !object.State.Running || object.State.Status != "running" || object.State.PID != p.RestoredAdmission.PostgresPID || object.State.StartedAt != p.RestoredAdmission.PostgresStartedAt {
				return failure("OFFLINE_SCOPE_BACKUP_CURRENT_PROCESS_CHANGED")
			}
		} else if object.State.Running || object.State.PID != 0 || object.State.Status != "created" && object.State.Status != "exited" {
			return failure("OFFLINE_SCOPE_BACKUP_STOPPED_APPLICATION_REQUIRED")
		}
	}
	tls, files, e := l.currentControlReplacementFiles(ctx)
	if e != nil {
		return e
	}
	destination := ""
	for _, mount := range model.Services["control"].Mounts {
		if mount.Source == "control-config" {
			if destination != "" || !mount.ReadOnly {
				return failure("OFFLINE_SCOPE_BACKUP_CURRENT_CONFIGURATION_MOUNT_REQUIRED")
			}
			destination = mount.Target
		}
	}
	if destination == "" {
		return failure("OFFLINE_SCOPE_BACKUP_CURRENT_CONFIGURATION_MOUNT_REQUIRED")
	}
	observation, e := readCurrentControlConfiguration(ctx, c, tls.ConfigurationOwner, job.Peers["control"], destination, tls.UID, tls.GID, files)
	if e != nil || observation.Status != engine.ConfigurationPayloadVerified {
		return failure("OFFLINE_SCOPE_BACKUP_CURRENT_CONFIGURATION_READBACK_REQUIRED:" + engine.DiagnosticCode(e))
	}
	return nil
}

func readBackupConfiguration(ctx context.Context, p BackupPlan, maximum int64) (map[string][]engine.ConfigurationFile, error) {
	root, e := openBackupDirectory(p.Request.DestinationRoot)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	if e = verifyBackupReservation(ctx, root, p); e != nil {
		return nil, e
	}
	files := map[string][]engine.ConfigurationFile{}
	for _, item := range p.ConfigurationFiles {
		raw, e := regular(root, "configuration/"+item.Role+"/"+item.File.Name, maximum)
		if e != nil || int64(len(raw)) != item.File.Bytes || wire.Digest(raw) != item.File.SHA256 {
			return nil, failure("OFFLINE_SCOPE_BACKUP_CONFIGURATION_CHANGED")
		}
		files[item.Role] = append(files[item.Role], engine.ConfigurationFile{Name: item.File.Name, Contents: raw})
	}
	return files, nil
}
