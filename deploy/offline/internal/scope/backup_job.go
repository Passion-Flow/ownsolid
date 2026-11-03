package scope

import (
	"context"
	"os"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func (l *locked) executeBackupJob(ctx context.Context, c backupEngine, p BackupPlan, payloads map[string][]engine.ConfigurationFile, r BackupRecord, job BackupJob, reconcile bool) (BackupRecord, error) {
	id := l.backupTaskID(p, r.Role)
	inspect := func() (engine.RuntimeContainer, bool, error) {
		if job.Store != nil {
			return c.InspectDatabaseTask(ctx, *job.Store)
		}
		return c.InspectPostgresBackupTask(ctx, *job.Postgres)
	}
	switch r.Phase {
	case "task-create":
		if !reconcile {
			var e error
			if job.Store != nil {
				_, e = c.CreateDatabaseTask(ctx, *job.Store)
			} else {
				_, e = c.CreatePostgresBackupTask(ctx, *job.Postgres)
			}
			if e != nil {
				return r, e
			}
		}
		v, found, e := inspect()
		if e != nil || !found || v.State.Status != "created" {
			return r, failure("OFFLINE_SCOPE_BACKUP_TASK_CREATION_UNRESOLVED")
		}
		r.ObjectID = v.ID
		return r, nil
	case "task-execute":
		if !utilityIDPattern.MatchString(id) {
			return r, failure("OFFLINE_SCOPE_BACKUP_TASK_ID_REQUIRED")
		}
		if !reconcile {
			var e error
			if job.Store != nil {
				_, e = c.StartDatabaseTask(ctx, *job.Store, id, payloads["control-input"], l.config.Compose.Process.ReservePercent)
			} else {
				_, e = c.StartPostgresBackupTask(ctx, *job.Postgres, id, payloads["postgres-input"], l.config.Compose.Process.ReservePercent)
			}
			if e != nil {
				return r, e
			}
		}
		deadline := time.NewTimer(time.Duration(p.Request.ObserveSeconds) * time.Second)
		defer deadline.Stop()
		for {
			v, found, e := inspect()
			if e != nil || !found || v.ID != id {
				return r, failure("OFFLINE_SCOPE_BACKUP_TASK_OBSERVATION_FAILED")
			}
			if v.State.Status == "created" {
				return r, failure("OFFLINE_SCOPE_BACKUP_NEVER_STARTED_OUTCOME_UNRESOLVED")
			}
			if v.State.Status == "exited" {
				if v.State.ExitCode != 0 || v.State.OOMKilled {
					return r, failure("OFFLINE_SCOPE_BACKUP_TASK_FAILED")
				}
				return r, nil
			}
			timer := time.NewTimer(time.Duration(p.Request.PollMilliseconds) * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return r, failure("OFFLINE_SCOPE_OPERATION_INTERRUPTED")
			case <-deadline.C:
				timer.Stop()
				return r, failure("OFFLINE_SCOPE_BACKUP_TASK_DEADLINE")
			case <-timer.C:
			}
		}
	case "archive-publish":
		return publishBackupArchive(ctx, c, p, r, job, id, reconcile)
	case "task-remove":
		if !reconcile {
			var e error
			if job.Store != nil {
				e = c.RemoveDatabaseTask(ctx, *job.Store, id)
			} else {
				e = c.RemovePostgresBackupTask(ctx, *job.Postgres, id)
			}
			if e != nil {
				return r, e
			}
		}
		if _, found, e := inspect(); e != nil || found {
			return r, failure("OFFLINE_SCOPE_BACKUP_TASK_REMOVAL_UNRESOLVED")
		}
		return r, nil
	}
	return r, failure("OFFLINE_SCOPE_BACKUP_STEP_INVALID")
}

func publishBackupArchive(ctx context.Context, c backupEngine, p BackupPlan, r BackupRecord, job BackupJob, id string, reconcile bool) (BackupRecord, error) {
	root, e := openBackupDirectory(p.Request.DestinationRoot)
	if e != nil {
		return r, e
	}
	defer root.Close()
	name := r.Role + ".tar"
	metadata := r.Role + ".json"
	if reconcile {
		if e := reconcileBackupPublication(ctx, c, p, root, r.Role, id, job); e != nil {
			return r, e
		}
	}
	if !reconcile {
		if _, e = root.Lstat(name); !os.IsNotExist(e) {
			return r, failure("OFFLINE_SCOPE_BACKUP_ARTIFACT_OCCUPIED")
		}
		var used int64
		for _, file := range backupExpectedFiles(p) {
			if _, e := root.Lstat(file); os.IsNotExist(e) {
				continue
			}
			v, e := backupHashFile(ctx, root, file, p.Request.MaximumBytes-used)
			if e != nil {
				return r, e
			}
			used += v.Bytes
		}
		// Reserve one bounded metadata record in addition to the opaque archive.
		remaining := p.Request.MaximumBytes - used - limitsBackupMetadataBytes
		if remaining < 1<<20 {
			return r, failure("OFFLINE_SCOPE_BACKUP_BYTE_LIMIT")
		}
		entryBudget, e := remainingBackupEntries(ctx, root, p)
		if e != nil {
			return r, e
		}
		if job.Store != nil {
			entryBudget -= postgresBackupArchiveEntries
			if entryBudget < 2 {
				return r, failure("OFFLINE_SCOPE_BACKUP_ENTRY_LIMIT")
			}
		} else if entryBudget < postgresBackupArchiveEntries {
			return r, failure("OFFLINE_SCOPE_BACKUP_ENTRY_LIMIT")
		}
		partial := name + ".partial"
		f, e := root.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return r, failure("OFFLINE_SCOPE_BACKUP_PARTIAL_OCCUPIED")
		}
		var observation any
		var expectedBytes int64
		var expectedSHA string
		if job.Store != nil {
			a, se := c.StreamStoreBackupArchive(ctx, *job.Store, id, remaining, entryBudget, p.Request.StreamSeconds, f)
			observation, expectedBytes, expectedSHA, e = a, a.Bytes, a.SHA256, se
		} else {
			a, se := c.StreamPostgresBackupArchive(ctx, *job.Postgres, id, remaining, p.Request.StreamSeconds, f)
			observation, expectedBytes, expectedSHA, e = a, a.Bytes, a.SHA256, se
		}
		if e != nil {
			f.Close()
			return r, e
		}
		if e = f.Sync(); e != nil {
			f.Close()
			return r, failure("OFFLINE_SCOPE_BACKUP_ARTIFACT_SYNC_FAILED")
		}
		if e = f.Close(); e != nil {
			return r, e
		}
		observed, e := backupHashFile(ctx, root, partial, remaining)
		if e != nil || observed.Bytes != expectedBytes || observed.SHA256 != expectedSHA {
			return r, failure("OFFLINE_SCOPE_BACKUP_ARTIFACT_READBACK_FAILED")
		}
		// Link publication cannot replace a raced existing destination. The
		// temporary second link is removed and directory synced before readback.
		if e = root.Link(partial, name); e != nil {
			return r, failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_UNCERTAIN")
		}
		if e = syncBackupDirectory(root, "."); e != nil {
			return r, e
		}
		if e = root.Remove(partial); e != nil {
			return r, failure("OFFLINE_SCOPE_BACKUP_PUBLICATION_UNCERTAIN")
		}
		if e = syncBackupDirectory(root, "."); e != nil {
			return r, e
		}
		raw := encode(observation)
		if int64(len(raw)) > limitsBackupMetadataBytes {
			return r, failure("OFFLINE_SCOPE_BACKUP_METADATA_LIMIT")
		}
		if e = writeBackupFile(root, metadata, raw); e != nil {
			return r, e
		}
	}
	raw, e := regular(root, metadata, limitsBackupMetadataBytes)
	if e != nil {
		return r, e
	}
	var expectedBytes int64
	var expectedSHA string
	if job.Store != nil {
		var a engine.StoreBackupArchive
		if wire.StrictJSON(raw, &a) != nil || a.Status != "verified_exited_store_snapshot_archive_bytes" || a.Role != r.Role {
			return r, failure("OFFLINE_SCOPE_BACKUP_ARTIFACT_METADATA_INVALID")
		}
		expectedBytes, expectedSHA = a.Bytes, a.SHA256
	} else {
		var a engine.PostgresBackupArchive
		if wire.StrictJSON(raw, &a) != nil || a.Status != "verified_exited_postgres_custom_archive_bytes" {
			return r, failure("OFFLINE_SCOPE_BACKUP_ARTIFACT_METADATA_INVALID")
		}
		expectedBytes, expectedSHA = a.Bytes, a.SHA256
	}
	f, e := backupHashFile(ctx, root, name, p.Request.MaximumBytes)
	if e != nil || f.Bytes != expectedBytes || f.SHA256 != expectedSHA {
		return r, failure("OFFLINE_SCOPE_BACKUP_ARTIFACT_CHANGED")
	}
	// A successful JSON sidecar alone is never publication authority: the exact
	// task is still independently inspected before its later removal phase.
	var value engine.RuntimeContainer
	var found bool
	if job.Store != nil {
		value, found, e = c.InspectDatabaseTask(ctx, *job.Store)
	} else {
		value, found, e = c.InspectPostgresBackupTask(ctx, *job.Postgres)
	}
	if e != nil || !found || value.ID != id || value.State.Status != "exited" || value.State.ExitCode != 0 || value.State.OOMKilled {
		return r, failure("OFFLINE_SCOPE_BACKUP_SUCCESSFUL_TASK_REQUIRED")
	}
	r.ArtifactSHA256, r.ArtifactBytes = f.SHA256, f.Bytes
	return r, nil
}

const limitsBackupMetadataBytes int64 = 1 << 20
