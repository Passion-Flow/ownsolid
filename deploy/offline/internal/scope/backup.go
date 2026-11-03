package scope

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type BackupResult struct {
	Status                string                                `json:"status"`
	ScopeID               string                                `json:"scope_id"`
	AttemptID             string                                `json:"attempt_id"`
	CompletedSteps        int                                   `json:"completed_steps"`
	TotalSteps            int                                   `json:"total_steps"`
	Pending               bool                                  `json:"pending"`
	ManifestSHA256        string                                `json:"manifest_sha256,omitempty"`
	Qualification         string                                `json:"qualification"`
	Profile               string                                `json:"profile,omitempty"`
	ReleaseManifestSHA256 string                                `json:"release_manifest_sha256,omitempty"`
	NativePostgreSQL      *wire.NativePostgreSQLBackupReference `json:"native_postgresql,omitempty"`
}

func (l *locked) backupResult() BackupResult {
	s := backupState(l.events)
	r := BackupResult{ScopeID: l.marker.ScopeID, Qualification: "selected_profile_backup_progress_only_fresh_restore_upgrade_full_package_and_native_target_pending"}
	if s.record == nil {
		r.Status = "no_backup_recorded"
		return r
	}
	r.AttemptID = s.record.AttemptID
	r.TotalSteps = s.record.TotalSteps
	r.CompletedSteps = len(s.completed)
	r.Pending = !s.terminal
	r.Status = "backup_incomplete_requires_reconciliation"
	if s.terminal {
		r.Status = "backup_aborted_partial_evidence_retained"
		if !s.aborted && s.record.Phase == "complete" {
			r.Status = "complete_selected_profile_backup_restore_pending"
			r.ManifestSHA256 = s.record.ArtifactSHA256
		}
	}
	return r
}

func RunBackup(ctx context.Context, c configuration.Config, r configuration.BackupRequest, mode string) (out BackupResult, err error) {
	if r.Validate() != nil || c.Engine == nil || c.Engine.Utility == nil || c.Compose == nil || c.Compose.Process == nil || r.StreamSeconds > c.Engine.LoadSeconds || r.StreamSeconds < c.Engine.DialSeconds {
		return BackupResult{}, failure("OFFLINE_SCOPE_BACKUP_CONFIGURATION_REQUIRED")
	}
	if mode != "backup" && mode != "reconcile-backup" && mode != "abort-backup" && mode != "inspect-backup" {
		return BackupResult{}, failure("OFFLINE_SCOPE_BACKUP_COMMAND_INVALID")
	}
	if mode == "inspect-backup" {
		return runBackup(ctx, c, r, mode, nil, nil)
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return BackupResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return BackupResult{}, e
	}
	return runBackup(ctx, c, r, mode, client, worker)
}

func runBackup(ctx context.Context, c configuration.Config, r configuration.BackupRequest, mode string, client backupEngine, worker runtimeOperator) (out BackupResult, err error) {
	l, e := open(c, mode != "inspect-backup")
	if e != nil {
		return BackupResult{}, e
	}
	defer l.close()
	if !contained(c.Verification.MountRoot, r.DestinationRoot) || r.DestinationRoot == c.ScopeRoot || contained(r.DestinationRoot, c.ScopeRoot) || contained(c.ScopeRoot, r.DestinationRoot) || !privateDirectory(filepath.Dir(r.DestinationRoot)) {
		return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_ISOLATED_DESTINATION_REQUIRED")
	}
	if l.usesMVPNativeBackup() {
		return l.runMVPNativeBackup(ctx, r, mode, client, worker)
	}

	s := backupState(l.events)
	if mode == "inspect-backup" {
		return l.inspectBackup(ctx, r)
	}
	var plan BackupPlan
	var payloads, sourceFiles map[string][]engine.ConfigurationFile
	if mode == "backup" {
		if s.record != nil && !s.terminal {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_RECONCILIATION_REQUIRED")
		}
		if _, e = os.Lstat(r.DestinationRoot); !os.IsNotExist(e) {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_DESTINATION_OCCUPIED")
		}
		v, e := l.verifyRuntimeContext(ctx, client, worker, false)
		if e != nil {
			return l.backupResult(), e
		}
		var restored *RestoredApplicationAdmission
		if restoreState(l.events).reserved || dependencyAdmission(l.events) != nil {
			var files []engine.ConfigurationFile
			restored, files, e = l.admitRestoredBackup(ctx, client, worker)
			if e != nil {
				return l.backupResult(), e
			}
			// This local read owns its map. Preserve the immutable descriptor and
			// other roles while exporting the actual mounted control generation.
			v.inputs.files["control-config"] = files
		} else if !databaseCreationProcessReady(l.events) || !schemaQualified(l.events) || !runtimeAccessQualified(l.events) || !bootstrapQualified(l.events) {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_QUALIFIED_DATABASE_ONLY_REQUIRED")
		}
		for role, svc := range v.observed.Services {
			if svc.ExitCode != 0 || role == "postgres" && svc.State != "running" || role != "postgres" && svc.State != "created" && svc.State != "exited" {
				return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_STOPPED_APPLICATION_REQUIRED")
			}
		}
		if e = backupDiskAdmission(r.DestinationRoot, r.MaximumBytes); e != nil {
			return l.backupResult(), e
		}
		id, e := nonce()
		if e != nil {
			return l.backupResult(), e
		}
		plan, payloads, e = l.makeBackupPlan(v, r, id)
		if e != nil {
			return l.backupResult(), e
		}
		if e = l.bindRestoredBackupPlan(&plan, restored); e != nil {
			return l.backupResult(), e
		}
		sourceFiles = v.inputs.files
	} else {
		if s.record == nil || s.terminal || s.record.RequestSHA256 != wire.Digest(encode(r)) || s.record.DestinationRoot != r.DestinationRoot {
			return l.backupResult(), failure("OFFLINE_SCOPE_MATCHING_INCOMPLETE_BACKUP_REQUIRED")
		}
		planMissing := false
		root, e := openBackupDirectory(r.DestinationRoot)
		var raw []byte
		if e == nil {
			raw, e = regular(root, backupPlanFile, c.Verification.MaximumMetadataBytes)
			root.Close()
		}
		if e != nil || wire.Digest(raw) != s.record.PlanSHA256 || wire.StrictJSON(raw, &plan) != nil {
			if mode != "abort-backup" || s.record.Step != 0 || s.record.Phase != "reserve" {
				return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_PLAN_CHANGED")
			}
			planMissing = true
		}
		verified, e := bundle.VerifyPrepared(ctx, c.Verification, filepath.Join(c.ScopeRoot, "release"), c.ExpectedArchiveSHA256, c.ExpectedManifestSHA256)
		if e != nil || verified.ReleaseID != l.marker.ReleaseID || verified.Profile != l.marker.Profile {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_RELEASE_CHANGED")
		}
		input, outcome, _ := inputsState(l.events)
		if input == nil || outcome != "completed" {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_INPUTS_REQUIRED")
		}
		view := l.backupAdmissionPrefix(s.record.AttemptID)
		p, e := view.readInputs(ctx, *input)
		if e != nil {
			return l.backupResult(), e
		}
		if mode != "abort-backup" {
			p.files, e = readBackupConfiguration(ctx, plan, c.Verification.MaximumMetadataBytes)
			if e != nil {
				return l.backupResult(), e
			}
		}
		v := verifiedRuntimeContext{inputs: p, creation: effectiveRuntimeState(view.events)}
		v.model, e = compose.DecodeModel(p.model, c.Verification.MaximumMetadataBytes)
		if e != nil {
			return l.backupResult(), e
		}
		expected, files, e := view.makeBackupPlan(v, r, s.record.AttemptID)
		if e == nil {
			e = view.bindRestoredBackupPlan(&expected, s.record.RestoredAdmission)
		}
		if e == nil {
			expected = recordedBackupLocalization(expected, s.record.PlanSHA256)
		}
		if e != nil || wire.Digest(encode(expected)) != s.record.PlanSHA256 || !planMissing && !reflect.DeepEqual(plan, expected) {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_CONTEXT_CHANGED")
		}
		if planMissing {
			plan = expected
		}
		payloads = files
		sourceFiles = p.files
		if mode == "abort-backup" {
			return l.abortBackup(ctx, client, plan)
		}
		if e = l.recheckRestoredBackup(ctx, client, v.model, plan); e != nil {
			return l.backupResult(), e
		}
		if s.aborting {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_ABORT_RECONCILIATION_REQUIRED")
		}
	}
	if len(plan.Steps) > 128 {
		return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_PLAN_LIMIT")
	}
	defer func() {
		if ctx.Err() == nil {
			return
		}
		state := backupState(l.events)
		if state.record == nil || state.terminal || state.record.AttemptID != plan.AttemptID {
			return
		}
		cleanup, done := context.WithTimeout(context.Background(), time.Duration(c.Compose.CommandSeconds)*time.Second)
		defer done()
		if _, e := l.abortBackup(cleanup, client, plan); e != nil {
			err = failure("OFFLINE_SCOPE_BACKUP_CANCELLATION_CLEANUP_UNRESOLVED")
		}
		out = l.backupResult()
	}()
	journal, e := l.root.Stat(journalName)
	if e != nil || journal.Size()+int64((len(plan.Steps)+2)*3)*maximumEventBytes > l.maximumJournalBytes() {
		return l.backupResult(), failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
	}
	for index, step := range plan.Steps {
		s = backupState(l.events)
		if _, done := s.completed[index]; done && s.record != nil && s.record.AttemptID == plan.AttemptID {
			continue
		}
		record := BackupRecord{RestoredAdmission: plan.RestoredAdmission, AttemptID: plan.AttemptID, PlanSHA256: wire.Digest(encode(plan)), RequestSHA256: wire.Digest(encode(r)), DestinationRoot: r.DestinationRoot, Step: index, TotalSteps: len(plan.Steps), Phase: step.Phase, Role: step.Role}
		reconcile := s.pending != ""
		id, e := nonce()
		if e != nil {
			return l.backupResult(), e
		}
		action, reference := "backup_effect", ""
		if reconcile {
			if mode != "reconcile-backup" || !backupRecordIdentity(record, *s.record) {
				return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_RECONCILIATION_REQUIRED")
			}
			action = "backup_reconcile"
			reference = s.pending
		} else if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", Backup: &record}); e != nil {
			return l.backupResult(), e
		}
		observed, e := l.executeBackupStep(ctx, client, plan, payloads, sourceFiles, record, reconcile)
		outcome, code := "completed", ""
		if e != nil {
			outcome = "uncertain"
			code = "OFFLINE_SCOPE_BACKUP_EFFECT_UNRESOLVED"
			observed = record
		}
		if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, Backup: &observed}); je != nil {
			return l.backupResult(), je
		}
		if e != nil {
			return l.backupResult(), e
		}
	}
	return l.backupResult(), nil
}

func openBackupDirectory(name string) (*os.Root, error) {
	if !privateDirectory(name) {
		return nil, failure("OFFLINE_SCOPE_BACKUP_PRIVATE_DIRECTORY_REQUIRED")
	}
	return openPrivateDirectory(name)
}
func backupDiskAdmission(destination string, maximum int64) error {
	for _, directory := range []string{filepath.Dir(destination), "/"} {
		var s syscall.Statfs_t
		if syscall.Statfs(directory, &s) != nil || s.Blocks == 0 {
			return failure("OFFLINE_SCOPE_BACKUP_DISK_OBSERVATION_FAILED")
		}
		free, total := uint64(s.Bavail)*uint64(s.Bsize), uint64(s.Blocks)*uint64(s.Bsize)
		additional := uint64(0)
		if directory != "/" {
			additional = uint64(maximum) * 2
		}
		if free < additional || free-additional < total*30/100 {
			return failure("OFFLINE_SCOPE_BACKUP_DISK_HEADROOM_REQUIRED")
		}
	}
	return nil
}

func (l *locked) backupTaskID(p BackupPlan, role string) string {
	if s := backupState(l.events); s.record == nil || s.record.AttemptID != p.AttemptID {
		return ""
	}
	for index, s := range p.Steps {
		if s.Phase == "task-create" && s.Role == role {
			return backupState(l.events).completed[index].ObjectID
		}
	}
	return ""
}
func (l *locked) executeBackupStep(ctx context.Context, c backupEngine, p BackupPlan, payloads, sourceFiles map[string][]engine.ConfigurationFile, r BackupRecord, reconcile bool) (BackupRecord, error) {
	if ctx.Err() != nil {
		return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if r.Phase == "reserve" {
		if !reconcile {
			if sourceFiles == nil {
				return r, failure("OFFLINE_SCOPE_BACKUP_INPUTS_REQUIRED")
			}
			if e := reserveBackupFiles(p, sourceFiles); e != nil {
				return r, e
			}
		}
		root, e := openBackupDirectory(p.Request.DestinationRoot)
		if e != nil {
			return r, e
		}
		defer root.Close()
		return r, verifyBackupReservation(ctx, root, p)
	}
	if volume, ok := p.Volumes[r.Role]; ok {
		switch r.Phase {
		case "volume-create":
			if !reconcile {
				if _, e := c.CreateVolume(ctx, volume.Owner); e != nil {
					return r, e
				}
			}
			_, found, e := c.InspectVolume(ctx, volume.Owner)
			if e != nil || !found {
				return r, failure("OFFLINE_SCOPE_BACKUP_VOLUME_UNRESOLVED")
			}
		case "utility-create":
			if !reconcile {
				if _, e := c.CreateUtility(ctx, volume.Owner, p.Initializer, volume.UID, volume.GID); e != nil {
					return r, e
				}
			}
			v, found, e := c.InspectUtility(ctx, volume.Owner, p.Initializer, volume.UID, volume.GID)
			if e != nil || !found {
				return r, failure("OFFLINE_SCOPE_BACKUP_UTILITY_UNRESOLVED")
			}
			r.ObjectID = v.ID
		case "payload-write":
			if !reconcile {
				if e := c.PutConfiguration(ctx, volume.Owner, p.Initializer, volume.UID, volume.GID, payloads[r.Role]); e != nil {
					return r, e
				}
			}
			if _, e := c.InspectConfiguration(ctx, volume.Owner, p.Initializer, volume.UID, volume.GID, payloads[r.Role]); e != nil {
				return r, e
			}
		case "utility-remove":
			if !reconcile {
				if e := c.RemoveUtility(ctx, volume.Owner, p.Initializer, volume.UID, volume.GID); e != nil {
					return r, e
				}
			}
			if _, found, e := c.InspectUtility(ctx, volume.Owner, p.Initializer, volume.UID, volume.GID); e != nil || found {
				return r, failure("OFFLINE_SCOPE_BACKUP_UTILITY_REMOVAL_UNRESOLVED")
			}
		case "volume-remove":
			if !reconcile {
				if e := c.RemoveBackupVolume(ctx, volume.Owner); e != nil {
					return r, e
				}
			}
			if _, found, e := c.InspectVolume(ctx, volume.Owner); e != nil || found {
				return r, failure("OFFLINE_SCOPE_BACKUP_VOLUME_REMOVAL_UNRESOLVED")
			}
		default:
			return r, failure("OFFLINE_SCOPE_BACKUP_STEP_INVALID")
		}
		return r, nil
	}
	if job, ok := p.Jobs[r.Role]; ok {
		return l.executeBackupJob(ctx, c, p, payloads, r, job, reconcile)
	}
	root, e := openBackupDirectory(p.Request.DestinationRoot)
	if e != nil {
		return r, e
	}
	defer root.Close()
	if r.Phase == "manifest-publish" {
		if !reconcile {
			m, e := p.buildManifest(ctx, root)
			if e != nil {
				return r, e
			}
			if e = writeBackupFile(root, backupManifestFile, encode(m)); e != nil {
				return r, e
			}
		}
		_, f, e := inspectBackupFiles(ctx, root, p)
		r.ArtifactSHA256, r.ArtifactBytes = f.SHA256, f.Bytes
		return r, e
	}
	if r.Phase == "complete" {
		_, f, e := inspectBackupFiles(ctx, root, p)
		r.ArtifactSHA256, r.ArtifactBytes = f.SHA256, f.Bytes
		return r, e
	}
	return r, failure("OFFLINE_SCOPE_BACKUP_STEP_INVALID")
}

func (l *locked) inspectBackup(ctx context.Context, r configuration.BackupRequest) (BackupResult, error) {
	record := completedBackupForRequest(l.events, r)
	if record == nil {
		return l.backupResult(), failure("OFFLINE_SCOPE_COMPLETED_BACKUP_REQUIRED")
	}
	root, e := openBackupDirectory(r.DestinationRoot)
	if e != nil {
		return l.backupResult(), e
	}
	defer root.Close()
	var p BackupPlan
	raw, e := regular(root, backupPlanFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.StrictJSON(raw, &p) != nil || wire.Digest(raw) != record.PlanSHA256 || p.AttemptID != record.AttemptID || p.ScopeID != l.marker.ScopeID || !reflect.DeepEqual(p.Request, r) {
		return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_PLAN_CHANGED")
	}
	_, f, e := inspectBackupFiles(ctx, root, p)
	if e != nil {
		return l.backupResult(), e
	}
	if f.SHA256 != record.ArtifactSHA256 || f.Bytes != record.ArtifactBytes {
		return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_MANIFEST_CHANGED")
	}
	return BackupResult{Status: "complete_selected_profile_backup_restore_pending", ScopeID: l.marker.ScopeID, AttemptID: record.AttemptID, CompletedSteps: record.TotalSteps, TotalSteps: record.TotalSteps, ManifestSHA256: record.ArtifactSHA256, Qualification: l.backupResult().Qualification}, nil
}

func completedBackupForRequest(events []Event, r configuration.BackupRequest) *BackupRecord {
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Backup != nil && (e.Action == "backup_effect" || e.Action == "backup_reconcile") && e.Outcome == "completed" && e.Backup.Phase == "complete" && e.Backup.RequestSHA256 == wire.Digest(encode(r)) && e.Backup.DestinationRoot == r.DestinationRoot {
			record := *e.Backup
			return &record
		}
	}
	return nil
}

func (l *locked) abortBackup(ctx context.Context, c backupEngine, p BackupPlan) (BackupResult, error) {
	s := backupState(l.events)
	record := *s.record
	record.ObjectID = ""
	record.ArtifactSHA256 = ""
	record.ArtifactBytes = 0
	id, reference := s.pending, s.abortReference
	if !s.aborting {
		var e error
		id, e = nonce()
		if e != nil {
			return l.backupResult(), e
		}
		reference = s.pending
		if e = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "backup_abort", Outcome: "started", Backup: &record}); e != nil {
			return l.backupResult(), e
		}
	}
	roles := []string{}
	for role := range p.Jobs {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		job := p.Jobs[role]
		var value engine.RuntimeContainer
		var found bool
		var e error
		// A task cannot have existed before its durable creation intent. Ignore
		// later uncreated roles whose prerequisite volumes may not exist yet.
		created := false
		for _, event := range l.events {
			if event.Backup != nil && event.Backup.AttemptID == p.AttemptID && event.Backup.Role == role && event.Backup.Phase == "task-create" {
				created = true
			}
		}
		if !created {
			continue
		}
		removed := false
		for _, event := range l.events {
			if event.Backup != nil && event.Backup.AttemptID == p.AttemptID && event.Backup.Role == role && event.Backup.Phase == "task-remove" && event.Outcome == "completed" {
				removed = true
			}
		}
		if removed {
			continue
		}
		// Removed backup volumes cannot retain any consumer. On a repeated
		// abort, first independently observe actor absence through candidates;
		// this avoids requiring volumes already removed by the prior cleanup.
		objects, e := c.RuntimeCandidates(ctx, p.ScopeID, false)
		if e != nil {
			return l.backupResult(), e
		}
		actorFound := false
		for _, o := range objects {
			if o.Labels["io.projectone.operation"] == p.AttemptID && o.Labels["io.projectone.role"] == "database-"+func() string {
				if job.Store != nil {
					return job.Store.Kind
				}
				return engine.PostgresBackupKind
			}() {
				actorFound = true
			}
		}
		if !actorFound {
			continue
		}
		if job.Store != nil {
			value, found, e = c.InspectDatabaseTask(ctx, *job.Store)
		} else {
			value, found, e = c.InspectPostgresBackupTask(ctx, *job.Postgres)
		}
		if e != nil {
			return l.backupResult(), e
		}
		if !found {
			continue
		}
		if value.State.Running {
			if job.Store != nil {
				e = c.StopDatabaseTask(ctx, *job.Store, value.ID, l.config.Compose.Process.StopSeconds)
			} else {
				e = c.StopPostgresBackupTask(ctx, *job.Postgres, value.ID, l.config.Compose.Process.StopSeconds)
			}
			if e != nil {
				return l.backupResult(), e
			}
		}
		if job.Store != nil {
			e = c.RemoveDatabaseTask(ctx, *job.Store, value.ID)
		} else {
			e = c.RemovePostgresBackupTask(ctx, *job.Postgres, value.ID)
		}
		if e != nil {
			return l.backupResult(), e
		}
	}
	keys := []string{}
	for key := range p.Volumes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := p.Volumes[key]
		if _, found, e := c.InspectUtility(ctx, v.Owner, p.Initializer, v.UID, v.GID); e != nil {
			return l.backupResult(), e
		} else if found {
			if e = c.RemoveUtility(ctx, v.Owner, p.Initializer, v.UID, v.GID); e != nil {
				return l.backupResult(), e
			}
		}
		if _, found, e := c.InspectVolume(ctx, v.Owner); e != nil {
			return l.backupResult(), e
		} else if found {
			if e = c.RemoveBackupVolume(ctx, v.Owner); e != nil {
				return l.backupResult(), e
			}
		}
	}
	record.ArtifactSHA256 = wire.Digest(encode(p))
	record.ArtifactBytes = int64(len(encode(p)))
	e := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "backup_abort", Outcome: "completed", Backup: &record})
	return l.backupResult(), e
}
