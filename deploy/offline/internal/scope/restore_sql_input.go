package scope

import (
	"context"
	"reflect"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func (l *locked) readRestoreSQLSource(ctx context.Context, plan RestorePlan) (engine.PostgresBackupArchive, BackupPlan, error) {
	var archive engine.PostgresBackupArchive
	source, e := InspectRestoreSource(ctx, l.config, plan.Request.RestoreSourceRequest)
	if e != nil || !reflect.DeepEqual(source, plan.Source) {
		return archive, BackupPlan{}, failure("OFFLINE_SCOPE_RESTORE_SQL_SOURCE_CHANGED")
	}
	root, e := openBackupDirectory(plan.Request.BackupRoot)
	if e != nil {
		return archive, BackupPlan{}, e
	}
	defer root.Close()
	raw, e := regular(root, backupManifestFile, l.config.Verification.MaximumMetadataBytes)
	var manifest BackupManifest
	if e != nil || wire.Digest(raw) != plan.Request.ManifestSHA256 || wire.StrictJSON(raw, &manifest) != nil {
		return archive, BackupPlan{}, failure("OFFLINE_SCOPE_RESTORE_SQL_MANIFEST_CHANGED")
	}
	metadata, e := regular(root, "postgres.json", l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.StrictJSON(metadata, &archive) != nil || archive.Status != "verified_exited_postgres_custom_archive_bytes" || archive.Source != plan.Source.Source || len(archive.Files) != 4 {
		return archive, BackupPlan{}, failure("OFFLINE_SCOPE_RESTORE_SQL_METADATA_CHANGED")
	}
	boundArchive, boundMetadata := false, false
	for _, file := range manifest.Files {
		if file.Name == "postgres.json" {
			boundMetadata = file.Bytes == int64(len(metadata)) && file.SHA256 == wire.Digest(metadata)
		}
		if file.Name == "postgres.tar" {
			boundArchive = file.Bytes == archive.Bytes && file.SHA256 == archive.SHA256
		}
	}
	if !boundArchive || !boundMetadata {
		return archive, BackupPlan{}, failure("OFFLINE_SCOPE_RESTORE_SQL_ARTIFACT_BINDING_INVALID")
	}
	var original BackupPlan
	raw, e = regular(root, backupPlanFile, l.config.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != plan.Source.PlanSHA256 || wire.StrictJSON(raw, &original) != nil || !validRestoreSourcePlan(original) {
		return archive, original, failure("OFFLINE_SCOPE_RESTORE_SQL_SOURCE_PLAN_CHANGED")
	}
	return archive, original, nil
}

func (l *locked) makeRestoreSQLRecord(ctx context.Context, plan RestorePlan) (RestoreSQLRecord, []engine.ConfigurationFile, error) {
	var record RestoreSQLRecord
	archive, original, e := l.readRestoreSQLSource(ctx, plan)
	if e != nil {
		return record, nil, e
	}
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return record, nil, failure("OFFLINE_SCOPE_RESTORE_SQL_INPUTS_REQUIRED")
	}
	p, e := l.readInputs(ctx, *input)
	if e != nil {
		return record, nil, e
	}
	model, e := compose.DecodeModel(p.model, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return record, nil, e
	}
	connection, e := restoreConnections(p, model)
	if e != nil {
		return record, nil, e
	}
	uid, gid, e := roleUser(model, "postgres")
	source := original.Jobs["postgres"].Postgres
	if e != nil || source == nil || source.UID != uid || source.GID != gid {
		return record, nil, failure("OFFLINE_SCOPE_RESTORE_SQL_NUMERIC_OWNER_MISMATCH")
	}
	runtime := runtimeState(l.events)
	if !runtime.completed || runtime.record == nil || plan.Target.RuntimeSHA256 != wire.Digest(encode(*runtime.record)) || plan.Target.DescriptorSHA256 != p.record.DescriptorSHA256 {
		return record, nil, failure("OFFLINE_SCOPE_RESTORE_SQL_RECORDED_TARGET_REQUIRED")
	}
	owner := func(role string) engine.Ownership {
		return engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: plan.AttemptID, Role: role + "-" + plan.AttemptID}
	}
	peers := map[string]string{}
	for role, s := range runtime.record.Services {
		peers[role] = s.ID
	}
	task := engine.PostgresBackupTask{Owner: engine.Ownership{ScopeID: l.marker.ScopeID, OperationID: plan.AttemptID, Role: "database-" + engine.PostgresRestoreKind}, InputOwner: owner(engine.PostgresRestoreInputRole), OutputOwner: owner(engine.PostgresRestoreOutputRole), Image: model.Services["postgres"].Image, UID: uid, GID: gid, NetworkID: runtime.record.NetworkID, Peers: peers, Host: "postgres", Port: connection.port, Database: connection.database, User: connection.operator, Restore: &engine.PostgresRestoreProfile{SourceOwner: owner(engine.PostgresRestoreSourceRole), SourceUID: uid, SourceGID: gid, ArchiveSHA256: archive.SHA256, ArchiveBytes: archive.Bytes, ExpectedSource: archive.Source, ExpectedFiles: archive.Files}}
	reservation := restoreState(l.events).record
	if reservation == nil || reservation.AttemptID != plan.AttemptID {
		return record, nil, failure("OFFLINE_SCOPE_RESTORE_RESERVATION_NOT_RECORDED")
	}
	record = RestoreSQLRecord{PlanSHA256: reservation.PlanSHA256, Initializer: p.descriptor.ImageID, Task: task, Phase: "volume-create", Role: "input"}
	if !validRestoreSQLRecord(&record, reservation, l.marker.ScopeID) {
		return record, nil, failure("OFFLINE_SCOPE_RESTORE_SQL_CONTEXT_INVALID")
	}
	password, e := engine.PostgresBackupPasswordFile(task, connection.password)
	if e != nil {
		return record, nil, e
	}
	return record, []engine.ConfigurationFile{password}, nil
}

func (l *locked) restoreSQLPassword(ctx context.Context, task engine.PostgresBackupTask) ([]engine.ConfigurationFile, error) {
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return nil, failure("OFFLINE_SCOPE_RESTORE_SQL_INPUTS_REQUIRED")
	}
	p, e := l.readInputs(ctx, *input)
	if e != nil {
		return nil, e
	}
	model, e := compose.DecodeModel(p.model, l.config.Verification.MaximumMetadataBytes)
	if e != nil {
		return nil, e
	}
	connection, e := restoreConnections(p, model)
	if e != nil {
		return nil, e
	}
	if task.Database != connection.database || task.User != connection.operator || task.Port != connection.port {
		return nil, failure("OFFLINE_SCOPE_RESTORE_SQL_TARGET_CONTEXT_CHANGED")
	}
	file, e := engine.PostgresBackupPasswordFile(task, connection.password)
	if e != nil {
		return nil, e
	}
	return []engine.ConfigurationFile{file}, nil
}
