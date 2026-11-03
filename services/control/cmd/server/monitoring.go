package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
)

// Wrap the selected serving chains, including their installation-lease fences.
// Replacing either chain with the raw application would bypass request admission.
func monitoringServingHandlers(recorder *monitoringmetrics.Recorder, serving, machine http.Handler) (http.Handler, http.Handler) {
	serving = recorder.Middleware(serving)
	if machine != nil {
		machine = recorder.Middleware(machine)
	}
	return serving, machine
}

func openMonitoring(ctx context.Context, c config.Config, selected *selectedDatabase, h *identityhttp.Handler, logger *slog.Logger) (*monitoringhttp.Handler, *monitoringmetrics.Recorder, error) {
	if e := c.ValidateMonitoringReference(); e != nil {
		return nil, nil, e
	}
	if c.MonitoringConfigFile == "" {
		return nil, nil, nil
	}
	bad := errors.New("MONITORING_OWNER_REQUIRED")
	if ctx == nil || selected == nil || h == nil || h.InitializationError() != nil || h.Service == nil ||
		h.Workspaces == nil || h.Licensing == nil || h.Components == nil || h.Distribution == nil || h.Monitoring != nil {
		return nil, nil, bad
	}
	store, e := selectedIdentityStore(selected, h.Service)
	if e != nil || store.Dialect() != transaction.PostgreSQL && store.Dialect() != transaction.MySQL {
		return nil, nil, bad
	}
	if _, e = installedTenant(ctx, store); e != nil {
		return nil, nil, e
	}
	policy, e := monitoring.LoadEnvironment(map[string]string{monitoring.ConfigEnvironment: c.MonitoringConfigFile})
	if e != nil {
		return nil, nil, e
	}
	recorder, e := monitoringmetrics.New(policy.HTTP, logger)
	if e != nil {
		return nil, nil, e
	}
	service, e := monitoring.New(h.Service, h.Workspaces.Service, h.Licensing.Service, h.Components.Service, h.Distribution.Service, recorder, policy)
	if e != nil {
		return nil, nil, e
	}
	return &monitoringhttp.Handler{Service: service}, recorder, nil
}
