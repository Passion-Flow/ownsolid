package inference

import (
	"context"
	"os"
	"path/filepath"
)

func (m *Manager) stop(ctx context.Context, j *journal) error {
	j.Phase = "stopping"
	if e := m.save(*j); e != nil {
		return e
	}
	container, exists, e := m.findContainer(ctx, *j)
	if e != nil {
		return e
	}
	if exists {
		j.ContainerID = container.ID
		if e = m.save(*j); e != nil {
			return e
		}
		if container.State.Running {
			if ctx.Err() != nil {
				return fail("INFERENCE_CANCELLED")
			}
			if e = m.docker.StopContainer(ctx, container.ID); e != nil {
				return e
			}
		}
		container, e = m.ownedContainer(ctx, *j)
		if e != nil {
			return e
		}
		if container.State.Running {
			return fail("INFERENCE_NOT_READY")
		}
	} else {
		j.ContainerID = ""
		j.Verification = nil
	}
	j.Phase = "stopped"
	j.ErrorCode = ""
	return m.save(*j)
}
func (m *Manager) remove(ctx context.Context, j *journal) error {
	j.Phase = "removing"
	if e := m.save(*j); e != nil {
		return e
	}
	container, exists, e := m.findContainer(ctx, *j)
	if e != nil {
		return e
	}
	if exists {
		j.ContainerID = container.ID
		if e = m.save(*j); e != nil {
			return e
		}
		if container.State.Running {
			if ctx.Err() != nil {
				return fail("INFERENCE_CANCELLED")
			}
			if e = m.docker.StopContainer(ctx, container.ID); e != nil {
				return e
			}
		}
		container, e = m.ownedContainer(ctx, *j)
		if e != nil {
			return e
		}
		if container.State.Running {
			return fail("INFERENCE_NOT_READY")
		}
		if ctx.Err() != nil {
			return fail("INFERENCE_CANCELLED")
		}
		if e = m.docker.RemoveContainer(ctx, container.ID); e != nil && e.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
			return e
		}
		if _, stillExists, e := m.findContainer(ctx, *j); e != nil {
			return e
		} else if stillExists {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
	}
	if ctx.Err() != nil {
		return fail("INFERENCE_CANCELLED")
	}
	if e = m.removePrivateFiles(*j); e != nil {
		return e
	}
	if j.NetworkID != "" {
		if e = m.docker.RemoveUnusedNetwork(ctx, j.NodeID, j.NetworkID); e != nil {
			return e
		}
	}
	j.Phase = "removed"
	j.ContainerID = ""
	j.NetworkID = ""
	j.Verification = nil
	j.Stages = []string{}
	j.CachePublicationIntent = false
	j.ErrorCode = ""
	return m.save(*j)
}
func (m *Manager) removePrivateFiles(j journal) error {
	path := m.deploymentPath(j.Command.DeploymentID)
	if _, e := os.Lstat(path); e == nil {
		if e = secureRoot(path, true); e != nil {
			return e
		}
		entries, e := os.ReadDir(path)
		if e != nil {
			return fail("INFERENCE_IO_FAILED")
		}
		if len(entries) != 0 {
			var owner deploymentOwnership
			if protectedJSON(filepath.Join(path, "owner.json"), &owner) != nil || owner != expectedDeploymentOwnership(j) {
				return fail("INFERENCE_OWNERSHIP_CONFLICT")
			}
			for _, entry := range entries {
				if entry.Name() != "owner.json" && entry.Name() != "cache" {
					return fail("INFERENCE_OWNERSHIP_CONFLICT")
				}
			}
			if info, e := os.Lstat(m.cachePath(j.Command.DeploymentID)); e == nil {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return fail("INFERENCE_OWNERSHIP_CONFLICT")
				}
			} else if !os.IsNotExist(e) {
				return fail("INFERENCE_IO_FAILED")
			}
		}
	} else if !os.IsNotExist(e) {
		return fail("INFERENCE_IO_FAILED")
	}
	// Validate every tracked model stage before deleting any partial files.
	for _, stage := range j.Stages {
		if !validStagePath(m.config.RootDirectory, stage) {
			return fail("INFERENCE_STATE_INVALID")
		}
		if _, e := os.Lstat(stage); os.IsNotExist(e) {
			continue
		}
		if e := secureModelDirectory(stage); e != nil {
			return e
		}
		entries, e := os.ReadDir(stage)
		if e != nil {
			return fail("INFERENCE_IO_FAILED")
		}
		for _, entry := range entries {
			if _, ok := j.Command.ModelFile(entry.Name()); !ok {
				return fail("INFERENCE_OWNERSHIP_CONFLICT")
			}
			info, e := os.Lstat(filepath.Join(stage, entry.Name()))
			if e != nil || !info.Mode().IsRegular() || !rootOwned(info) {
				return fail("INFERENCE_OWNERSHIP_CONFLICT")
			}
		}
	}
	root, e := os.OpenRoot(m.config.RootDirectory)
	if e != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	defer root.Close()
	if _, e = os.Lstat(path); e == nil {
		// Only the verified dedicated runtime-cache namespace may contain dynamic engine files.
		if root.RemoveAll(filepath.Join("deployments", j.Command.DeploymentID, "cache")) != nil {
			return fail("INFERENCE_IO_FAILED")
		}
		if e = root.Remove(filepath.Join("deployments", j.Command.DeploymentID, "owner.json")); e != nil && !os.IsNotExist(e) {
			return fail("INFERENCE_IO_FAILED")
		}
		if root.Remove(filepath.Join("deployments", j.Command.DeploymentID)) != nil {
			return fail("INFERENCE_IO_FAILED")
		}
		if e = syncDir(filepath.Join(m.config.RootDirectory, "deployments")); e != nil {
			return e
		}
	}
	for _, stage := range j.Stages {
		if _, e = os.Lstat(stage); os.IsNotExist(e) {
			continue
		}
		relative, e := filepath.Rel(m.config.RootDirectory, stage)
		if e != nil || root.RemoveAll(relative) != nil {
			return fail("INFERENCE_IO_FAILED")
		}
	}
	if _, e = os.Lstat(filepath.Join(m.config.RootDirectory, "models")); e == nil {
		return syncDir(filepath.Join(m.config.RootDirectory, "models"))
	}
	return nil
}
