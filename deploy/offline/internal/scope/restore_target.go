package scope

import (
	"context"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/compose"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type RestoreTargetResult struct {
	Status               string   `json:"status"`
	SourceScopeID        string   `json:"source_scope_id"`
	TargetScopeID        string   `json:"target_scope_id"`
	BackupManifestSHA256 string   `json:"backup_manifest_sha256"`
	DescriptorSHA256     string   `json:"descriptor_sha256"`
	RuntimeSHA256        string   `json:"runtime_sha256"`
	BindingSHA256        string   `json:"binding_sha256"`
	Stores               []string `json:"stores"`
	Qualification        string   `json:"qualification"`
	CompatibilitySHA256  string   `json:"compatibility_sha256,omitempty"`
}

func InspectRestoreTarget(ctx context.Context, c configuration.Config, r configuration.RestoreSourceRequest) (RestoreTargetResult, error) {
	if c.Engine == nil || c.Compose == nil || c.Compose.Process == nil {
		return RestoreTargetResult{}, failure("OFFLINE_SCOPE_RESTORE_TARGET_CONFIGURATION_REQUIRED")
	}
	source, e := InspectRestoreSource(ctx, c, r)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	l, e := open(c, false)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	defer l.close()
	if e = restoreTargetIdentity(l.marker, source); e != nil {
		return RestoreTargetResult{}, e
	}
	client, e := engine.New(*c.Engine)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	defer client.Close()
	worker, e := compose.New(c)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	return l.inspectRestoreTarget(ctx, r, source, client, worker)
}

func (l *locked) inspectRestoreTarget(ctx context.Context, r configuration.RestoreSourceRequest, source RestoreSourceResult, client runtimeEngine, worker runtimeOperator, compatibility ...*restoreCompatibilityProof) (RestoreTargetResult, error) {
	if e := restoreTargetIdentityAuthorized(l.marker, source, compatibility...); e != nil {
		return RestoreTargetResult{}, e
	}
	v, e := l.verifyRuntimeContext(ctx, client, worker, false)
	if e != nil {
		return RestoreTargetResult{}, e
	}
	if e = restoreTargetLineage(l.marker, l.events, v, source, compatibility...); e != nil {
		return RestoreTargetResult{}, e
	}
	for role, svc := range v.observed.Services {
		object, found, e := client.InspectRuntime(ctx, l.marker.ScopeID, role)
		if e != nil || !found || object.ID != svc.ID {
			return RestoreTargetResult{}, failure("OFFLINE_SCOPE_RESTORE_PEER_CHANGED")
		}
		if role != "postgres" {
			started, e := time.Parse(time.RFC3339Nano, object.State.StartedAt)
			if e != nil || !started.IsZero() || object.State.Status != "created" {
				return RestoreTargetResult{}, failure("OFFLINE_SCOPE_RESTORE_NEVER_STARTED_APPLICATION_REQUIRED")
			}
		}
	}
	if _, e = restoreConnections(v.inputs, v.model); e != nil {
		return RestoreTargetResult{}, e
	}
	if source.Profile == wire.MVPPostgreSQLCPUProfile || l.marker.Profile == wire.MVPPostgreSQLCPUProfile || source.NativePostgreSQL != nil {
		if e = l.verifyMVPNativeRestoreTargetConfiguration(ctx, r, source, v); e != nil {
			return RestoreTargetResult{}, e
		}
	} else {
		plan, files, e := readRestoreSourceConfiguration(ctx, l.config, r, source.PlanSHA256)
		if e != nil {
			return RestoreTargetResult{}, e
		}
		if e = restoreKeyInventory(v.inputs, v.model, plan, files, l.marker); e != nil {
			return RestoreTargetResult{}, e
		}
	}
	result := RestoreTargetResult{Status: "verified_restore_target_lineage_not_restored", SourceScopeID: source.SourceScopeID, TargetScopeID: l.marker.ScopeID, BackupManifestSHA256: r.ManifestSHA256, DescriptorSHA256: v.inputs.record.DescriptorSHA256, RuntimeSHA256: wire.Digest(encode(*v.creation.record)), Stores: append([]string{}, source.Stores...), Qualification: "readonly_fresh_operator_lineage_private_connection_and_key_inventory_only_not_SQL_empty_import_activation_or_target_qualification"}
	sort.Strings(result.Stores)
	if len(compatibility) == 1 && compatibility[0] != nil {
		result.CompatibilitySHA256 = wire.Digest(encode(compatibility[0]))
	}
	result.BindingSHA256 = wire.Digest(encode(result))
	return result, nil
}

func restoreTargetLineage(marker Marker, events []Event, v verifiedRuntimeContext, source RestoreSourceResult, compatibility ...*restoreCompatibilityProof) error {
	if e := restoreTargetIdentityAuthorized(marker, source, compatibility...); e != nil {
		return e
	}
	if !databaseCreationProcessReady(events) || databaseTaskState(events).record != nil || modelTaskState(events).record != nil || backupState(events).record != nil || processState(events).pending != "" {
		return failure("OFFLINE_SCOPE_RESTORE_FRESH_TARGET_LINEAGE_REQUIRED")
	}
	if !v.creation.completed || v.creation.record == nil || v.observed.Status != "observed_complete_compose_resources" || !runtimeIDsMatch(*v.creation.record, v.observed) {
		return failure("OFFLINE_SCOPE_RESTORE_RECORDED_TARGET_REQUIRED")
	}
	for role, s := range v.observed.Services {
		if role == "postgres" && s.State != "running" || role != "postgres" && s.State != "created" {
			return failure("OFFLINE_SCOPE_RESTORE_NEVER_STARTED_APPLICATION_REQUIRED")
		}
	}
	wanted := map[string]bool{}
	for _, role := range source.Stores {
		if wanted[role] {
			return failure("OFFLINE_SCOPE_RESTORE_STORE_SET_MISMATCH")
		}
		wanted[role] = true
	}
	for _, role := range []string{"models", "datasets", "training", "preparations"} {
		_, declared := volumeState(events).roles[role]
		if declared != wanted[role] {
			return failure("OFFLINE_SCOPE_RESTORE_STORE_SET_MISMATCH")
		}
		delete(wanted, role)
	}
	if len(wanted) > 0 {
		return failure("OFFLINE_SCOPE_RESTORE_STORE_SET_MISMATCH")
	}
	return nil
}

func restoreTargetIdentity(marker Marker, source RestoreSourceResult) error {
	if marker.ScopeID == source.SourceScopeID || marker.ReleaseID != source.ReleaseID || marker.Profile != source.Profile {
		return failure("OFFLINE_SCOPE_RESTORE_INDEPENDENT_SAME_RELEASE_TARGET_REQUIRED")
	}
	return nil
}

func readRestoreSourceConfiguration(ctx context.Context, c configuration.Config, r configuration.RestoreSourceRequest, expectedPlan string) (BackupPlan, map[string][]engine.ConfigurationFile, error) {
	var plan BackupPlan
	files := map[string][]engine.ConfigurationFile{}
	root, e := openBackupDirectory(r.BackupRoot)
	if e != nil {
		return plan, nil, e
	}
	defer root.Close()
	raw, e := regular(root, backupPlanFile, c.Verification.MaximumMetadataBytes)
	if e != nil || wire.Digest(raw) != expectedPlan || wire.StrictJSON(raw, &plan) != nil || !validRestoreSourcePlan(plan) {
		return plan, nil, failure("OFFLINE_SCOPE_RESTORE_SOURCE_PLAN_CHANGED")
	}
	for _, f := range plan.ConfigurationFiles {
		if ctx.Err() != nil {
			return plan, nil, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
		}
		raw, e := regular(root, "configuration/"+f.Role+"/"+f.File.Name, c.Verification.MaximumMetadataBytes)
		if e != nil || int64(len(raw)) != f.File.Bytes || wire.Digest(raw) != f.File.SHA256 {
			return plan, nil, failure("OFFLINE_SCOPE_RESTORE_SOURCE_CONFIGURATION_CHANGED")
		}
		files[f.Role] = append(files[f.Role], engine.ConfigurationFile{Name: f.File.Name, Contents: raw})
	}
	return plan, files, nil
}

func restoreKeyInventory(p preparedInputs, model compose.Model, source BackupPlan, files map[string][]engine.ConfigurationFile, marker ...Marker) error {
	policyFiles, e := restoredReleasePolicyFiles(p, model, source, files, marker)
	if e != nil {
		return e
	}
	return restoreRetainedKeyInventory(p, model, source.ConfigurationFiles, files, policyFiles)
}

// The policy owner must validate generation-specific policy files before
// supplying their exact names. This common byte/key check consumes only the
// actual configuration inventory, never a fabricated legacy backup/job plan.
func restoreRetainedKeyInventory(p preparedInputs, model compose.Model, source []BackupConfigurationFile, files map[string][]engine.ConfigurationFile, policyFiles map[string]bool) error {
	// These payloads are intentionally generation-specific. Every other file,
	// including unknown future keys, must remain present and byte-identical.
	replaceable := map[string]bool{"operator-config/control.yaml": true, "operator-config/runtime.yaml": true, "operator-config/operator-dsn": true, "operator-config/runtime-dsn": true, "operator-config/bootstrap-request.json": true, "operator-config/store-request.json": true, "control-config/runtime.yaml": true, "control-config/runtime-dsn": true, "control-config/certificate.pem": true, "control-config/private-key.pem": true, "postgres-config/postgres-password": true}
	for name := range policyFiles {
		replaceable[name] = true
	}
	for _, f := range source {
		if replaceable[f.Role+"/"+f.File.Name] {
			continue
		}
		raw, e := restoreNamedFile(p.files[f.Role], f.File.Name)
		if e != nil || int64(len(raw)) != f.File.Bytes || wire.Digest(raw) != f.File.SHA256 {
			return failure("OFFLINE_SCOPE_RESTORE_RETAINED_KEY_INVENTORY_MISMATCH")
		}
	}
	old, e := restoreEnvelope(files["control-config"], "runtime.yaml")
	if e != nil {
		return e
	}
	current, e := restoreEnvelope(p.files["control-config"], "runtime.yaml")
	if e != nil {
		return e
	}
	oldRefs := []string{old.Security.AuthHMACKeyFile, old.Security.MailKeyringFile, old.SSO.KeyringFile}
	newRefs := []string{current.Security.AuthHMACKeyFile, current.Security.MailKeyringFile, current.SSO.KeyringFile}
	for i, reference := range oldRefs {
		if reference == "" && newRefs[i] == "" {
			continue
		}
		if reference == "" || newRefs[i] == "" || path.Base(reference) != path.Base(newRefs[i]) {
			return failure("OFFLINE_SCOPE_RESTORE_KEY_REFERENCE_MISMATCH")
		}
		previous, e := restoreNamedFile(files["control-config"], path.Base(reference))
		if e != nil {
			return e
		}
		present, e := restorePayloadAt(p, model, "control-config", newRefs[i])
		if e != nil {
			return e
		}
		if string(previous) != string(present) {
			return failure("OFFLINE_SCOPE_RESTORE_KEY_REFERENCE_MISMATCH")
		}
	}
	// The source credential is read only to reject reuse. It is never logged,
	// returned, connected to or persisted in a restore plan.
	previous, e := restoreNamedFile(files["control-config"], path.Base(old.Database.DSNFile))
	if e != nil {
		return e
	}
	u, _, e := restoreURI(previous)
	if e != nil {
		return e
	}
	newConnection, e := restoreConnections(p, model)
	if e != nil {
		return e
	}
	password, _ := u.User.Password()
	if u.User.Username() == newConnection.runtimeRole || password == newConnection.runtimePassword {
		return failure("OFFLINE_SCOPE_RESTORE_RUNTIME_CREDENTIAL_REUSE_REFUSED")
	}
	for _, role := range []string{"operator-config"} {
		for _, name := range []string{"control.yaml", "runtime.yaml"} {
			v, e := restoreEnvelope(p.files[role], name)
			if e != nil {
				return e
			}
			refs := []string{v.Security.AuthHMACKeyFile, v.Security.MailKeyringFile, v.SSO.KeyringFile}
			for i, ref := range refs {
				if path.Base(ref) != path.Base(newRefs[i]) || ref != "" && !strings.HasPrefix(ref, engine.DatabaseTaskDestination+"/private/") {
					return failure("OFFLINE_SCOPE_RESTORE_KEY_REFERENCE_MISMATCH")
				}
			}
		}
	}
	return nil
}
