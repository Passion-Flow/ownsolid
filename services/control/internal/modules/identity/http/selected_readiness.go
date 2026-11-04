package http

import (
	"context"
	"errors"
)

func (h *Handler) InitializationError() error {
	if h == nil {
		return errors.New("CONTROL_COMPOSITION_REQUIRED")
	}
	return h.initializationError
}

// VerifyStored composes actual owning checks. The server calls it after opening
// stores/processors and starting owners with tracked lifecycle prerequisites.
func (h *Handler) VerifyStored(ctx context.Context) error {
	if h == nil || h.initializationError != nil || h.Service == nil || h.Licensing == nil || h.Licensing.Service == nil ||
		h.Preferences == nil || h.Preferences.Service == nil || h.Notifications == nil || h.Notifications.Service == nil ||
		h.Models == nil || h.Models.Service == nil || h.Datasets == nil || h.Datasets.Service == nil || h.Training == nil ||
		h.ModelPreparations == nil || h.Knowledge == nil || h.Knowledge.Service == nil || h.Plugins == nil || h.Plugins.Service == nil ||
		h.Workflows == nil || h.Workflows.Service == nil {
		return errors.New("SERVICE_NOT_READY")
	}
	if h.RuntimeReadiness != nil {
		if e := h.RuntimeReadiness(); e != nil {
			return errors.New("SERVICE_NOT_READY")
		}
	}
	for _, check := range []func(context.Context) error{
		h.Licensing.Service.VerifyStored, h.Preferences.Service.VerifyStored, h.Notifications.Service.VerifyStored,
		h.Models.Service.VerifyStoredVersions, h.Datasets.Service.VerifyStored, h.Training.VerifyStored,
		h.ModelPreparations.VerifyStored, h.Knowledge.Service.VerifyStored, h.Plugins.Service.VerifyStored, h.Workflows.Service.VerifyStored,
	} {
		if e := check(ctx); e != nil {
			return errors.New("SERVICE_NOT_READY")
		}
	}
	if c := h.Service.Config.ObjectStorage; c != nil && c.Enabled {
		if h.ArtifactStores == nil {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.ArtifactStores.VerifyStored(ctx); e != nil {
			return errors.New("SERVICE_NOT_READY")
		}
	}
	if h.Service.Config.ImageRegistry != nil {
		if h.ImageRegistry == nil {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.ImageRegistry.VerifyControlState(ctx); e != nil {
			return errors.New("SERVICE_NOT_READY")
		}
	}
	if h.Service.Config.ReleaseViewConfigFile != "" {
		if h.Releases == nil || h.Releases.Service == nil || h.Releases.Service.VerifyStored(ctx) != nil {
			return errors.New("SERVICE_NOT_READY")
		}
	} else if h.Releases != nil {
		return errors.New("SERVICE_NOT_READY")
	}
	if h.Service.Config.MiddlewareConfigFile != "" {
		if h.Middleware == nil || h.Middleware.Service == nil || h.Middleware.Service.Identity != h.Service || h.Workspaces == nil || h.Middleware.Service.Workspaces != h.Workspaces.Service {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Middleware.Service.VerifyStored(ctx); e != nil {
			return errors.New("SERVICE_NOT_READY")
		}
	} else if h.Middleware != nil {
		return errors.New("SERVICE_NOT_READY")
	}
	if h.Service.Config.PrivateClusterConfigFile != "" {
		if h.PrivateClusters == nil || h.PrivateClusters.Service == nil || h.Workspaces == nil ||
			!h.PrivateClusters.Service.UsesOwners(h.Service, h.Workspaces.Service, h.Licensing.Service) {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.PrivateClusters.Service.VerifyStored(ctx); e != nil {
			return errors.New("SERVICE_NOT_READY")
		}
	} else if h.PrivateClusters != nil {
		return errors.New("SERVICE_NOT_READY")
	}
	if h.Service.Config.TaskEvents.Enabled {
		if h.TaskDeliveries == nil || h.Nodes == nil || h.Nodes.Service == nil || h.Nodes.Service.TaskEvents != h.TaskDeliveries {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.TaskDeliveries.VerifyStored(ctx); e != nil {
			return errors.New("SERVICE_NOT_READY")
		}
	} else if h.TaskDeliveries != nil {
		return errors.New("SERVICE_NOT_READY")
	}
	return nil
}
