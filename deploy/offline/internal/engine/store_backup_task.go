package engine

import (
	"context"
	"net/http"
	"net/url"
	"path"
)

const StoreBackupInputRole = "backup-control-config"
const StoreBackupDestination = "/var/lib/projectone/files-backup"
const ModelFilesExport = "export-model-files"
const DatasetFilesExport = "export-dataset-files"
const TrainingFilesExport = "export-training-files"
const PreparationFilesExport = "export-preparation-files"

type storeBackupProfile struct{ role string }

func storeBackupTask(kind string) (storeBackupProfile, bool) {
	roles := map[string]string{ModelFilesExport: "models", DatasetFilesExport: "datasets", TrainingFilesExport: "training", PreparationFilesExport: "preparations"}
	role, ok := roles[kind]
	return storeBackupProfile{role}, ok
}

func StoreBackupTaskContext(kind string) (string, bool) {
	p, ok := storeBackupTask(kind)
	return p.role, ok
}
func (t DatabaseTask) storeBackupArguments() []string {
	if _, ok := storeBackupTask(t.Kind); !ok {
		return nil
	}
	return []string{"--backup-root", StoreBackupDestination + "/private/snapshot", "--reason", t.Reason}
}

type databaseMountProfile struct {
	owner    Ownership
	target   string
	readOnly bool
	private  bool
}

func (t DatabaseTask) databaseMountProfiles() []databaseMountProfile {
	profiles := []databaseMountProfile{{t.VolumeOwner, DatabaseTaskDestination, true, false}}
	if t.LocalizationOwner != nil {
		profiles = append(profiles, databaseMountProfile{*t.LocalizationOwner, LocalizationTaskDestination, true, false})
	}
	if t.OutputOwner != nil {
		target, readOnly := t.outputProfile()
		profiles = append(profiles, databaseMountProfile{*t.OutputOwner, target, readOnly, false})
	}
	if t.SourceOwner != nil {
		target, readonly := "/var/lib/projectone/"+t.SourceOwner.Role, false
		if p, ok := storeRestoreTask(t.Kind); ok && p.importFiles {
			target, readonly = StoreRestoreSourceDestination, true
		}
		profiles = append(profiles, databaseMountProfile{*t.SourceOwner, target, readonly, false})
	}
	if t.NativePostgreSQLBackupVerifier != nil {
		profiles = append(profiles, t.nativeVerifierExtraMounts()...)
	}
	if t.NativePostgreSQLRecovery != nil {
		profiles = append(profiles, t.nativeRecoveryExtraMounts()...)
	}
	if t.NativePostgreSQLTargetInitialization != nil {
		profiles = append(profiles, t.nativeTargetInitializationMounts()...)
	}
	if t.NativePostgreSQLRestoreRootsInspection != nil {
		profiles = append(profiles, t.nativeRestoreRootsMounts()...)
	}
	return profiles
}
func (t DatabaseTask) validDatabaseMounts(container RuntimeContainer) bool {
	wanted := map[string]databaseMountProfile{}
	for _, profile := range t.databaseMountProfiles() {
		if wanted[profile.target].target != "" {
			return false
		}
		wanted[profile.target] = profile
	}
	if len(container.HostConfig.Mounts) != len(wanted) || len(container.Mounts) != len(wanted) {
		return false
	}
	seen := map[string]bool{}
	for _, m := range container.HostConfig.Mounts {
		p, ok := wanted[m.Target]
		subpath := ""
		if p.private {
			subpath = UtilityPrivateDirectory
		}
		if !ok || seen[m.Target] || m.Type != "volume" || m.Source != p.owner.VolumeName() || m.ReadOnly != p.readOnly || m.VolumeOptions == nil || !m.VolumeOptions.NoCopy || m.VolumeOptions.Subpath != subpath {
			return false
		}
		seen[m.Target] = true
	}
	seen = map[string]bool{}
	for _, m := range container.Mounts {
		p, ok := wanted[m.Destination]
		if !ok || seen[m.Destination] || m.Type != "volume" || m.Name != p.owner.VolumeName() || m.Driver != "local" || m.RW == p.readOnly {
			return false
		}
		seen[m.Destination] = true
	}
	return true
}
func (c *Client) inspectEmptyStoreBackupOutput(ctx context.Context, t DatabaseTask, id string) error {
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return e
	}
	if before.State.Status != "created" {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_EXECUTION_PRESERVED")
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {StoreBackupDestination}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return failure("OFFLINE_ENGINE_STORE_BACKUP_EMPTY_OUTPUT_READBACK_FAILED")
	}
	value, e := inspectPrivateArchive(raw, nil, t.UID, t.GID, path.Base(StoreBackupDestination))
	if e != nil || value.Status != "verified_private_volume_payload" {
		return failure("OFFLINE_ENGINE_NONEMPTY_STORE_BACKUP_OUTPUT_PRESERVED")
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return failure("OFFLINE_ENGINE_DATABASE_TASK_CHANGED_DURING_READBACK")
	}
	return nil
}
