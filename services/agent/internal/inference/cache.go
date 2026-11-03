package inference

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

type ModelDownload func(context.Context, wire.ModelFile, string) error

func makeRootDirectory(path string, mode os.FileMode) error {
	if e := os.Mkdir(path, mode); e != nil && !os.IsExist(e) {
		return fail("INFERENCE_IO_FAILED")
	}
	return secureRoot(path, mode == 0700)
}
func (m *Manager) ensureModel(ctx context.Context, j *journal, download ModelDownload) error {
	models := filepath.Join(m.config.RootDirectory, "models")
	if e := makeRootDirectory(models, 0700); e != nil {
		return e
	}
	if _, e := os.Lstat(m.commandModelPath(j.Command)); e == nil {
		if j.CachePublicationIntent {
			if e = verifyCommandDirectory(ctx, j.Command, m.commandModelPath(j.Command)); e != nil {
				return fail("INFERENCE_MODEL_INVALID")
			}
			if e = m.writeCommandCacheMarker(j.Command); e != nil {
				return e
			}
			j.CachePublicationIntent = false
			if e = m.save(*j); e != nil {
				return e
			}
		}
		return m.verifyCommandCache(ctx, j.Command)
	} else if !os.IsNotExist(e) {
		return fail("INFERENCE_IO_FAILED")
	}
	var stage string
	retained := []string{}
	for _, path := range j.Stages {
		if _, e := os.Lstat(path); os.IsNotExist(e) {
			continue
		}
		retained = append(retained, path)
		if stage == "" && secureModelDirectory(path) == nil && verifyCommandDirectory(ctx, j.Command, path) == nil {
			stage = path
		}
	}
	j.Stages = retained
	if stage == "" {
		if len(j.Stages) >= 3 {
			return fail("INFERENCE_RECOVERY_REQUIRED")
		}
		available, e := availableBytes(models)
		if e != nil {
			return e
		}
		if available < uint64(j.Command.ModelBytes()*4+256*1024*1024) {
			return fail("INFERENCE_DISK_SPACE_LOW")
		}
		suffix, e := randomSuffix()
		if e != nil {
			return e
		}
		stage = filepath.Join(models, ".staging-"+j.TaskID+"-"+suffix)
		j.Stages = append(j.Stages, stage)
		j.CachePublicationIntent = false
		if e = m.save(*j); e != nil {
			return e
		}
		if os.Mkdir(stage, 0700) != nil {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if download == nil {
			return fail("INFERENCE_MODEL_INVALID")
		}
		for _, file := range j.Command.ModelFiles() {
			if ctx.Err() != nil {
				return fail("INFERENCE_CANCELLED")
			}
			if e = download(ctx, file, filepath.Join(stage, file.Path)); e != nil {
				if ctx.Err() != nil {
					return fail("INFERENCE_CANCELLED")
				}
				return fail("INFERENCE_MODEL_INVALID")
			}
		}
		if e = verifyCommandDirectory(ctx, j.Command, stage); e != nil {
			if ctx.Err() != nil {
				return fail("INFERENCE_CANCELLED")
			}
			return fail("INFERENCE_MODEL_INVALID")
		}
	}
	if ctx.Err() != nil {
		return fail("INFERENCE_CANCELLED")
	}
	for _, file := range j.Command.ModelFiles() {
		path := filepath.Join(stage, file.Path)
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || !rootOwned(info) {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if os.Chmod(path, 0444) != nil {
			return fail("INFERENCE_IO_FAILED")
		}
	}
	if os.Chmod(stage, 0755) != nil || syncDir(stage) != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	j.CachePublicationIntent = true
	if e := m.save(*j); e != nil {
		return e
	}
	if _, e := os.Lstat(m.commandModelPath(j.Command)); !os.IsNotExist(e) {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	root, e := os.OpenRoot(models)
	if e != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	defer root.Close()
	if modelartifact.PublishDirectory(root, filepath.Base(stage), filepath.Base(m.commandModelPath(j.Command))) != nil || syncDir(models) != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	if e := m.writeCommandCacheMarker(j.Command); e != nil {
		return e
	}
	j.CachePublicationIntent = false
	return m.save(*j)
}

type deploymentOwnership struct {
	ModelKind           string `json:"model_kind,omitempty"`
	ServingRecipeSHA256 string `json:"serving_recipe_sha256,omitempty"`
	SchemaVersion       int    `json:"schema_version"`
	NodeID              string `json:"node_id"`
	DeploymentID        string `json:"deployment_id"`
	ModelID             string `json:"model_id"`
	CatalogDigest       string `json:"catalog_digest"`
}

func expectedDeploymentOwnership(j journal) deploymentOwnership {
	out := deploymentOwnership{SchemaVersion: 1, NodeID: j.NodeID, DeploymentID: j.Command.DeploymentID, ModelID: j.Command.ModelID, CatalogDigest: j.Command.CatalogDigest}
	if j.Command.Derived != nil {
		out.ModelKind = wire.DerivedModelKind
		out.ServingRecipeSHA256 = j.Command.Derived.ServingRecipeSHA256
	}
	return out
}
func (m *Manager) prepareRuntimeCache(j journal) error {
	parent := filepath.Join(m.config.RootDirectory, "deployments")
	if e := makeRootDirectory(parent, 0700); e != nil {
		return e
	}
	path := m.deploymentPath(j.Command.DeploymentID)
	marker := filepath.Join(path, "owner.json")
	if _, e := os.Lstat(path); os.IsNotExist(e) {
		if e = makeRootDirectory(path, 0700); e != nil {
			return e
		}
		body, _ := json.Marshal(expectedDeploymentOwnership(j))
		if e = writeNew(marker, body, 0600); e != nil {
			return e
		}
	} else if e != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	if e := secureRoot(path, true); e != nil {
		return e
	}
	if _, e := os.Lstat(marker); os.IsNotExist(e) && j.Phase == "preparing" && j.ContainerID == "" {
		entries, readError := os.ReadDir(path)
		if readError != nil || len(entries) != 0 {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		body, _ := json.Marshal(expectedDeploymentOwnership(j))
		if e = writeNew(marker, body, 0600); e != nil {
			return e
		}
	}
	var owner deploymentOwnership
	if e := protectedJSON(marker, &owner); e != nil || owner != expectedDeploymentOwnership(j) {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	cache := m.cachePath(j.Command.DeploymentID)
	if _, e := os.Lstat(cache); os.IsNotExist(e) {
		if os.Mkdir(cache, 0700) != nil || os.Chown(cache, 2000, 0) != nil {
			return fail("INFERENCE_IO_FAILED")
		}
	} else if e != nil {
		return fail("INFERENCE_IO_FAILED")
	}
	info, e := os.Lstat(cache)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	if rootOwned(info) && j.Phase == "preparing" && j.ContainerID == "" {
		entries, e := os.ReadDir(cache)
		if e != nil || len(entries) != 0 {
			return fail("INFERENCE_OWNERSHIP_CONFLICT")
		}
		if os.Chown(cache, 2000, 0) != nil {
			return fail("INFERENCE_IO_FAILED")
		}
		info, e = os.Lstat(cache)
		if e != nil {
			return fail("INFERENCE_IO_FAILED")
		}
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || identity.Uid != 2000 || identity.Gid != 0 {
		return fail("INFERENCE_OWNERSHIP_CONFLICT")
	}
	return syncDir(path)
}
