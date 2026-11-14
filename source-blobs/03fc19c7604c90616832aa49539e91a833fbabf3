package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	platform "runtime"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const (
	RegistryImportOffline    = "registry-import-offline"
	RegistryReconcileOffline = "registry-reconcile-offline"
	RegistryReconcileTask    = "registry-reconcile-task"
)

type RegistryOptions struct {
	Operation, TaskID, ManifestFile, ArchiveFile string
}

func (o RegistryOptions) Validate() error {
	absolute := func(value string) bool { return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value }
	valid := false
	switch o.Operation {
	case RegistryImportOffline:
		valid = o.TaskID == "" && absolute(o.ManifestFile) && absolute(o.ArchiveFile)
	case RegistryReconcileOffline:
		valid = o.TaskID == "" && absolute(o.ManifestFile) && o.ArchiveFile == ""
	case RegistryReconcileTask:
		valid = idPattern.MatchString(o.TaskID) && o.ManifestFile == "" && o.ArchiveFile == ""
	}
	if !valid {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	return nil
}

type registryOperator interface {
	ImportOffline(context.Context, wire.RegistryOfflineManifest, string) error
	ReconcileOffline(context.Context, wire.RegistryOfflineManifest) error
	ReconcileStoredTask(context.Context, string) error
}

func executeRegistryOperation(ctx context.Context, o RegistryOptions, m wire.RegistryOfflineManifest, loader registryOperator) error {
	if o.Validate() != nil || loader == nil {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	if ctx.Err() != nil {
		return errors.New("INFERENCE_CANCELLED")
	}
	if o.Operation != RegistryReconcileTask && m.Validate() != nil {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	switch o.Operation {
	case RegistryImportOffline:
		return loader.ImportOffline(ctx, m, o.ArchiveFile)
	case RegistryReconcileOffline:
		return loader.ReconcileOffline(ctx, m)
	case RegistryReconcileTask:
		return loader.ReconcileStoredTask(ctx, o.TaskID)
	}
	return errors.New("AGENT_ARGUMENT_INVALID")
}

var registryPublicCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

// RunRegistryOperation shares the ordinary agent's exclusive lock and platform/
// socket restrictions. Offline image facts never grant task execution authority.
func RunRegistryOperation(ctx context.Context, c Config, o RegistryOptions) (result error) {
	defer func() {
		if result != nil && !registryPublicCode.MatchString(result.Error()) {
			result = errors.New("REGISTRY_OPERATION_FAILED")
		}
	}()
	if o.Validate() != nil {
		return errors.New("AGENT_ARGUMENT_INVALID")
	}
	if c.ImageRegistry == nil || validateRegistryConfiguration(c) != nil {
		return errors.New("AGENT_REGISTRY_CONFIGURATION_INVALID")
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(c.ImageRegistry.OperationSeconds)*time.Second)
	defer cancel()
	if bounded.Err() != nil {
		return errors.New("INFERENCE_CANCELLED")
	}
	lock, e := state.Lock(c.StateDirectory)
	if e != nil {
		return e
	}
	defer lock.Close()
	var m wire.RegistryOfflineManifest
	if o.Operation != RegistryReconcileTask {
		m, e = imageregistry.ReadOfflineManifest(*c.ImageRegistry, o.ManifestFile)
		if e != nil {
			return e
		}
	}
	if platform.GOOS != "linux" || platform.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("INFERENCE_PLATFORM_UNSUPPORTED")
	}
	if e = inference.ProtectedDockerSocket(c.Inference.DockerSocket); e != nil {
		return e
	}
	docker, e := inference.NewDocker(c.Inference.DockerSocket, c.Policy())
	if e != nil {
		return e
	}
	defer docker.Close()
	loader, e := imageregistry.New(*c.ImageRegistry, nil, docker)
	if e != nil {
		return e
	}
	return executeRegistryOperation(bounded, o, m, loader)
}
