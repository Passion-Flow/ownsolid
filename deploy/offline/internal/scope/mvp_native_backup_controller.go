package scope

import (
	"bytes"
	"context"
	"io"
	"os"
	"reflect"
	"sort"
	"syscall"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// The caller already holds the original Scope lock and destination admission.
// This branch never enters the legacy PostgresBackupTask/four-export task loop.
func (l *locked) runMVPNativeBackup(ctx context.Context, r configuration.BackupRequest, mode string, base backupEngine, worker runtimeOperator) (BackupResult, error) {
	if !l.usesMVPNativeBackup() {
		return l.backupResult(), failure("OFFLINE_SCOPE_NATIVE_BACKUP_PROFILE_REQUIRED")
	}
	if mode == "inspect-backup" {
		return l.inspectMVPNativeBackup(ctx, r)
	}
	client, ok := base.(mvpNativeBackupEngine)
	if !ok {
		return l.backupResult(), failure("OFFLINE_SCOPE_NATIVE_BACKUP_ENGINE_REQUIRED")
	}
	state := backupState(l.events)
	var p mvpNativeBackupPlan
	var execution mvpNativeBackupExecution
	var payloads map[string][]engine.ConfigurationFile
	var sourceFiles map[string][]engine.ConfigurationFile
	var sourceDescriptor, release, signature []byte
	if mode == "backup" {
		if state.record != nil && !state.terminal {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_RECONCILIATION_REQUIRED")
		}
		if _, e := os.Lstat(r.DestinationRoot); !os.IsNotExist(e) {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_DESTINATION_OCCUPIED")
		}
		v, e := l.verifyRuntimeContext(ctx, client, worker, false)
		if e != nil {
			return l.backupResult(), e
		}
		if e = l.admitMVPNativeRestoredBackup(ctx, client, worker, &v); e != nil {
			return l.backupResult(), e
		}
		if !restoreState(l.events).reserved && dependencyAdmission(l.events) == nil && (!databaseCreationProcessReady(l.events) || !schemaQualified(l.events) || !runtimeAccessQualified(l.events) || !bootstrapQualified(l.events)) {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_QUALIFIED_DATABASE_ONLY_REQUIRED")
		}

		for role, s := range v.observed.Services {
			if s.ExitCode != 0 || role == "postgres" && s.State != "running" || role != "postgres" && s.State != "created" && s.State != "exited" {
				return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_STOPPED_APPLICATION_REQUIRED")
			}
		}
		id, e := nonce()
		if e != nil {
			return l.backupResult(), e
		}
		p, execution, payloads, e = l.makeMVPNativeBackupExecution(ctx, v, r, id)
		if e != nil {
			return l.backupResult(), e
		}
		if e = admitMVPNativeBackupStorage(ctx, client, p, execution, l.config.ScopeRoot, imageState(l.events).engine); e != nil {
			return l.backupResult(), e
		}
		sourceFiles = v.inputs.files
		sourceDescriptor = v.inputs.descriptorBytes()
		release, signature, e = l.mvpNativeSourceRelease(ctx)
		if e != nil {
			return l.backupResult(), e
		}
	} else {
		if state.record == nil || state.terminal || state.record.RequestSHA256 != wire.Digest(encode(r)) || state.record.DestinationRoot != r.DestinationRoot {
			return l.backupResult(), failure("OFFLINE_SCOPE_MATCHING_INCOMPLETE_BACKUP_REQUIRED")
		}
		root, e := openBackupDirectory(r.DestinationRoot)
		if e != nil {
			return l.backupResult(), e
		}
		raw, e := regular(root, backupPlanFile, l.config.Verification.MaximumMetadataBytes)
		if e != nil || wire.Digest(raw) != state.record.PlanSHA256 || wire.StrictJSON(raw, &p) != nil || !validMVPNativeBackupPlan(p) || p.ScopeID != l.marker.ScopeID || p.AttemptID != state.record.AttemptID || !reflect.DeepEqual(p.Request, r) {
			root.Close()
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_PLAN_CHANGED")
		}
		execution, e = readMVPNativeExecution(root, l.config, p)
		if e != nil {
			root.Close()
			return l.backupResult(), e
		}
		if mode == "abort-backup" {
			root.Close()
			return l.abortMVPNativeBackup(ctx, client, p, execution)
		}
		if e = verifyMVPNativeBackupReservation(ctx, root, p, l.config); e != nil {
			root.Close()
			return l.backupResult(), e
		}
		sourceFiles = map[string][]engine.ConfigurationFile{}
		for _, f := range p.ConfigurationFiles {
			data, e := regular(root, "configuration/"+f.Role+"/"+f.File.Name, l.config.Verification.MaximumMetadataBytes)
			if e != nil {
				root.Close()
				return l.backupResult(), e
			}
			sourceFiles[f.Role] = append(sourceFiles[f.Role], engine.ConfigurationFile{Name: f.File.Name, Contents: data})
		}
		root.Close()
		view, e := l.mvpNativeBackupAdmissionView(p.AttemptID, execution.MaintenanceHead)
		if e != nil {
			return l.backupResult(), e
		}

		input, outcome, _ := inputsState(view.events)
		if input == nil || outcome != "completed" {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_INPUTS_REQUIRED")
		}
		prepared, e := view.readInputs(ctx, *input)
		if e != nil {
			return l.backupResult(), e
		}
		prepared.files = sourceFiles
		model, e := compose.DecodeModel(prepared.model, l.config.Verification.MaximumMetadataBytes)
		if e != nil {
			return l.backupResult(), e
		}
		if _, complete := controlReplacementProof(view.events); complete {
			replacement, e := view.readControlReplacementPlan()
			if e != nil {
				return l.backupResult(), e
			}
			model, e = compose.ControlConfigurationModel(model, replacement.GenerationID)
			if e != nil {
				return l.backupResult(), e
			}
		}
		if cumulativeUpgradeState(view.events).record != nil {
			current, e := view.cumulativeMVPCurrentSource(ctx)
			if e != nil {
				return l.backupResult(), e
			}
			model = current.Model
		}
		v := verifiedRuntimeContext{inputs: prepared, model: model, creation: effectiveRuntimeState(view.events)}
		if e = view.recheckMVPNativeBackupSource(ctx, client, worker, p, execution); e != nil {
			return l.backupResult(), e
		}

		expected, expectedExecution, files, e := view.makeMVPNativeBackupExecution(ctx, v, r, p.AttemptID)
		if e != nil || !reflect.DeepEqual(expected, p) || !reflect.DeepEqual(expectedExecution, execution) {
			return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_CONTEXT_CHANGED")
		}
		payloads = files
	}
	journal, e := l.root.Stat(journalName)
	if e != nil || journal.Size()+int64((len(p.Steps)+2)*3)*maximumEventBytes > l.maximumJournalBytes() {
		return l.backupResult(), failure("OFFLINE_SCOPE_JOURNAL_CAPACITY_REQUIRED")
	}
	for index, step := range p.Steps {
		state = backupState(l.events)
		if _, done := state.completed[index]; done && state.record != nil && state.record.AttemptID == p.AttemptID {
			continue
		}
		record := BackupRecord{AttemptID: p.AttemptID, PlanSHA256: wire.Digest(encode(p)), RequestSHA256: wire.Digest(encode(r)), DestinationRoot: r.DestinationRoot, Step: index, TotalSteps: len(p.Steps), Phase: step.Phase, Role: step.Role}
		if p.Restored != nil {
			a := p.Restored.Admission
			record.RestoredAdmission = &a
		}

		id, e := nonce()
		if e != nil {
			return l.backupResult(), e
		}
		action, reference := "backup_effect", ""
		reconcile := state.pending != ""
		if reconcile {
			if mode != "reconcile-backup" || !backupRecordIdentity(record, *state.record) {
				return l.backupResult(), failure("OFFLINE_SCOPE_BACKUP_RECONCILIATION_REQUIRED")
			}
			action, reference = "backup_reconcile", state.pending
			if e = admitMVPNativeBackupStorage(ctx, client, p, execution, l.config.ScopeRoot, imageState(l.events).engine, BackupStep{Phase: "complete"}); e != nil {
				return l.backupResult(), e
			}
		} else {
			if step.Phase != "volume-remove" && step.Phase != "task-remove" && step.Phase != "utility-remove" && step.Phase != "complete" {
				source := l.backupAdmissionPrefix(p.AttemptID)
				if e = source.recheckMVPNativeBackupSource(ctx, client, worker, p, execution); e != nil {
					return l.backupResult(), e
				}
			}
			if e = admitMVPNativeBackupStorage(ctx, client, p, execution, l.config.ScopeRoot, imageState(l.events).engine, step); e != nil {
				return l.backupResult(), e
			}
			if e = l.appendEvent(Event{OperationID: id, Action: action, Outcome: "started", Backup: &record}); e != nil {
				return l.backupResult(), e
			}
		}
		observed, e := l.executeMVPNativeBackupStep(ctx, client, p, execution, payloads, sourceFiles, sourceDescriptor, release, signature, record, reconcile)
		outcome, code := "completed", ""
		if e != nil {
			outcome, code = "uncertain", "OFFLINE_SCOPE_BACKUP_EFFECT_UNRESOLVED"
			observed = record
		}
		if je := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: action, Outcome: outcome, Code: code, Backup: &observed}); je != nil {
			return l.backupResult(), je
		}
		if e != nil {
			return l.backupResult(), e
		}
	}
	return l.inspectMVPNativeBackup(ctx, r)
}

func (l *locked) mvpNativeBackupAdmissionView(attempt string, originalHead []byte) (*locked, error) {
	view := l.backupAdmissionPrefix(attempt)
	bad := failure("OFFLINE_SCOPE_NATIVE_BACKUP_ORIGINAL_JOURNAL_PREFIX_REQUIRED")
	if len(view.events) < 1 || len(view.events) >= len(l.events) {
		return nil, bad
	}
	raw, e := regular(l.root, journalName, l.maximumJournalBytes())
	if e != nil {
		return nil, e
	}
	lines := bytes.Split(raw, []byte("\n"))
	if len(lines) != len(l.events)+1 || len(lines[len(lines)-1]) != 0 {
		return nil, bad
	}
	var count int64
	var last string
	for i, event := range view.events {
		var original Event
		if wire.StrictJSON(lines[i], &original) != nil || !reflect.DeepEqual(original, event) || original.PreviousSHA256 != last {
			return nil, bad
		}
		count += int64(len(lines[i]) + 1)
		last = wire.Digest(lines[i])
	}
	if l.events[len(view.events)].PreviousSHA256 != last {
		return nil, bad
	}
	var head journalHead
	expected := journalHead{FormatVersion: scopeFormatVersion, ScopeID: l.marker.ScopeID, ConfigurationSHA256: l.marker.ConfigurationSHA256, Sequence: len(view.events), EventSHA256: last, JournalBytes: count}
	if wire.StrictJSON(originalHead, &head) != nil || !reflect.DeepEqual(head, expected) {
		return nil, bad
	}
	view.lastHead = append([]byte(nil), originalHead...)
	view.lastSHA = last
	view.journalBytes = count
	return view, nil
}

func (l *locked) executeMVPNativeBackupStep(ctx context.Context, c mvpNativeBackupEngine, p mvpNativeBackupPlan, execution mvpNativeBackupExecution, payloads, sourceFiles map[string][]engine.ConfigurationFile, descriptor, release, signature []byte, r BackupRecord, reconcile bool) (BackupRecord, error) {
	if ctx == nil || ctx.Err() != nil {
		return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if r.Phase == "reserve" {
		if !reconcile {
			if e := reserveMVPNativeBackupFiles(p, sourceFiles, release, signature, descriptor); e != nil {
				return r, e
			}
			root, e := openBackupDirectory(p.Request.DestinationRoot)
			if e != nil {
				return r, e
			}
			e = writeBackupFile(root, mvpNativeExecutionFile, encode(execution))
			root.Close()
			if e != nil {
				return r, e
			}
		}
		root, e := openBackupDirectory(p.Request.DestinationRoot)
		if e != nil {
			return r, e
		}
		defer root.Close()
		if e = verifyMVPNativeBackupReservation(ctx, root, p, l.config); e != nil {
			return r, e
		}
		_, e = readMVPNativeExecution(root, l.config, p)
		return r, e
	}
	if volume, ok := execution.Volumes[r.Role]; ok {
		if r.Phase == "volume-remove" {
			if !reconcile {
				if e := c.RemoveNativePostgreSQLVolume(ctx, volume.Owner); e != nil {
					return r, e
				}
			}
			_, found, e := c.InspectVolume(ctx, volume.Owner)
			if e != nil || found {
				return r, failure("OFFLINE_SCOPE_BACKUP_VOLUME_REMOVAL_UNRESOLVED")
			}
			return r, nil
		}
		if r.Phase == "payload-write" && r.Role == "native-scaffold" {
			if !reconcile {
				if _, e := c.PutNativePostgreSQLScaffold(ctx, execution.Writer, execution.Initializer); e != nil {
					return r, e
				}
			}
			observed, e := c.InspectNativePostgreSQLScaffold(ctx, execution.Writer, execution.Initializer)
			if e != nil || observed.Status != engine.ConfigurationPayloadVerified || observed.Files != 0 || observed.Bytes != 0 {
				return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_SCAFFOLD_UNRESOLVED")
			}
			return r, nil
		}
		legacy := BackupPlan{ScopeID: p.ScopeID, AttemptID: p.AttemptID, Request: p.Request, Initializer: execution.Initializer, Volumes: execution.Volumes}
		return l.executeBackupStep(ctx, c, legacy, payloads, sourceFiles, r, reconcile)
	}
	root, e := openBackupDirectory(p.Request.DestinationRoot)
	if e != nil {
		return r, e
	}
	defer root.Close()
	if r.Phase == "archive-publish" && r.Role == "native-archive" {
		return l.publishMVPNativeArchive(ctx, c, root, p, execution, r, reconcile)
	}
	if r.Phase == "archive-publish" && r.Role == "native-verifier-plan" {
		raw, e := regular(root, mvpNativeWriterCompletionFile, l.config.Verification.MaximumMetadataBytes)
		var complete engine.NativePostgreSQLBackupCompletion
		if e != nil || wire.StrictJSON(raw, &complete) != nil || complete.BackupID != p.AttemptID || !wire.ValidSHA256(complete.ManifestSHA256) {
			return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_COMPLETION_REQUIRED")
		}
		original, e := regular(root, mvpNativeDirectory+"/complete.json", l.config.Verification.MaximumMetadataBytes)
		candidate, ce := regular(root, mvpNativeDirectory+"/candidate.json", l.config.Verification.MaximumMetadataBytes)
		if e != nil || ce != nil || wire.Digest(original) != wire.Digest(raw) || wire.Digest(candidate) != complete.ManifestSHA256 {
			return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_COMPLETION_CHANGED")
		}
		task, e := l.mvpNativeVerifierTask(p, execution, complete.ManifestSHA256)
		if e != nil {
			return r, e
		}
		if !reconcile {
			if e = confirmMVPNativePublication(root, mvpNativeVerifierPlanFile, encode(task), l.config.Verification.MaximumMetadataBytes); e != nil {
				return r, e
			}
		}
		retained, e := regular(root, mvpNativeVerifierPlanFile, l.config.Verification.MaximumMetadataBytes)
		if e != nil || wire.Digest(retained) != wire.Digest(encode(task)) {
			return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_VERIFIER_PLAN_CHANGED")
		}
		r.ArtifactSHA256, r.ArtifactBytes = wire.Digest(retained), int64(len(retained))
		return r, nil
	}
	if r.Role == "native-writer" || r.Role == "native-verifier" {
		task := execution.Writer
		files := payloads["native-input"]
		if r.Role == "native-verifier" {
			raw, e := regular(root, mvpNativeVerifierPlanFile, l.config.Verification.MaximumMetadataBytes)
			if e != nil || wire.StrictJSON(raw, &task) != nil || task.NativePostgreSQLBackupVerifier == nil {
				return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_VERIFIER_PLAN_CHANGED")
			}
			expected, e := l.mvpNativeVerifierTask(p, execution, task.NativePostgreSQLBackupVerifier.CandidateSHA256)
			if e != nil || !reflect.DeepEqual(task, expected) {
				return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_VERIFIER_PLAN_CHANGED")
			}
			files = payloads["native-verify-input"]
		}
		id := l.mvpNativeTaskID(p, r.Role)
		switch r.Phase {
		case "task-create":
			if !reconcile {
				if _, e := c.CreateDatabaseTask(ctx, task); e != nil {
					return r, e
				}
			}
			actual, found, e := c.InspectDatabaseTask(ctx, task)
			if e != nil || !found || actual.State.Status != "created" {
				return r, failure("OFFLINE_SCOPE_BACKUP_TASK_CREATION_UNRESOLVED")
			}
			r.ObjectID = actual.ID
			return r, nil
		case "task-execute":
			if !utilityIDPattern.MatchString(id) {
				return r, failure("OFFLINE_SCOPE_BACKUP_TASK_ID_REQUIRED")
			}
			original, e := l.executeMVPNativeTask(ctx, c, p, task, id, files, reconcile)
			if e != nil {
				return r, e
			}
			name := mvpNativeWriterCompletionFile
			if r.Role == "native-verifier" {
				name = mvpNativeObservationFile
			}
			if e = confirmMVPNativePublication(root, name, original, l.config.Verification.MaximumMetadataBytes); e != nil {
				return r, e
			}
			return r, nil
		case "task-remove":
			if !utilityIDPattern.MatchString(id) {
				return r, failure("OFFLINE_SCOPE_BACKUP_TASK_ID_REQUIRED")
			}
			if !reconcile {
				if e = c.RemoveDatabaseTask(ctx, task, id); e != nil {
					return r, e
				}
			}
			_, found, e := c.InspectDatabaseTask(ctx, task)
			if e != nil || found {
				return r, failure("OFFLINE_SCOPE_BACKUP_TASK_REMOVAL_UNRESOLVED")
			}
			return r, nil
		}
	}
	if r.Phase == "manifest-publish" {
		original, e := regular(root, mvpNativeObservationFile, l.config.Verification.MaximumMetadataBytes)
		if e != nil {
			return r, e
		}
		if !reconcile {
			file, e := publishMVPNativeBackupManifest(ctx, root, p, l.config, original)
			if e != nil {
				return r, e
			}
			r.ArtifactSHA256, r.ArtifactBytes = file.SHA256, file.Bytes
			return r, nil
		}
	}
	if r.Phase == "manifest-publish" || r.Phase == "complete" {
		raw, e := regular(root, backupManifestFile, l.config.Verification.MaximumMetadataBytes)
		if e != nil {
			return r, e
		}
		request := configuration.RestoreSourceRequest{FormatVersion: 1, BackupRoot: p.Request.DestinationRoot, ManifestSHA256: wire.Digest(raw), MaximumBytes: p.Request.MaximumBytes, MaximumEntries: p.Request.MaximumEntries}
		source, e := readMVPNativeRestoreSource(ctx, l.config, request)
		if e != nil || !reflect.DeepEqual(source.Plan, p) {
			return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_FINAL_READBACK_REQUIRED")
		}
		if e = confirmMVPNativePublication(root, backupManifestFile, raw, l.config.Verification.MaximumMetadataBytes); e != nil {
			return r, e
		}
		r.ArtifactSHA256, r.ArtifactBytes = wire.Digest(raw), int64(len(raw))
		return r, nil
	}
	return r, failure("OFFLINE_SCOPE_BACKUP_STEP_INVALID")
}

func (l *locked) publishMVPNativeArchive(ctx context.Context, c mvpNativeBackupEngine, root *os.Root, p mvpNativeBackupPlan, execution mvpNativeBackupExecution, r BackupRecord, reconcile bool) (BackupRecord, error) {
	id := l.mvpNativeTaskID(p, "native-writer")
	if !utilityIDPattern.MatchString(id) {
		return r, failure("OFFLINE_SCOPE_BACKUP_TASK_ID_REQUIRED")
	}
	if reconcile {
		observed, e := c.StreamNativePostgreSQLBackupArchive(ctx, execution.Writer, id, p.Request.MaximumBytes, p.Request.MaximumEntries, p.Request.StreamSeconds, io.Discard)
		if e != nil {
			return r, e
		}
		raw, e := regular(root, mvpNativeArchiveDescriptorFile, l.config.Verification.MaximumMetadataBytes)
		var retained engine.NativePostgreSQLArchiveProfile
		if e != nil || wire.StrictJSON(raw, &retained) != nil || retained.EntriesSHA256 != observed.Profile.EntriesSHA256 || retained.Entries != observed.Profile.Entries {
			return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_ARCHIVE_PUBLICATION_UNRESOLVED")
		}
		entries, _, e := inventoryMVPNativeSource(ctx, root, p.Request.MaximumBytes, p.Request.MaximumEntries, l.config.Verification.MaximumMetadataBytes)
		if e != nil {
			return r, e
		}
		if _, e = readMVPNativeArchive(ctx, root, l.config, p, entries); e != nil {
			return r, e
		}
		r.ArtifactSHA256, r.ArtifactBytes = retained.Artifact.SHA256, retained.Artifact.Bytes
		return r, nil
	}
	entries, total, e := inventoryMVPNativeSource(ctx, root, p.Request.MaximumBytes, p.Request.MaximumEntries, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return r, e
	}
	// The original policy and complete coexistence bound were frozen before
	// reservation. Never shrink the SQL/store policy to remaining archive space.
	metadataFiles := []string{mvpNativeArchiveDescriptorFile, mvpNativeVerifierPlanFile, mvpNativeObservationFile, backupManifestFile}
	maximum := p.Storage.NativeTarMaximumBytes
	entryLimit := p.Request.MaximumEntries - len(entries) - len(metadataFiles) - 2
	remaining, ok := mvpNativeStorageSum(total, maximum, p.Storage.NativeOutputMaximumBytes, int64(len(metadataFiles))*l.config.Verification.MaximumMetadataBytes)
	if !ok || remaining > p.Request.MaximumBytes || maximum < 1<<20 || entryLimit < p.NativeRequest.Policy.MaximumStoreFiles+16 {
		return r, failure("OFFLINE_SCOPE_NATIVE_BACKUP_ARCHIVE_BUDGET_REQUIRED")
	}
	partial := mvpNativeArchiveFile + ".partial"
	file, e := root.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return r, failure("OFFLINE_SCOPE_BACKUP_PARTIAL_OCCUPIED")
	}
	observed, streamError := c.StreamNativePostgreSQLBackupArchive(ctx, execution.Writer, id, maximum, entryLimit, p.Request.StreamSeconds, file)
	syncError := file.Sync()
	closeError := file.Close()
	if streamError != nil {
		return r, streamError
	}
	if syncError != nil || closeError != nil {
		return r, failure("OFFLINE_SCOPE_BACKUP_ARTIFACT_SYNC_FAILED")
	}
	if e = finishBackupPublication(ctx, root, mvpNativeArchiveFile, mvpNativeArchiveDescriptorFile, observed.Profile.Artifact.Bytes, observed.Profile.Artifact.SHA256, encode(observed.Profile)); e != nil {
		return r, e
	}
	input, e := root.OpenFile(mvpNativeArchiveFile, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return r, e
	}
	defer input.Close()
	if _, e = extractMVPNativeArchive(ctx, root, input, observed.Profile.Artifact, p.Request, l.config.Verification.MaximumMetadataBytes); e != nil {
		return r, e
	}
	r.ArtifactSHA256, r.ArtifactBytes = observed.Profile.Artifact.SHA256, observed.Profile.Artifact.Bytes
	return r, nil
}

// Abort joins/removes only exact recorded tasks and disposable generated input
// owners. The native artifact volume is retained as recovery evidence, even if
// its host copy is partial; the original SQL/store volumes are never removed.
func (l *locked) abortMVPNativeBackup(ctx context.Context, c mvpNativeBackupEngine, p mvpNativeBackupPlan, e mvpNativeBackupExecution) (BackupResult, error) {
	state := backupState(l.events)
	if state.record == nil || state.terminal {
		return l.backupResult(), failure("OFFLINE_SCOPE_MATCHING_INCOMPLETE_BACKUP_REQUIRED")
	}
	record := *state.record
	record.ObjectID = ""
	record.ArtifactSHA256 = ""
	record.ArtifactBytes = 0
	id, reference := state.pending, state.abortReference
	if !state.aborting {
		var err error
		id, err = nonce()
		if err != nil {
			return l.backupResult(), err
		}
		reference = state.pending
		if err = l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "backup_abort", Outcome: "started", Backup: &record}); err != nil {
			return l.backupResult(), err
		}
	}
	for _, role := range []string{"native-verifier", "native-writer"} {
		created := false
		removed := false
		for _, event := range l.events {
			if event.Backup != nil && event.Backup.AttemptID == p.AttemptID && event.Backup.Role == role {
				if event.Backup.Phase == "task-create" {
					created = true
				}
				if event.Backup.Phase == "task-remove" && event.Outcome == "completed" {
					removed = true
				}
			}
		}
		if !created || removed {
			continue
		}
		task := e.Writer
		if role == "native-verifier" {
			root, err := openBackupDirectory(p.Request.DestinationRoot)
			if err != nil {
				return l.backupResult(), err
			}
			raw, err := regular(root, mvpNativeVerifierPlanFile, l.config.Verification.MaximumMetadataBytes)
			root.Close()
			if err != nil || wire.StrictJSON(raw, &task) != nil {
				return l.backupResult(), failure("OFFLINE_SCOPE_NATIVE_BACKUP_ABORT_TASK_CONTEXT_REQUIRED")
			}
		}
		current, found, err := c.InspectDatabaseTask(ctx, task)
		if err != nil {
			return l.backupResult(), err
		}
		if !found {
			continue
		}
		if current.State.Running {
			if err = c.StopDatabaseTask(ctx, task, current.ID, l.config.Compose.Process.StopSeconds); err != nil {
				return l.backupResult(), err
			}
		}
		if err = c.RemoveDatabaseTask(ctx, task, current.ID); err != nil {
			return l.backupResult(), err
		}
	}
	roles := []string{}
	for role := range e.Volumes {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		if role == "native-artifact" {
			continue
		}
		v := e.Volumes[role]
		_, found, err := c.InspectVolume(ctx, v.Owner)
		if err != nil {
			return l.backupResult(), err
		}
		if !found {
			continue
		}
		if _, present, err := c.InspectUtility(ctx, v.Owner, e.Initializer, v.UID, v.GID); err != nil {
			return l.backupResult(), err
		} else if present {
			if err = c.RemoveUtility(ctx, v.Owner, e.Initializer, v.UID, v.GID); err != nil {
				return l.backupResult(), err
			}
		}
		if err = c.RemoveNativePostgreSQLVolume(ctx, v.Owner); err != nil {
			return l.backupResult(), err
		}
	}
	record.ArtifactSHA256 = wire.Digest(encode(p))
	record.ArtifactBytes = int64(len(encode(p)))
	if err := l.appendEvent(Event{OperationID: id, ReferenceOperationID: reference, Action: "backup_abort", Outcome: "completed", Backup: &record}); err != nil {
		return l.backupResult(), err
	}
	out := l.backupResult()
	out.Qualification = "aborted_original_native_tasks_joined_partial_host_and_native_artifact_volume_retained_for_explicit_recovery"
	return out, nil
}
