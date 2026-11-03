package engine

import (
	"context"
	"net/http"
	"net/url"
)

// Only per-attempt disposable backup volumes can pass this removal boundary.
// Source data and installation configuration roles never match this catalog.
func (c *Client) RemoveBackupVolume(ctx context.Context, o Ownership) error {
	return c.removeGeneratedVolume(ctx, o, []string{StoreBackupInputRole, PostgresBackupInputRole, PostgresBackupOutputRole, "backup-models", "backup-datasets", "backup-training", "backup-preparations"})
}

func (c *Client) RemovePostgresRestoreVolume(ctx context.Context, o Ownership) error {
	return c.removeGeneratedVolume(ctx, o, []string{PostgresRestoreInputRole, PostgresRestoreOutputRole, PostgresRestoreSourceRole})
}

func (c *Client) RemoveStoreRestoreVolume(ctx context.Context, o Ownership) error {
	return c.removeGeneratedVolume(ctx, o, []string{StoreRestoreConfigRole, StoreRestoreArchiveRole + "-models", StoreRestoreArchiveRole + "-datasets", StoreRestoreArchiveRole + "-training", StoreRestoreArchiveRole + "-preparations"})
}

func (c *Client) removeGeneratedVolume(ctx context.Context, o Ownership, prefixes []string) error {
	valid := false
	for _, prefix := range prefixes {
		if o.Role == prefix+"-"+o.OperationID {
			valid = true
		}
	}
	if !o.valid() || !valid {
		return failure("OFFLINE_ENGINE_BACKUP_VOLUME_CONTEXT_INVALID")
	}
	if _, found, e := c.InspectVolume(ctx, o); e != nil {
		return e
	} else if !found {
		return failure("OFFLINE_ENGINE_BACKUP_VOLUME_REQUIRED")
	}
	if e := c.VerifyVolumeConsumers(ctx, o, nil); e != nil {
		return e
	}
	_, status, e := c.request(ctx, http.MethodDelete, "/v"+c.policy.APIVersion+"/volumes/"+url.PathEscape(o.VolumeName()), nil, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusNoContent {
		return failure("OFFLINE_ENGINE_BACKUP_VOLUME_REMOVE_UNCERTAIN")
	}
	if _, found, e := c.InspectVolume(ctx, o); e != nil || found {
		return failure("OFFLINE_ENGINE_BACKUP_VOLUME_REMOVE_UNCERTAIN")
	}
	return nil
}
