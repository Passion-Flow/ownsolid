package engine

import (
	"context"
	"path"
	"reflect"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const NativePostgreSQLBackup = "backup-kubernetes-postgres"
const NativePostgreSQLRestore = "restore-kubernetes-postgres"
const NativePostgreSQLRestoreVerify = "verify-kubernetes-postgres-restore"
const NativePostgreSQLRecoveryInputRole = "native-pg-config"
const NativePostgreSQLConfigurationRole = "native-pg-projection"
const NativePostgreSQLRecoveryScratchRole = "native-pg-scratch"
const NativePostgreSQLRestoreRecordsRole = "native-pg-records"
const NativePostgreSQLScaffoldRole = "native-pg-scaffold"
const NativePostgreSQLPreparedRole = "native-pg-prepared"
const NativePostgreSQLRegistryRole = "image-registry"
const NativePostgreSQLRecoveryRequestFile = "native-request.json"

// NativePostgreSQLRecoveryProfile selects only the three original fixed native
// commands. Exactly one original request is present. The scope journals this
// complete profile before any create/start, and independently admits the signed
// image, complete private configuration, genuine source and fresh target.
// All owners are exact same-scope local volumes; no host bind is admitted.
// Original native paths and request policy are retained, never rewritten here.
type NativePostgreSQLRecoveryProfile struct {
	BackupRequest           *wire.PostgreSQLBackupRequest         `json:"backup_request,omitempty"`
	RestoreRequest          *wire.PostgreSQLRestoreRequest        `json:"restore_request,omitempty"`
	BackupReference         *wire.NativePostgreSQLBackupReference `json:"backup_reference,omitempty"`
	ConfigurationOwner      Ownership                             `json:"configuration_owner"`
	BackupOwner             Ownership                             `json:"backup_owner"`
	ScratchOwner            Ownership                             `json:"scratch_owner"`
	RecordsOwner            *Ownership                            `json:"records_owner,omitempty"`
	Stores                  [4]NativePostgreSQLStoreMount         `json:"stores"`
	Registry                *NativePostgreSQLRegistryMount        `json:"registry,omitempty"`
	PreparedRelease         *NativePostgreSQLPreparedMount        `json:"prepared_release,omitempty"`
	Scaffold                *NativePostgreSQLVerifierPlaceholder  `json:"scaffold,omitempty"`
	OperatorEnvironmentFile string                                `json:"operator_environment_file,omitempty"`
}

// The fixed order is models, datasets, training, preparations. The mount is the
// existing volume's private child at the exact original configured store root.
type NativePostgreSQLStoreMount struct {
	Owner  Ownership `json:"owner"`
	Target string    `json:"target"`
}

type NativePostgreSQLRegistryMount struct {
	Owner  Ownership `json:"owner"`
	Target string    `json:"target"`
}

type NativePostgreSQLPreparedMount struct {
	Owner  Ownership `json:"owner"`
	Target string    `json:"target"`
}

func (t DatabaseTask) validNativePostgreSQLRecovery() bool {
	p := t.NativePostgreSQLRecovery
	if p == nil || t.NativePostgreSQLRestoreRootsInspection != nil || t.NativePostgreSQLTargetInitialization != nil || t.NativePostgreSQLBackupVerifier != nil || !utilityIdentity(t.Owner, t.Image, t.UID, t.GID) || t.Owner.Role != "database-"+t.Kind || !nativeVerifierOwner(t.VolumeOwner, t.Owner, NativePostgreSQLRecoveryInputRole) || !containerIdentity.MatchString(t.NetworkID) || len(t.Peers) < 1 || len(t.Peers) > 3 || t.Peers["postgres"] == "" || t.LocalizationOwner != nil || t.OutputOwner != nil || t.SourceOwner != nil || t.StoreRestore != nil || t.Reason != "" {
		return false
	}
	seenPeers := map[string]bool{}
	for role, id := range t.Peers {
		if !runtimeService(role) || !containerIdentity.MatchString(id) || seenPeers[id] {
			return false
		}
		seenPeers[id] = true
	}
	if p.OperatorEnvironmentFile != "" && p.OperatorEnvironmentFile != DatabaseTaskDestination+"/private/operator.env" {
		return false
	}
	if !nativeVerifierOwner(p.ConfigurationOwner, t.Owner, NativePostgreSQLConfigurationRole) || !nativeVerifierOwner(p.BackupOwner, t.Owner, NativePostgreSQLArtifactRole) || !nativeVerifierOwner(p.ScratchOwner, t.Owner, NativePostgreSQLRecoveryScratchRole) {
		return false
	}
	switch t.Kind {
	case NativePostgreSQLBackup:
		r := p.BackupRequest
		if r == nil || p.RestoreRequest != nil || p.BackupReference != nil || p.RecordsOwner != nil || !r.Valid() || r.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || r.SourceScopeID != t.Owner.ScopeID || "sha256:"+r.ControlImageSHA256 != t.Image {
			return false
		}
	case NativePostgreSQLRestore, NativePostgreSQLRestoreVerify:
		r := p.RestoreRequest
		b := p.BackupReference
		if r == nil || p.BackupRequest != nil || b == nil || !b.Valid() || !r.Valid() || r.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || r.TargetScopeID != t.Owner.ScopeID || r.SourceManifestSHA256 != b.CandidateSHA256 || p.RecordsOwner == nil || !nativeVerifierOwner(*p.RecordsOwner, t.Owner, NativePostgreSQLRestoreRecordsRole) || (b.RegistryConfigured != (p.Registry != nil)) {
			return false
		}
		if r.TargetRoots != nil {
			for i, root := range r.TargetRoots {
				if root.Path != p.Stores[i].Target || int(root.UID) != t.UID || int(root.GID) != t.GID {
					return false
				}
			}
		}
	default:
		return false
	}
	targets := []string{}
	owners := map[string]bool{t.VolumeOwner.VolumeName(): true}
	for _, m := range t.nativeRecoveryExtraMounts() {
		if !nativeVerifierTarget(m.target) || owners[m.owner.VolumeName()] {
			return false
		}
		owners[m.owner.VolumeName()] = true
		if p.Scaffold != nil && m.owner == p.Scaffold.Owner {
			continue
		}
		for _, target := range targets {
			if nativeTargetsOverlap(m.target, target) {
				return false
			}
		}
		targets = append(targets, m.target)
	}
	roles := []string{"models", "datasets", "training", "preparations"}
	for i, m := range p.Stores {
		if !m.Owner.valid() || m.Owner.ScopeID != t.Owner.ScopeID || m.Owner.Role != roles[i] {
			return false
		}
	}
	config, _, _, _ := t.nativeRecoveryPaths()
	runtime, environment := t.nativeRecoveryRuntimeFiles()
	if !strings.HasPrefix(runtime, config+"/") || environment != "" && !strings.HasPrefix(environment, config+"/") {
		return false
	}
	if p.Registry != nil && (!p.Registry.Owner.valid() || p.Registry.Owner.ScopeID != t.Owner.ScopeID || p.Registry.Owner.Role != NativePostgreSQLRegistryRole || p.Scaffold == nil || !strings.HasPrefix(p.Registry.Target, p.Scaffold.Target+"/")) {
		return false
	}
	if p.PreparedRelease != nil && (!nativeVerifierOwner(p.PreparedRelease.Owner, t.Owner, NativePostgreSQLPreparedRole) || p.Registry == nil) {
		return false
	}
	if p.Scaffold != nil {
		q := p.Scaffold
		if !nativeVerifierOwner(q.Owner, t.Owner, NativePostgreSQLScaffoldRole) {
			return false
		}
		for _, target := range targets {
			if nativeTargetsOverlap(q.Target, target) && !strings.HasPrefix(target, q.Target+"/") {
				return false
			}
		}
	}
	return true
}

// Paths are structural projections of the original typed request.
func (t DatabaseTask) nativeRecoveryPaths() (configuration, backup, records, scratch string) {
	p := t.NativePostgreSQLRecovery
	if r := p.BackupRequest; r != nil {
		return r.ConfigurationRoot, r.BackupRoot, "", r.Policy.ScratchRoot
	}
	r := p.RestoreRequest
	return r.ConfigurationRoot, r.BackupRoot, r.RestoreRoot, r.Policy.ScratchRoot
}
func (t DatabaseTask) nativeRecoveryRuntimeFiles() (string, string) {
	if r := t.NativePostgreSQLRecovery.BackupRequest; r != nil {
		return r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile
	}
	r := t.NativePostgreSQLRecovery.RestoreRequest
	return r.RuntimeConfigurationFile, r.RuntimeEnvironmentFile
}
func (t DatabaseTask) nativeRecoveryPolicy() wire.PostgreSQLBackupPolicy {
	if r := t.NativePostgreSQLRecovery.BackupRequest; r != nil {
		return r.Policy
	}
	return t.NativePostgreSQLRecovery.RestoreRequest.Policy
}
func (t DatabaseTask) nativeRecoveryCommand() []string {
	args := []string{t.Kind, "--config", DatabaseTaskDestination + "/private/control.yaml", "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLRecoveryRequestFile}
	if t.NativePostgreSQLRecovery.OperatorEnvironmentFile != "" {
		args = append(args, "--env-file", t.NativePostgreSQLRecovery.OperatorEnvironmentFile)
	}
	return args
}
func (t DatabaseTask) nativeRecoveryFiles(files []ConfigurationFile) bool {
	var request, config, environment bool
	for _, f := range files {
		switch f.Name {
		case "control.yaml":
			config = true
		case "operator.env":
			environment = true
		case NativePostgreSQLRecoveryRequestFile:
			if t.Kind == NativePostgreSQLBackup {
				var r wire.PostgreSQLBackupRequest
				request = wire.StrictJSON(f.Contents, &r) == nil && reflect.DeepEqual(r, *t.NativePostgreSQLRecovery.BackupRequest)
			} else {
				var r wire.PostgreSQLRestoreRequest
				request = wire.StrictJSON(f.Contents, &r) == nil && reflect.DeepEqual(r, *t.NativePostgreSQLRecovery.RestoreRequest)
			}
		}
	}
	return request && config && (environment == (t.NativePostgreSQLRecovery.OperatorEnvironmentFile != ""))
}
func (t DatabaseTask) nativeRecoveryExtraMounts() []databaseMountProfile {
	p := t.NativePostgreSQLRecovery
	config, backup, records, scratch := t.nativeRecoveryPaths()
	out := []databaseMountProfile{}
	if p.Scaffold != nil {
		out = append(out, databaseMountProfile{p.Scaffold.Owner, p.Scaffold.Target, true, true})
	}
	out = append(out, databaseMountProfile{p.ConfigurationOwner, config, true, true})
	if t.Kind == NativePostgreSQLBackup {
		out = append(out, databaseMountProfile{p.BackupOwner, path.Dir(backup), false, true})
	} else {
		out = append(out, databaseMountProfile{p.BackupOwner, path.Dir(backup), true, true})
		out = append(out, databaseMountProfile{*p.RecordsOwner, path.Dir(records), t.Kind == NativePostgreSQLRestoreVerify, true})
	}
	out = append(out, databaseMountProfile{p.ScratchOwner, scratch, false, true})
	for _, m := range p.Stores {
		out = append(out, databaseMountProfile{m.Owner, m.Target, t.Kind == NativePostgreSQLRestoreVerify, true})
	}
	if p.Registry != nil {
		out = append(out, databaseMountProfile{p.Registry.Owner, p.Registry.Target, t.Kind != NativePostgreSQLRestore, true})
	}
	if p.PreparedRelease != nil {
		out = append(out, databaseMountProfile{p.PreparedRelease.Owner, p.PreparedRelease.Target, true, true})
	}
	return out
}
func (c *Client) nativeRecoveryVolumes(ctx context.Context, t DatabaseTask) error {
	for _, m := range t.nativeRecoveryExtraMounts() {
		if _, found, e := c.InspectVolume(ctx, m.owner); e != nil {
			return e
		} else if !found {
			return failure("OFFLINE_ENGINE_NATIVE_RECOVERY_VOLUME_REQUIRED")
		}
	}
	return nil
}
func (c *Client) nativeRecoveryConsumers(ctx context.Context, t DatabaseTask, id string) error {
	for _, m := range t.nativeRecoveryExtraMounts() {
		allowed := []string{}
		if id != "" {
			allowed = append(allowed, id)
		}
		if m.owner.Role == "models" || m.owner.Role == "datasets" || m.owner.Role == "training" || m.owner.Role == "preparations" || m.owner.Role == NativePostgreSQLRegistryRole {
			if peer := t.Peers["control"]; peer != "" {
				allowed = append(allowed, peer)
			}
		}
		if e := c.VerifyVolumeConsumers(ctx, m.owner, allowed); e != nil {
			return e
		}
	}
	return nil
}

// ValidateNativePostgreSQLRecoveryTask validates the frozen fixed task shape;
// it does not replace current Engine, native source, SQL or filesystem checks.
func ValidateNativePostgreSQLRecoveryTask(t DatabaseTask) error {
	if !t.validNativePostgreSQLRecovery() {
		return failure("OFFLINE_ENGINE_NATIVE_RECOVERY_TASK_INVALID")
	}
	return nil
}
