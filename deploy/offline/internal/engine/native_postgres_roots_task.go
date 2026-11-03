package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const NativePostgreSQLRestoreRootsInputRole = "native-pg-roots-config"
const NativePostgreSQLRestoreRootsRequestFile = "native-roots-request.json"

// The original producer reads only the frozen typed runtime and four existing
// private stores. Binding peers remain current metadata fences, not networking
// authority: this actor uses network none and never mounts SQL/record/CAS inputs.
type NativePostgreSQLRestoreRootsInspectionProfile struct {
	Request                  wire.PostgreSQLRestoreRootsInspectionRequest `json:"request"`
	ConfigurationOwner       Ownership                                    `json:"configuration_owner"`
	RuntimeConfigurationFile string                                       `json:"runtime_configuration_file"`
	RuntimeEnvironmentFile   string                                       `json:"runtime_environment_file,omitempty"`
	Stores                   [4]NativePostgreSQLStoreMount                `json:"stores"`
}

func (p *NativePostgreSQLRestoreRootsInspectionProfile) UnmarshalJSON(raw []byte) error {
	type plain NativePostgreSQLRestoreRootsInspectionProfile
	var value plain
	if p == nil || wire.StrictJSON(raw, &value) != nil || !exactNativeRecoveryJSONShape(raw, reflect.TypeOf(value)) {
		return failure("OFFLINE_ENGINE_NATIVE_ROOTS_PROFILE_JSON_INVALID")
	}
	var fields map[string]json.RawMessage
	var stores []json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(fields["stores"], &stores) != nil || len(stores) != 4 {
		return failure("OFFLINE_ENGINE_NATIVE_ROOTS_PROFILE_JSON_INVALID")
	}
	*p = NativePostgreSQLRestoreRootsInspectionProfile(value)
	return nil
}

func (t DatabaseTask) validNativePostgreSQLRestoreRootsInspection() bool {
	p := t.NativePostgreSQLRestoreRootsInspection
	if p == nil || t.NativePostgreSQLRecovery != nil || t.NativePostgreSQLBackupVerifier != nil || t.NativePostgreSQLTargetInitialization != nil || t.LocalizationOwner != nil || t.OutputOwner != nil || t.SourceOwner != nil || t.StoreRestore != nil || t.Reason != "" || t.Kind != wire.PostgreSQLRestoreRootsInspectCommand || !utilityIdentity(t.Owner, t.Image, t.UID, t.GID) || t.Owner.Role != "database-"+t.Kind || !nativeVerifierOwner(t.VolumeOwner, t.Owner, NativePostgreSQLRestoreRootsInputRole) || !p.Request.Valid() {
		return false
	}
	r := p.Request
	if t.Owner.ScopeID != r.TargetScopeID || t.Owner.OperationID != r.InspectionID || uint32(t.UID) != r.ExpectedUID || uint32(t.GID) != r.ExpectedGID || t.NetworkID != r.Binding.NetworkID || t.Peers["postgres"] != r.Binding.PostgresContainerID || !containerIdentity.MatchString(t.NetworkID) || len(t.Peers) < 1 || len(t.Peers) > 3 || !nativeVerifierTarget(r.RuntimeConfigurationRoot) {
		return false
	}
	projectionTask := Ownership{ScopeID: t.Owner.ScopeID, OperationID: r.Binding.RestoreAttemptID}
	if !nativeVerifierOwner(p.ConfigurationOwner, projectionTask, NativePostgreSQLConfigurationRole) || !wire.PostgreSQLBackupPath(p.RuntimeConfigurationFile) || !strings.HasPrefix(p.RuntimeConfigurationFile, r.RuntimeConfigurationRoot+"/") || (p.RuntimeEnvironmentFile == "") != (r.RuntimeEnvironmentSHA256 == "") || p.RuntimeEnvironmentFile != "" && (!wire.PostgreSQLBackupPath(p.RuntimeEnvironmentFile) || !strings.HasPrefix(p.RuntimeEnvironmentFile, r.RuntimeConfigurationRoot+"/") || p.RuntimeEnvironmentFile == p.RuntimeConfigurationFile) {
		return false
	}
	seenPeers := map[string]bool{}
	for role, id := range t.Peers {
		if !runtimeService(role) || !containerIdentity.MatchString(id) || seenPeers[id] {
			return false
		}
		seenPeers[id] = true
	}
	roles := []string{"models", "datasets", "training", "preparations"}
	targets := []string{DatabaseTaskDestination, r.RuntimeConfigurationRoot}
	volumes := map[string]bool{t.VolumeOwner.VolumeName(): true, p.ConfigurationOwner.VolumeName(): true}
	for i, m := range p.Stores {
		if !m.Owner.valid() || m.Owner.ScopeID != t.Owner.ScopeID || m.Owner.Role != roles[i] || !nativeVerifierTarget(m.Target) || volumes[m.Owner.VolumeName()] {
			return false
		}
		for _, target := range targets {
			if nativeTargetsOverlap(target, m.Target) {
				return false
			}
		}
		targets = append(targets, m.Target)
		volumes[m.Owner.VolumeName()] = true
	}
	return true
}

func ValidateNativePostgreSQLRestoreRootsInspectionTask(t DatabaseTask) error {
	if !t.validNativePostgreSQLRestoreRootsInspection() {
		return failure("OFFLINE_ENGINE_NATIVE_ROOTS_TASK_INVALID")
	}
	return nil
}
func (t DatabaseTask) nativeRestoreRootsCommand() []string {
	p := t.NativePostgreSQLRestoreRootsInspection
	args := []string{wire.PostgreSQLRestoreRootsInspectCommand, "--config", p.RuntimeConfigurationFile, "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLRestoreRootsRequestFile}
	if p.RuntimeEnvironmentFile != "" {
		args = append(args, "--env-file", p.RuntimeEnvironmentFile)
	}
	return args
}
func (t DatabaseTask) nativeRestoreRootsFiles(files []ConfigurationFile) bool {
	if len(files) != 1 || files[0].Name != NativePostgreSQLRestoreRootsRequestFile {
		return false
	}
	var r wire.PostgreSQLRestoreRootsInspectionRequest
	return wire.StrictJSON(files[0].Contents, &r) == nil && reflect.DeepEqual(r, t.NativePostgreSQLRestoreRootsInspection.Request)
}
func (t DatabaseTask) nativeRestoreRootsMounts() []databaseMountProfile {
	p := t.NativePostgreSQLRestoreRootsInspection
	out := []databaseMountProfile{{p.ConfigurationOwner, p.Request.RuntimeConfigurationRoot, true, true}}
	for _, m := range p.Stores {
		out = append(out, databaseMountProfile{m.Owner, m.Target, true, true})
	}
	return out
}
func (c *Client) nativeRestoreRootsVolumes(ctx context.Context, t DatabaseTask) error {
	for _, m := range t.nativeRestoreRootsMounts() {
		if _, found, e := c.InspectVolume(ctx, m.owner); e != nil {
			return e
		} else if !found {
			return failure("OFFLINE_ENGINE_NATIVE_ROOTS_VOLUME_REQUIRED")
		}
	}
	return nil
}
func (c *Client) nativeRestoreRootsConsumers(ctx context.Context, t DatabaseTask, id string) error {
	for _, m := range t.nativeRestoreRootsMounts() {
		allowed := []string{}
		if id != "" {
			allowed = append(allowed, id)
		}
		if m.owner.Role != "" && (m.owner.Role == "models" || m.owner.Role == "datasets" || m.owner.Role == "training" || m.owner.Role == "preparations") && t.Peers["control"] != "" {
			allowed = append(allowed, t.Peers["control"])
		}
		if e := c.VerifyVolumeConsumers(ctx, m.owner, allowed); e != nil {
			return e
		}
	}
	return nil
}
func (t DatabaseTask) freezeNativeRestoreRoots() DatabaseTask {
	if t.NativePostgreSQLRestoreRootsInspection != nil {
		p := *t.NativePostgreSQLRestoreRootsInspection
		t.NativePostgreSQLRestoreRootsInspection = &p
		peers := map[string]string{}
		for k, v := range t.Peers {
			peers[k] = v
		}
		t.Peers = peers
	}
	return t
}
