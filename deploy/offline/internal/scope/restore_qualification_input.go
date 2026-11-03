package scope

import (
	"bytes"
	"context"
	"path"
	"reflect"
	"sort"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const restoredRuntimeContextName = "restored-runtime.json"

// Build only from the immutable target snapshot and a completed SQL journal.
// Credentials remain in memory/protected payloads, never in the result journal.
func (l *locked) makeRestoreQualificationInput(ctx context.Context, plan RestorePlan) ([]engine.ConfigurationFile, error) {
	if ctx.Err() != nil {
		return nil, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
	}
	if requiresMVPNativeRestore(l.marker, plan) && !validMVPNativeRestoreSource(plan.Source) {
		return nil, mvpNativeRestoreRequired()
	}
	reservation := restoreState(l.events)
	if !reservation.reserved || reservation.pending != "" || reservation.aborted || reservation.record == nil || wire.Digest(encode(plan)) != reservation.record.PlanSHA256 || !l.validRestorePlan(plan) {
		return nil, failure("OFFLINE_SCOPE_RESTORE_QUALIFICATION_PLAN_CHANGED")
	}
	if _, ok := restoreSQLProof(l.events); !ok {
		return nil, failure("OFFLINE_SCOPE_RESTORE_QUALIFICATION_SQL_REQUIRED")
	}
	if e := l.verifyRestorePlan(); e != nil {
		return nil, e
	}
	input, outcome, _ := inputsState(l.events)
	if input == nil || outcome != "completed" {
		return nil, failure("OFFLINE_SCOPE_RESTORE_QUALIFICATION_INPUTS_REQUIRED")
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
	if requiresMVPNativeRestore(l.marker, plan) {
		complete, e := l.readMVPNativeRestoreOriginal(ctx, plan)
		if e != nil {
			return nil, e
		}
		return mergeRestoreQualificationInput(p, model, complete.RuntimeContext)
	}
	sql := restoreSQLState(l.events).record
	if sql == nil || sql.Task.Restore == nil || !reflect.DeepEqual(sql.Task.Restore.ExpectedSource, plan.Source.Source) {
		return nil, failure("OFFLINE_SCOPE_RESTORE_QUALIFICATION_SQL_CONTEXT_CHANGED")
	}
	r := wire.RestoredRuntimeContext{FormatVersion: 1, RestoreID: plan.AttemptID, SourceScopeID: plan.Source.SourceScopeID, TargetScopeID: l.marker.ScopeID, BackupManifestSHA256: plan.Request.ManifestSHA256, SQLArchiveSHA256: sql.Task.Restore.ArchiveSHA256, TenantID: plan.Source.Source.TenantID, SchemaVersion: plan.Source.Source.SchemaVersion, TargetDatabase: connection.database, TargetOperator: connection.operator, Reason: plan.Request.Reason}
	return mergeRestoreQualificationInput(p, model, r)
}

func mergeRestoreQualificationInput(p preparedInputs, model compose.Model, r wire.RestoredRuntimeContext) ([]engine.ConfigurationFile, error) {
	bad := func() ([]engine.ConfigurationFile, error) {
		return nil, failure("OFFLINE_SCOPE_RESTORE_QUALIFICATION_CONFIGURATION_INVALID")
	}
	if !r.Valid() {
		return bad()
	}
	connection, e := restoreConnections(p, model)
	if e != nil || connection.database != r.TargetDatabase || connection.operator != r.TargetOperator {
		return bad()
	}
	merged := map[string][]byte{}
	for _, role := range []string{"operator-config", "control-config"} {
		seen := map[string]bool{}
		for _, file := range p.files[role] {
			if !wire.SafePath(file.Name) || path.Base(file.Name) != file.Name || file.Name == restoredRuntimeContextName || seen[file.Name] || len(file.Contents) == 0 {
				return bad()
			}
			seen[file.Name] = true
			if previous, ok := merged[file.Name]; ok && !bytes.Equal(previous, file.Contents) {
				return bad()
			}
			merged[file.Name] = append([]byte{}, file.Contents...)
		}
	}
	// The fixed SDK mount is authoritative for these commands; a reference to
	// another old mount must not silently become a missing key or outside read.
	for _, item := range []struct{ role, name string }{{"operator-config", "control.yaml"}, {"operator-config", "runtime.yaml"}, {"control-config", "runtime.yaml"}} {
		envelope, e := restoreEnvelope(p.files[item.role], item.name)
		if e != nil {
			return bad()
		}
		for _, reference := range []string{envelope.Database.DSNFile, envelope.Security.AuthHMACKeyFile, envelope.Security.MailKeyringFile, envelope.SSO.KeyringFile} {
			if reference == "" {
				continue
			}
			prefix := engine.DatabaseTaskDestination + "/private/"
			if reference != prefix+path.Base(reference) || len(merged[path.Base(reference)]) == 0 {
				return bad()
			}
		}
	}
	merged[restoredRuntimeContextName] = encode(r)
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	files := make([]engine.ConfigurationFile, 0, len(names))
	for _, name := range names {
		files = append(files, engine.ConfigurationFile{Name: name, Contents: merged[name]})
	}
	return files, nil
}

// This canonical index preserves the database stage's historical payload hash.
// Payload contents, including credentials, never enter the public descriptor.
func restoreConfigurationDigest(files []engine.ConfigurationFile) string {
	index := []InputFile{}
	for _, f := range files {
		index = append(index, InputFile{Name: f.Name, Bytes: int64(len(f.Contents)), SHA256: wire.Digest(f.Contents)})
	}
	return wire.Digest(encode(index))
}
