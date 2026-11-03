package scope

import (
	"context"
	"os"
	"reflect"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type mvpNativeRestoreEngine interface {
	storageEngine
	runtimeEngine
	CreateDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, error)
	InspectDatabaseTask(context.Context, engine.DatabaseTask) (engine.RuntimeContainer, bool, error)
	StartDatabaseTask(context.Context, engine.DatabaseTask, string, []engine.ConfigurationFile, int) (engine.ResourceAdmission, error)
	RemoveDatabaseTask(context.Context, engine.DatabaseTask, string) error
	ObserveDatabaseTaskOutput(context.Context, engine.DatabaseTask, string) (*engine.TaskOutputObserver, error)
	ReadNativePostgreSQLTaskCompletion(context.Context, engine.DatabaseTask, string) (engine.TaskOutputObservation, error)
	PutNativePostgreSQLBackupArchive(context.Context, engine.DatabaseTask, string, *os.File, engine.NativePostgreSQLArchiveProfile) (engine.NativePostgreSQLArchiveProfile, error)
	InspectNativePostgreSQLBackupArchiveInput(context.Context, engine.DatabaseTask, string, engine.NativePostgreSQLArchiveProfile) (engine.NativePostgreSQLArchiveProfile, error)
	PutNativePostgreSQLScaffold(context.Context, engine.DatabaseTask, string) (engine.ConfigurationObservation, error)
	InspectNativePostgreSQLScaffold(context.Context, engine.DatabaseTask, string) (engine.ConfigurationObservation, error)
	RemoveNativePostgreSQLVolume(context.Context, engine.Ownership) error
}

var _ mvpNativeRestoreEngine = (*engine.Client)(nil)

// All calls here are under the already durable native-prepare intent. A partial
// preparation is preserved for diagnosis; reconciliation never enters here.
func (l *locked) provisionNativeRestoreVolume(ctx context.Context, c mvpNativeRestoreEngine, p mvpNativeRestorePreparation, owner engine.Ownership, files []engine.ConfigurationFile, fill func() error) error {
	if _, found, e := c.InspectVolume(ctx, owner); e != nil {
		return e
	} else if found {
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_FRESH_VOLUME_REQUIRED")
	}
	if _, e := c.CreateVolume(ctx, owner); e != nil {
		return e
	}
	uid, gid := p.RootsTask.UID, p.RootsTask.GID
	if _, e := c.CreateUtility(ctx, owner, p.Initializer, uid, gid); e != nil {
		return e
	}
	if e := c.PutConfiguration(ctx, owner, p.Initializer, uid, gid, files); e != nil {
		return e
	}
	if _, e := c.InspectConfiguration(ctx, owner, p.Initializer, uid, gid, files); e != nil {
		return e
	}
	if fill != nil {
		if e := fill(); e != nil {
			return e
		}
	}
	if e := c.RemoveUtility(ctx, owner, p.Initializer, uid, gid); e != nil {
		return e
	}
	if _, found, e := c.InspectUtility(ctx, owner, p.Initializer, uid, gid); e != nil {
		return e
	} else if found {
		return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_UTILITY_REMOVAL_UNRESOLVED")
	}
	return nil
}

// Bounded one-start transport for original readonly producers/target initializer.
// Unknown starts and lost stdout are unresolved and never retried.
func (l *locked) observeNativeRestoreTask(ctx context.Context, c mvpNativeRestoreEngine, t engine.DatabaseTask, id string, files []engine.ConfigurationFile) (engine.TaskOutputObservation, error) {
	p := l.config.Compose.Process
	bounded, cancel := context.WithTimeout(ctx, time.Duration(p.ObserveSeconds)*time.Second)
	defer cancel()
	actual, found, e := c.InspectDatabaseTask(bounded, t)
	if e != nil {
		return engine.TaskOutputObservation{}, e
	}
	start, e := time.Parse(time.RFC3339Nano, actual.State.StartedAt)
	if e != nil || !found || actual.ID != id || actual.State.Status != "created" || !start.IsZero() || actual.State.Running || actual.State.OOMKilled {
		return engine.TaskOutputObservation{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_NEVER_STARTED_REQUIRED")
	}
	out, e := c.ObserveDatabaseTaskOutput(bounded, t, id)
	if e != nil {
		return engine.TaskOutputObservation{}, e
	}
	defer out.Close()
	if _, e = c.StartDatabaseTask(bounded, t, id, files, p.ReservePercent); e != nil {
		return engine.TaskOutputObservation{}, e
	}
	for {
		actual, found, e = c.InspectDatabaseTask(bounded, t)
		if e != nil {
			return engine.TaskOutputObservation{}, e
		}
		if !found || actual.ID != id {
			return engine.TaskOutputObservation{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
		}
		if mvpNativeRestoreActorExited(actual, id) {
			break
		}
		if actual.State.Status != "running" || !actual.State.Running || actual.State.OOMKilled || actual.State.Dead {
			return engine.TaskOutputObservation{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_TASK_UNRESOLVED")
		}
		timer := time.NewTimer(time.Duration(p.PollMilliseconds) * time.Millisecond)
		select {
		case <-bounded.Done():
			timer.Stop()
			return engine.TaskOutputObservation{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_TASK_UNRESOLVED")
		case <-timer.C:
		}
	}
	value, e := out.Wait(bounded)
	if e != nil {
		return value, e
	}
	if value.Redacted || value.StdoutCode != "" || value.StderrCode != "" {
		return value, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_STDOUT_REQUIRED")
	}
	return value, nil
}

// A preparation actor's exact specification and actual create ID are retained
// before start. Its original output is durable before normal remove/absence.
func (l *locked) runNativePreparationActor(ctx context.Context, c mvpNativeRestoreEngine, t engine.DatabaseTask, files []engine.ConfigurationFile, evidence *[]InputFile) (engine.TaskOutputObservation, error) {
	id, e := nonce()
	if e != nil {
		return engine.TaskOutputObservation{}, e
	}
	prefix := "mvp-native-prepare-" + id
	if e = writeBackupFile(l.root, prefix+"-task.json", encode(t)); e != nil {
		return engine.TaskOutputObservation{}, e
	}
	actual, e := c.CreateDatabaseTask(ctx, t)
	if e != nil {
		return engine.TaskOutputObservation{}, e
	}
	if !utilityIDPattern.MatchString(actual.ID) {
		return engine.TaskOutputObservation{}, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_CHANGED")
	}
	if e = writeBackupFile(l.root, prefix+"-id.json", encode(struct {
		ID string `json:"id"`
	}{actual.ID})); e != nil {
		return engine.TaskOutputObservation{}, e
	}
	out, e := l.observeNativeRestoreTask(ctx, c, t, actual.ID, files)
	if e != nil {
		return out, e
	}
	var raw []byte
	switch {
	case out.NativePostgreSQLRestoreRootsInspection != nil:
		raw = out.NativePostgreSQLRestoreRootsInspection.Original
	case out.NativePostgreSQLTargetInitialization != nil:
		raw = out.NativePostgreSQLTargetInitialization.Original
	case out.NativePostgreSQLBackup != nil:
		raw = out.NativePostgreSQLBackup.Original
	default:
		return out, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_STDOUT_REQUIRED")
	}
	if len(raw) == 0 {
		return out, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ORIGINAL_STDOUT_REQUIRED")
	}
	if e = writeBackupFile(l.root, prefix+"-output.json", raw); e != nil {
		return out, e
	}
	for _, item := range []struct {
		name string
		raw  []byte
	}{{prefix + "-task.json", encode(t)}, {prefix + "-id.json", encode(struct {
		ID string `json:"id"`
	}{actual.ID})}, {prefix + "-output.json", raw}} {
		*evidence = append(*evidence, InputFile{Name: item.name, Bytes: int64(len(item.raw)), SHA256: wire.Digest(item.raw)})
	}
	if e = c.RemoveDatabaseTask(ctx, t, actual.ID); e != nil {
		return out, e
	}
	if _, found, e := c.InspectDatabaseTask(ctx, t); e != nil {
		return out, e
	} else if found {
		return out, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ACTOR_REMOVAL_UNRESOLVED")
	}
	return out, nil
}

func (l *locked) prepareMVPNativeRestore(ctx context.Context, c mvpNativeRestoreEngine, plan RestorePlan, p mvpNativeRestorePreparation) (mvpNativeRestoreExecution, error) {
	var x mvpNativeRestoreExecution
	inputs, model, connection, e := l.nativeRestoreInputs(ctx)
	if e != nil {
		return x, e
	}
	projection, e := projectMVPNativeConfiguration(inputs, model, p.Descriptor)
	if e != nil {
		return x, e
	}
	if projection.OperatorSHA256 != p.OperatorPayloadSHA256 || projection.ConfigurationSHA256 != p.RuntimePayloadSHA256 {
		return x, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_PREPARATION_CHANGED")
	}
	source, e := readMVPNativeRestoreSource(ctx, l.config, plan.Request.RestoreSourceRequest)
	if e != nil {
		return x, e
	}
	if !reflect.DeepEqual(source.Result, plan.Source) || source.ArchiveSHA256 != p.SourceArchiveSHA256 || source.NativeArchiveProfile != p.Archive {
		return x, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_SOURCE_CHANGED")
	}
	own := func(op, role string) engine.Ownership {
		return engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: op, Role: role + "-" + op}
	}
	owners := []engine.Ownership{}
	preparationFiles := []InputFile{}
	provision := func(o engine.Ownership, files []engine.ConfigurationFile, fill func() error) error {
		owners = append(owners, o)
		return l.provisionNativeRestoreVolume(ctx, c, p, o, files, fill)
	}
	if e = provision(p.RootsTask.NativePostgreSQLRestoreRootsInspection.ConfigurationOwner, projection.ConfigurationFiles, nil); e != nil {
		return x, e
	}
	backup := own(plan.AttemptID, engine.NativePostgreSQLArtifactRole)
	verifier := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: plan.AttemptID, Role: "database-" + engine.NativePostgreSQLBackupVerify}, VolumeOwner: own(plan.AttemptID, engine.NativePostgreSQLVerifierInputRole), Kind: engine.NativePostgreSQLBackupVerify, Image: p.RootsTask.Image, UID: p.RootsTask.UID, GID: p.RootsTask.GID, NativePostgreSQLBackupVerifier: &engine.NativePostgreSQLBackupVerifierProfile{Request: p.SourceRequest, CandidateSHA256: plan.Source.NativePostgreSQL.CandidateSHA256, BackupOwner: backup, BackupParent: true, ScratchOwner: own(plan.AttemptID, engine.NativePostgreSQLVerifierScratchRole)}}
	if e = provision(backup, nil, func() error {
		f, e := os.OpenFile(source.NativeArchiveFile, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		defer f.Close()
		observed, e := c.PutNativePostgreSQLBackupArchive(ctx, verifier, p.Initializer, f, p.Archive)
		if e != nil {
			return e
		}
		if observed != p.Archive {
			return failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ARCHIVE_CHANGED")
		}
		return nil
	}); e != nil {
		return x, e
	}
	verifierFiles := []engine.ConfigurationFile{{Name: engine.NativePostgreSQLVerifierRequestFile, Contents: encode(p.SourceRequest)}}
	for _, v := range []struct {
		o engine.Ownership
		f []engine.ConfigurationFile
	}{{verifier.VolumeOwner, verifierFiles}, {verifier.NativePostgreSQLBackupVerifier.ScratchOwner, nil}, {p.RootsTask.VolumeOwner, []engine.ConfigurationFile{{Name: engine.NativePostgreSQLRestoreRootsRequestFile, Contents: encode(p.RootsTask.NativePostgreSQLRestoreRootsInspection.Request)}}}} {
		if e = provision(v.o, v.f, nil); e != nil {
			return x, e
		}
	}
	verified, e := l.runNativePreparationActor(ctx, c, verifier, verifierFiles, &preparationFiles)
	if e != nil {
		return x, e
	}
	if verified.NativePostgreSQLBackup == nil || !reflect.DeepEqual(verified.NativePostgreSQLBackup.NativePostgreSQL, plan.Source.NativePostgreSQL) {
		return x, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_SOURCE_VERIFICATION_CHANGED")
	}
	rootsFiles := []engine.ConfigurationFile{{Name: engine.NativePostgreSQLRestoreRootsRequestFile, Contents: encode(p.RootsTask.NativePostgreSQLRestoreRootsInspection.Request)}}
	roots, e := l.runNativePreparationActor(ctx, c, p.RootsTask, rootsFiles, &preparationFiles)
	if e != nil {
		return x, e
	}
	if roots.NativePostgreSQLRestoreRootsInspection == nil {
		return x, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_ROOTS_REQUIRED")
	}
	request, e := p.Descriptor.BindRestoreObservedRoots(configuration.NativePostgreSQLRestoreBinding{ScopeID: l.marker.ScopeID, AttemptID: plan.AttemptID, SignedSourceSHA256: l.marker.ManifestSHA256, ControlImageSHA256: p.SourceRequest.ControlImageSHA256, PostgresImageSHA256: p.SourceRequest.PostgresImageSHA256, SourceRequest: p.SourceRequest, SourceReference: *plan.Source.NativePostgreSQL, TargetDatabase: connection.database, TargetOperator: connection.operator, Reason: plan.Request.Reason, UID: p.RootsTask.UID, GID: p.RootsTask.GID}, p.RootsTask.NativePostgreSQLRestoreRootsInspection.Request, roots.NativePostgreSQLRestoreRootsInspection.Observation)
	if e != nil {
		return x, e
	}
	discoveryID, e := nonce()
	if e != nil {
		return x, e
	}
	discovery, e := p.Descriptor.BindRestoreTargetInspection(request, configuration.NativePostgreSQLRestoreTargetBinding{InitializationID: discoveryID, Binding: p.RootsTask.NativePostgreSQLRestoreRootsInspection.Request.Binding, SourceDatabase: p.SourceRequest.Database, Endpoint: wire.PostgreSQLRestoreTargetInitializationEndpoint{Host: "postgres", Port: connection.port, SSLMode: "disable"}})
	if e != nil {
		return x, e
	}
	targetTask := func(r wire.PostgreSQLRestoreTargetInitializationRequest, kind string) engine.DatabaseTask {
		return engine.DatabaseTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: r.InitializationID, Role: "database-" + kind}, VolumeOwner: own(r.InitializationID, engine.NativePostgreSQLTargetInputRole), Kind: kind, Image: p.RootsTask.Image, UID: p.RootsTask.UID, GID: p.RootsTask.GID, NetworkID: p.RootsTask.NetworkID, Peers: p.RootsTask.Peers, NativePostgreSQLTargetInitialization: &engine.NativePostgreSQLTargetInitializationProfile{Request: r, RecordsOwner: own(r.InitializationID, engine.NativePostgreSQLTargetRecordsRole)}}
	}
	runTarget := func(r wire.PostgreSQLRestoreTargetInitializationRequest, kind string) (engine.TaskOutputObservation, error) {
		task := targetTask(r, kind)
		if e := engine.ValidateNativePostgreSQLTargetInitializationTask(task); e != nil {
			return engine.TaskOutputObservation{}, e
		}
		files := append(append([]engine.ConfigurationFile(nil), projection.OperatorFiles...), engine.ConfigurationFile{Name: engine.NativePostgreSQLTargetRequestFile, Contents: encode(r)})
		if e := provision(task.VolumeOwner, files, nil); e != nil {
			return engine.TaskOutputObservation{}, e
		}
		if e := provision(task.NativePostgreSQLTargetInitialization.RecordsOwner, nil, nil); e != nil {
			return engine.TaskOutputObservation{}, e
		}
		return l.runNativePreparationActor(ctx, c, task, files, &preparationFiles)
	}
	inspected, e := runTarget(discovery, wire.PostgreSQLRestoreTargetInspectCommand)
	if e != nil {
		return x, e
	}
	if inspected.NativePostgreSQLTargetInitialization == nil {
		return x, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_TARGET_INSPECTION_REQUIRED")
	}
	initID, e := nonce()
	if e != nil {
		return x, e
	}
	init, e := p.Descriptor.BindRestoreTargetInitialization(discovery, inspected.NativePostgreSQLTargetInitialization.Observation, initID)
	if e != nil {
		return x, e
	}
	initialized, e := runTarget(init, wire.PostgreSQLRestoreTargetInitializeCommand)
	if e != nil {
		return x, e
	}
	if initialized.NativePostgreSQLTargetInitialization == nil || !initialized.NativePostgreSQLTargetInitialization.Observation.ValidFor(init, false) || !initialized.NativePostgreSQLTargetInitialization.Observation.OriginalCompletionPresent {
		return x, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_TARGET_INITIALIZATION_REQUIRED")
	}
	stores, registry, e := l.mvpNativeRestoreDomainMounts(model, p.Descriptor)
	if e != nil {
		return x, e
	}
	records := own(plan.AttemptID, engine.NativePostgreSQLRestoreRecordsRole)
	profile := &engine.NativePostgreSQLRecoveryProfile{RestoreRequest: &request, BackupReference: plan.Source.NativePostgreSQL, ConfigurationOwner: p.RootsTask.NativePostgreSQLRestoreRootsInspection.ConfigurationOwner, BackupOwner: backup, ScratchOwner: own(plan.AttemptID, engine.NativePostgreSQLRecoveryScratchRole), RecordsOwner: &records, Stores: stores, Registry: &registry, Scaffold: &engine.NativePostgreSQLVerifierPlaceholder{Owner: own(plan.AttemptID, engine.NativePostgreSQLScaffoldRole), Target: p.Descriptor.RegistryScaffoldRoot}}
	writer := engine.DatabaseTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: plan.AttemptID, Role: "database-" + engine.NativePostgreSQLRestore}, VolumeOwner: own(plan.AttemptID, engine.NativePostgreSQLRecoveryInputRole), Kind: engine.NativePostgreSQLRestore, Image: p.RootsTask.Image, UID: p.RootsTask.UID, GID: p.RootsTask.GID, NetworkID: p.RootsTask.NetworkID, Peers: p.RootsTask.Peers, NativePostgreSQLRecovery: profile}
	if e = engine.ValidateNativePostgreSQLRecoveryTask(writer); e != nil {
		return x, e
	}
	files := append(append([]engine.ConfigurationFile(nil), projection.OperatorFiles...), engine.ConfigurationFile{Name: engine.NativePostgreSQLRecoveryRequestFile, Contents: encode(request)})
	for _, v := range []struct {
		o engine.Ownership
		f []engine.ConfigurationFile
	}{{writer.VolumeOwner, files}, {profile.ScratchOwner, nil}, {records, nil}, {profile.Scaffold.Owner, nil}} {
		if e = provision(v.o, v.f, nil); e != nil {
			return x, e
		}
	}
	// Scaffold has its own strict empty-directory projection. Reopen only that
	// exact utility temporarily; do not fabricate a prepared release tree.
	if _, e = c.CreateUtility(ctx, profile.Scaffold.Owner, p.Initializer, writer.UID, writer.GID); e != nil {
		return x, e
	}
	if _, e = c.PutNativePostgreSQLScaffold(ctx, writer, p.Initializer); e != nil {
		return x, e
	}
	if _, e = c.InspectNativePostgreSQLScaffold(ctx, writer, p.Initializer); e != nil {
		return x, e
	}
	if e = c.RemoveUtility(ctx, profile.Scaffold.Owner, p.Initializer, writer.UID, writer.GID); e != nil {
		return x, e
	}
	x = mvpNativeRestoreExecution{FormatVersion: 1, PreparationSHA256: wire.Digest(encode(p)), SourceArchiveSHA256: p.SourceArchiveSHA256, NativeRequest: request, Initializer: p.Initializer, Writer: writer, TemporaryOwners: owners, PreparationFiles: preparationFiles}
	if e = writeBackupFile(l.root, mvpNativeRestoreExecutionFile, encode(x)); e != nil {
		return x, e
	}
	return x, nil
}

func (l *locked) nativeRestoreTaskFiles(ctx context.Context, x mvpNativeRestoreExecution) ([]engine.ConfigurationFile, error) {
	p, e := l.readMVPNativeRestorePreparation()
	if e != nil {
		return nil, e
	}
	inputs, m, _, e := l.nativeRestoreInputs(ctx)
	if e != nil {
		return nil, e
	}
	projection, e := projectMVPNativeConfiguration(inputs, m, p.Descriptor)
	if e != nil {
		return nil, e
	}
	if projection.OperatorSHA256 != p.OperatorPayloadSHA256 || projection.ConfigurationSHA256 != p.RuntimePayloadSHA256 {
		return nil, failure("OFFLINE_SCOPE_MVP_NATIVE_RESTORE_PREPARATION_CHANGED")
	}
	return append(append([]engine.ConfigurationFile(nil), projection.OperatorFiles...), engine.ConfigurationFile{Name: engine.NativePostgreSQLRecoveryRequestFile, Contents: encode(x.NativeRequest)}), nil
}
