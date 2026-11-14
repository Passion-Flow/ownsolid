package main

import (
	"context"
	"errors"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/taskdeliveries"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
)

func openTaskDeliveries(ctx context.Context, c config.Config, store transaction.Store, h *identityhttp.Handler) (*taskdeliveries.Service, error) {
	if e := c.TaskEvents.Validate(); e != nil {
		return nil, e
	}
	if !c.TaskEvents.Enabled {
		return nil, nil
	}
	if ctx == nil {
		return nil, errors.New("TASK_BUS_COMPOSITION_INVALID")
	}
	if store == nil || store.Dialect() != transaction.PostgreSQL {
		return nil, errors.New("TASK_BUS_SCHEMA_REJECTED")
	}
	if h == nil || h.InitializationError() != nil || h.Service == nil || h.Workspaces == nil || h.Nodes == nil || h.Nodes.Service == nil || h.Nodes.Service.TaskEvents != nil || h.TaskDeliveries != nil {
		return nil, errors.New("TASK_BUS_COMPOSITION_INVALID")
	}
	tenant, e := installedTenant(ctx, store)
	if e != nil {
		return nil, e
	}
	bus, e := taskbus.NewJetStream(ctx, c.TaskEvents, tenant)
	if e != nil {
		return nil, e
	}
	s, e := taskdeliveries.NewService(ctx, h.Service, h.Workspaces.Service, h.Nodes.Service, c.TaskEvents, bus)
	if e != nil {
		return nil, errors.Join(e, bus.Close())
	}
	h.Nodes.Service.TaskEvents = s
	return s, nil
}

func closeTaskDeliveries(ctx context.Context, s *taskdeliveries.Service) error {
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Configuration.ShutdownSeconds))
	defer cancel()
	return s.Close(bounded)
}
