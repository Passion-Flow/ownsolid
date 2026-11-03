package engine

import (
	"context"
	"path"
	"reflect"
	"sort"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const NativePostgreSQLBackupVerify = "verify-kubernetes-postgres-backup"
const NativePostgreSQLVerifierInputRole = "native-pg-verify-config"
const NativePostgreSQLArtifactRole = "native-pg-backup"
const NativePostgreSQLVerifierScratchRole = "native-pg-verify-scratch"
const NativePostgreSQLVerifierPlaceholderRole = "native-pg-verify-base"
const NativePostgreSQLVerifierRequestFile = "native-backup-request.json"

// This profile permits only the original network-free readonly backup verifier.
// BackupOwner is an artifact volume, never a fifth domain-store namespace.
// A placeholder is an empty owned private directory at an archived declaration;
// it must never point to a live registry, prepared release or host bind.
// The scope must initialize and verify its exact empty namespace directories
// through the original never-started volume initializer before task creation.
type NativePostgreSQLBackupVerifierProfile struct {
	BackupParent    bool                                 `json:"backup_parent,omitempty"`
	Request         wire.PostgreSQLBackupRequest         `json:"request"`
	CandidateSHA256 string                               `json:"candidate_sha256"`
	BackupOwner     Ownership                            `json:"backup_owner"`
	ScratchOwner    Ownership                            `json:"scratch_owner"`
	Placeholder     *NativePostgreSQLVerifierPlaceholder `json:"placeholder,omitempty"`
}

type NativePostgreSQLVerifierPlaceholder struct {
	Owner  Ownership `json:"owner"`
	Target string    `json:"target"`
}

func (t DatabaseTask) validNativePostgreSQLVerifier() bool {
	p := t.NativePostgreSQLBackupVerifier
	if p == nil || t.NativePostgreSQLRestoreRootsInspection != nil || t.NativePostgreSQLTargetInitialization != nil || t.NativePostgreSQLRecovery != nil || t.Kind != NativePostgreSQLBackupVerify || !utilityIdentity(t.Owner, t.Image, t.UID, t.GID) || t.Owner.Role != "database-"+NativePostgreSQLBackupVerify || t.NetworkID != "" || len(t.Peers) != 0 || t.LocalizationOwner != nil || t.OutputOwner != nil || t.SourceOwner != nil || t.StoreRestore != nil || t.Reason != "" || !p.Request.Valid() || p.Request.ApplicationProfile != wire.MVPPostgreSQLCPUApplicationProfile || "sha256:"+p.Request.ControlImageSHA256 != t.Image || !wire.ValidSHA256(p.CandidateSHA256) {
		return false
	}
	if !nativeVerifierOwner(t.VolumeOwner, t.Owner, NativePostgreSQLVerifierInputRole) || !nativeVerifierOwner(p.BackupOwner, t.Owner, NativePostgreSQLArtifactRole) || !nativeVerifierOwner(p.ScratchOwner, t.Owner, NativePostgreSQLVerifierScratchRole) || !nativeVerifierTarget(p.Request.BackupRoot) || !nativeVerifierTarget(p.Request.Policy.ScratchRoot) || nativeTargetsOverlap(p.Request.BackupRoot, p.Request.Policy.ScratchRoot) {
		return false
	}
	backupTarget := p.Request.BackupRoot
	if p.BackupParent {
		backupTarget = path.Dir(backupTarget)
	}
	if !nativeVerifierTarget(backupTarget) || nativeTargetsOverlap(backupTarget, p.Request.Policy.ScratchRoot) {
		return false
	}
	if p.Placeholder != nil {
		q := p.Placeholder
		if !nativeVerifierOwner(q.Owner, t.Owner, NativePostgreSQLVerifierPlaceholderRole) || !nativeVerifierTarget(q.Target) || q.Target == backupTarget || strings.HasPrefix(q.Target, backupTarget+"/") || q.Target == p.Request.BackupRoot || q.Target == p.Request.Policy.ScratchRoot || strings.HasPrefix(q.Target, p.Request.BackupRoot+"/") || strings.HasPrefix(q.Target, p.Request.Policy.ScratchRoot+"/") {
			return false
		}
	}
	return true
}

func nativeVerifierOwner(v, task Ownership, role string) bool {
	return v.valid() && v.ScopeID == task.ScopeID && v.OperationID == task.OperationID && v.Role == role+"-"+task.OperationID
}

func nativeTargetsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}

func nativeVerifierTarget(target string) bool {
	if !wire.PostgreSQLBackupPath(target) || nativeTargetsOverlap(target, DatabaseTaskDestination) {
		return false
	}
	for _, reserved := range []string{"/proc", "/sys", "/dev", "/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt", "/Volumes"} {
		if nativeTargetsOverlap(target, reserved) {
			return false
		}
	}
	return true
}

func (t DatabaseTask) nativeVerifierCommand() []string {
	return []string{NativePostgreSQLBackupVerify, "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLVerifierRequestFile, "--manifest-sha256", t.NativePostgreSQLBackupVerifier.CandidateSHA256}
}

func (t DatabaseTask) nativeVerifierFiles(files []ConfigurationFile) bool {
	if len(files) != 1 || files[0].Name != NativePostgreSQLVerifierRequestFile {
		return false
	}
	var r wire.PostgreSQLBackupRequest
	return wire.StrictJSON(files[0].Contents, &r) == nil && reflect.DeepEqual(r, t.NativePostgreSQLBackupVerifier.Request)
}

func (t DatabaseTask) nativeVerifierExtraMounts() []databaseMountProfile {
	p := t.NativePostgreSQLBackupVerifier
	result := []databaseMountProfile{}
	if p.Placeholder != nil {
		result = append(result, databaseMountProfile{p.Placeholder.Owner, p.Placeholder.Target, true, true})
	}
	target := p.Request.BackupRoot
	if p.BackupParent {
		target = path.Dir(target)
	}
	return append(result,
		databaseMountProfile{p.BackupOwner, target, true, true},
		databaseMountProfile{p.ScratchOwner, p.Request.Policy.ScratchRoot, false, true})
}

func (c *Client) nativeVerifierVolumes(ctx context.Context, t DatabaseTask) error {
	for _, p := range t.nativeVerifierExtraMounts() {
		_, found, e := c.InspectVolume(ctx, p.owner)
		if e != nil {
			return e
		}
		if !found {
			return failure("OFFLINE_ENGINE_NATIVE_VERIFIER_VOLUME_REQUIRED")
		}
	}
	return nil
}

func (c *Client) nativeVerifierConsumers(ctx context.Context, t DatabaseTask, id string) error {
	for _, p := range t.nativeVerifierExtraMounts() {
		if e := c.noForeignConsumers(ctx, p.owner, id); e != nil {
			return e
		}
	}
	return nil
}

func (t DatabaseTask) databaseTaskNetworkMode() string {
	if t.NativePostgreSQLBackupVerifier != nil || t.NativePostgreSQLRestoreRootsInspection != nil {
		return "none"
	}
	return t.NetworkID
}

func (t DatabaseTask) databaseTaskEnvironment(image Image) (map[string]string, bool) {
	env, ok := imageEnvironment(image.Config.Env)
	if ok && t.NativePostgreSQLBackupVerifier != nil {
		env["TMPDIR"] = t.NativePostgreSQLBackupVerifier.Request.Policy.ScratchRoot
	}
	if ok && t.NativePostgreSQLRecovery != nil {
		for key := range env {
			if strings.HasPrefix(key, "PG") {
				return nil, false
			}
		}
		env["TMPDIR"] = t.nativeRecoveryPolicy().ScratchRoot
	}
	if ok && (t.NativePostgreSQLTargetInitialization != nil || t.NativePostgreSQLRestoreRootsInspection != nil) {
		for key := range env {
			if strings.HasPrefix(key, "PG") {
				return nil, false
			}
		}
	}
	return env, ok
}

func nativeVerifierEnvironmentValues(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+env[key])
	}
	return values
}
