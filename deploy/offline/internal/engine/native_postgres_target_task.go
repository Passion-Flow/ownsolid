package engine

import (
	"context"
	"path"
	"reflect"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const NativePostgreSQLTargetInputRole = "native-pg-target-config"
const NativePostgreSQLTargetRecordsRole = "native-pg-target-records"
const NativePostgreSQLTargetRequestFile = "native-target-request.json"

// The original native owner creates only its once-bound fresh target marker.
// The Engine transports the original typed init/inspect request and never
// writes SQL, infers pins, changes a request/policy or prepares runtime roles.
type NativePostgreSQLTargetInitializationProfile struct {
	Request                 wire.PostgreSQLRestoreTargetInitializationRequest `json:"request"`
	RecordsOwner            Ownership                                         `json:"records_owner"`
	OperatorEnvironmentFile string                                            `json:"operator_environment_file,omitempty"`
}

func (t DatabaseTask) validNativePostgreSQLTargetInitialization() bool {
	p := t.NativePostgreSQLTargetInitialization
	if p == nil || t.NativePostgreSQLRestoreRootsInspection != nil || t.NativePostgreSQLRecovery != nil || t.NativePostgreSQLBackupVerifier != nil || t.LocalizationOwner != nil || t.OutputOwner != nil || t.SourceOwner != nil || t.StoreRestore != nil || t.Reason != "" || !utilityIdentity(t.Owner, t.Image, t.UID, t.GID) || t.Owner.Role != "database-"+t.Kind || !nativeVerifierOwner(t.VolumeOwner, t.Owner, NativePostgreSQLTargetInputRole) || !nativeVerifierOwner(p.RecordsOwner, t.Owner, NativePostgreSQLTargetRecordsRole) || !containerIdentity.MatchString(t.NetworkID) || len(t.Peers) < 1 || len(t.Peers) > 3 || t.Peers["postgres"] == "" {
		return false
	}
	r := p.Request
	if t.Owner.ScopeID != r.TargetScopeID || t.Owner.OperationID != r.InitializationID || t.NetworkID != r.Binding.NetworkID || t.Peers["postgres"] != r.Binding.PostgresContainerID || !nativeVerifierTarget(path.Dir(r.InitializationRoot)) {
		return false
	}
	switch t.Kind {
	case wire.PostgreSQLRestoreTargetInitializeCommand:
		if !r.Valid() {
			return false
		}
	case wire.PostgreSQLRestoreTargetInspectCommand:
		if !r.ValidForInspection() {
			return false
		}
	default:
		return false
	}
	if p.OperatorEnvironmentFile != "" && p.OperatorEnvironmentFile != DatabaseTaskDestination+"/private/operator.env" {
		return false
	}
	seen := map[string]bool{}
	for role, id := range t.Peers {
		if !runtimeService(role) || !containerIdentity.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func ValidateNativePostgreSQLTargetInitializationTask(t DatabaseTask) error {
	if !t.validNativePostgreSQLTargetInitialization() {
		return failure("OFFLINE_ENGINE_NATIVE_TARGET_TASK_INVALID")
	}
	return nil
}

func (t DatabaseTask) nativeTargetInitializationCommand() []string {
	args := []string{t.Kind, "--config", DatabaseTaskDestination + "/private/control.yaml", "--request", DatabaseTaskDestination + "/private/" + NativePostgreSQLTargetRequestFile}
	if f := t.NativePostgreSQLTargetInitialization.OperatorEnvironmentFile; f != "" {
		args = append(args, "--env-file", f)
	}
	return args
}
func (t DatabaseTask) nativeTargetInitializationMounts() []databaseMountProfile {
	p := t.NativePostgreSQLTargetInitialization
	return []databaseMountProfile{{p.RecordsOwner, path.Dir(p.Request.InitializationRoot), t.Kind == wire.PostgreSQLRestoreTargetInspectCommand, true}}
}
func (t DatabaseTask) nativeTargetInitializationFiles(files []ConfigurationFile) bool {
	var configuration, request, environment bool
	for _, f := range files {
		switch f.Name {
		case "control.yaml":
			configuration = true
		case "operator.env":
			environment = true
		case NativePostgreSQLTargetRequestFile:
			var r wire.PostgreSQLRestoreTargetInitializationRequest
			request = wire.StrictJSON(f.Contents, &r) == nil && reflect.DeepEqual(r, t.NativePostgreSQLTargetInitialization.Request)
		}
	}
	return configuration && request && environment == (t.NativePostgreSQLTargetInitialization.OperatorEnvironmentFile != "")
}
func (c *Client) nativeTargetInitializationVolumes(ctx context.Context, t DatabaseTask) error {
	if _, found, e := c.InspectVolume(ctx, t.NativePostgreSQLTargetInitialization.RecordsOwner); e != nil {
		return e
	} else if !found {
		return failure("OFFLINE_ENGINE_NATIVE_TARGET_RECORD_VOLUME_REQUIRED")
	}
	return nil
}
func (c *Client) nativeTargetInitializationConsumers(ctx context.Context, t DatabaseTask, id string) error {
	return c.noForeignConsumers(ctx, t.NativePostgreSQLTargetInitialization.RecordsOwner, id)
}
func (t DatabaseTask) freezeNativeTargetInitialization() DatabaseTask {
	if t.NativePostgreSQLTargetInitialization != nil {
		p := *t.NativePostgreSQLTargetInitialization
		t.NativePostgreSQLTargetInitialization = &p
		if t.Peers != nil {
			peers := map[string]string{}
			for k, v := range t.Peers {
				peers[k] = v
			}
			t.Peers = peers
		}
	}
	return t
}
