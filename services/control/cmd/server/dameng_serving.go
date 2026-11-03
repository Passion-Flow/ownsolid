package main

import (
	"context"
	"errors"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
)

// The existing constructor owns the business graph. This assertion rejects an
// incomplete native composition before workers/listeners start; module-specific
// Initialize/VerifyStored checks still execute through their existing owners.
func validateServingGraph(selected *selectedDatabase, service *identity.Service, outbox *mail.Outbox, h *identityhttp.Handler) error {
	if selected == nil {
		return errors.New("SELECTED_DATABASE_REQUIRED")
	}
	if selected.native == nil {
		return nil
	}
	bad := errors.New("DAMENG_SERVING_GRAPH_INVALID")
	if selected.postgres != nil || selected.pgLease != nil || selected.mysql != nil || selected.myLease != nil || selected.store == nil || selected.store.Dialect() != transaction.Dameng || service == nil || service.DB != nil || service.Storage != selected.store || service.Config.DatabaseEngine() != config.DamengDatabaseEngine || outbox == nil || outbox.DB != nil || outbox.Storage != selected.store || service.Mail != outbox || h == nil || h.Service != service {
		return bad
	}
	if h.Workspaces == nil || h.Workspaces.Service == nil || h.Nodes == nil || h.Nodes.Service == nil || h.Components == nil || h.Components.Service == nil || h.Models == nil || h.Models.Service == nil || h.Deployments == nil || h.Deployments.Service == nil || h.Distribution == nil || h.Distribution.Service == nil || h.Datasets == nil || h.Datasets.Service == nil || h.Training == nil || h.ModelPreparations == nil || h.Knowledge == nil || h.Knowledge.Service == nil || h.Plugins == nil || h.Plugins.Service == nil || h.Workflows == nil || h.Workflows.Service == nil || h.Applications == nil || h.Applications.Service == nil || h.Notifications == nil || h.Notifications.Service == nil || h.Preferences == nil || h.Preferences.Service == nil || h.SSO == nil || h.SSO.Service == nil || h.AWS == nil || h.AWS.Service == nil || h.Licensing == nil || h.Licensing.Service == nil {
		return bad
	}
	w, n, m := h.Workspaces.Service, h.Nodes.Service, h.Models.Service
	for _, owner := range []*identity.Service{w.Identity, n.Identity, m.Identity, h.Distribution.Service.Identity, h.Datasets.Service.Identity, h.Training.Identity, h.ModelPreparations.Identity, h.Knowledge.Service.Identity, h.Plugins.Service.Identity, h.Workflows.Service.Identity, h.Applications.Service.Identity, h.Notifications.Service.Identity, h.Preferences.Service.Identity, h.SSO.Service.Identity, h.AWS.Service.Identity} {
		if owner != service {
			return bad
		}
	}
	if n.Workspaces != w || m.Workspaces != w || h.Distribution.Service.Workspaces != w || h.Datasets.Service.Workspaces != w || h.Training.Workspaces != w || h.ModelPreparations.Workspaces != w || h.Knowledge.Service.Workspaces != w || h.Plugins.Service.Workspaces != w || h.Workflows.Service.Workspaces != w || h.Applications.Service.Workspaces != w || h.AWS.Service.Workspaces != w {
		return bad
	}
	if w.DB != nil || n.DB != nil || m.DB != nil || h.Distribution.Service.DB != nil || h.Datasets.Service.DB != nil || h.Training.DB != nil || h.ModelPreparations.DB != nil || h.Knowledge.Service.DB != nil || h.Plugins.Service.DB != nil || h.Workflows.Service.DB != nil || h.Applications.Service.DB != nil || h.Notifications.Service.DB != nil || h.Preferences.Service.DB != nil {
		return bad
	}
	if h.Components.Service.Nodes != n || h.Deployments.Service.Nodes != n || h.Deployments.Service.Models != m || n.Components != h.Components.Service || n.Inference != h.Deployments.Service || n.Training != h.Training || n.Preparations != h.ModelPreparations || h.Training.Holdouts != h.ModelPreparations || h.ModelPreparations.Training != h.Training || h.ModelPreparations.Datasets != h.Datasets.Service || h.Workflows.Service.Plugins != h.Plugins.Service || h.Workflows.Service.Knowledge != h.Knowledge.Service || h.Applications.Service.Workflows != h.Workflows.Service || service.BusinessAdmission != h.Licensing.Service {
		return bad
	}
	return nil
}

// Readiness also runs without incoming HTTP probes. A failed audit terminates
// this lifecycle; recovery always requires a fresh, validated explicit startup.
func watchNativeReadiness(ctx context.Context, selected *selectedDatabase, c config.Config) error {
	if selected == nil || selected.native == nil || c.Database.Dameng == nil || c.Database.Dameng.Validate() != nil {
		return errors.New("DAMENG_SERVING_CONFIGURATION_REQUIRED")
	}
	interval := min(c.Database.Dameng.ReadinessSeconds, c.Policy().HTTP.InstallationLeaseIntervalSeconds)
	return auditInstallationLease(ctx, config.Seconds(interval), config.Seconds(c.Policy().HTTP.RequestSeconds), selected.Verify)
}
