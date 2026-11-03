package http

import (
	"context"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	applicationhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	componenthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/components/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	datasethttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	knowledgehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	licensehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications"
	notificationhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	pluginhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	preferencehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	ssohttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	workflowhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	workspacehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"strings"
)

func newAccount(s *identity.Service) *Handler {
	license, e := licensing.FromConfig(s)
	locales, localeError := s.LocaleRegistry()
	s.BusinessAdmission = license
	h := &Handler{Service: s, Licensing: &licensehttp.Handler{Service: license}, Locales: locales, licenseInitializationError: e, initializationError: errors.Join(e, localeError), active: make(chan struct{}, s.Config.Policy().HTTP.MaxActiveRequests)}
	if s.DB != nil {
		h.SchemaReadiness = func(ctx context.Context) error { return database.Check(ctx, s.DB, s.Config.Server.Profile()) }
	}
	if s.Config.WorkspaceRuntime() {
		h.Workspaces = &workspacehttp.Handler{Service: &workspaces.Service{DB: s.DB, Identity: s}}
	}
	if s.Config.NodeRuntime() {
		h.Nodes = &nodehttp.Handler{Service: &nodes.Service{DB: s.DB, Identity: s, Workspaces: h.Workspaces.Service}}
	}
	if s.Config.ComponentRuntime() {
		c := &components.Service{Nodes: h.Nodes.Service, ArchiveFile: s.Config.Components.NodeExporterArchiveFile, CatalogDirectory: s.Config.Components.NodeExporterCatalogDirectory}
		h.Components = &componenthttp.Handler{Service: c}
		h.Nodes.Service.Components = c
		h.Nodes.OpenArtifact = c.OpenArtifact
		h.Nodes.Artifact = c.TaskArtifact
		h.Nodes.ComponentArtifact = c.DownloadArtifact
	}
	if s.Config.ModelsRuntime() {
		h.composeModels()
	}
	if s.Config.GatewayRuntime() {
		h.composeGateway()
	}
	if s.Config.DatasetRuntime() {
		h.Datasets = &datasethttp.Handler{Service: &datasets.Service{DB: s.DB, Identity: s, Workspaces: h.Workspaces.Service}}
	}
	if s.Config.TrainingRuntime() {
		h.composeTraining()
	}
	if s.Config.LifecycleRuntime() {
		h.composeLifecycle()
	}
	if s.Config.KnowledgeRuntime() {
		h.Knowledge = &knowledgehttp.Handler{Service: &knowledge.Service{DB: s.DB, Identity: s, Workspaces: h.Workspaces.Service, Distribution: h.Distribution.Service}}
	}
	if s.Config.PluginsRuntime() {
		h.Plugins = &pluginhttp.Handler{Service: &plugins.Service{DB: s.DB, Identity: s, Workspaces: h.Workspaces.Service, Models: h.Models.Service}}
	}
	if s.Config.WorkflowsRuntime() {
		h.Workflows = &workflowhttp.Handler{Service: &workflows.Service{DB: s.DB, Identity: s, Workspaces: h.Workspaces.Service, Knowledge: h.Knowledge.Service, Distribution: h.Distribution.Service}}
		if s.Config.PluginsRuntime() {
			h.Workflows.Service.Plugins = h.Plugins.Service
		}
	}
	if s.Config.PortalRuntime() {
		h.Applications = &applicationhttp.Handler{Service: &applications.Service{DB: s.DB, Identity: s, Workspaces: h.Workspaces.Service, Workflows: h.Workflows.Service}}
		h.Notifications = &notificationhttp.Handler{Service: &notifications.Service{DB: s.DB, Identity: s}}
		h.Preferences = &preferencehttp.Handler{Service: &preferences.Service{DB: s.DB, Identity: s}}
	}
	if s.Config.SSORuntime() {
		service, ok := s.Sources.(*sso.Service)
		var err error
		if !ok || service.Identity != s {
			service, err = sso.New(s)
		}
		h.initializationError = errors.Join(h.initializationError, err)
		if err == nil {
			s.Sources = service
			h.SSO = &ssohttp.Handler{Service: service}
		}
	}
	return h
}

// workspaceAPI identifies the namespace, not the capabilities available below it.
// Minimal composition must delegate it exclusively to the Workspace handler.
func workspaceAPI(path string) bool {
	for _, root := range []string{"/api/v1/workspaces", "/api/v1/admin/workspaces"} {
		if path == root {
			return true
		}
		if !strings.HasPrefix(path, root+"/") {
			continue
		}
		p := strings.Split(strings.TrimPrefix(path, root+"/"), "/")
		if len(p) == 1 {
			return true
		}
		if len(p) == 2 {
			return p[0] == "operations" || p[1] == "members" || p[1] == "notes"
		}
		if len(p) == 3 {
			return p[1] == "members" || p[1] == "notes"
		}
		return len(p) == 4 && p[1] == "notes" && p[3] == "export"
	}
	return false
}
func accountAPI(path string) bool {
	for _, auth := range []string{"csrf", "login", "logout", "register", "invitation-validations", "password-reset-requests", "password-resets"} {
		if path == "/api/v1/auth/"+auth {
			return true
		}
	}
	if path == "/api/v1/me" || path == "/api/v1/license/status" || strings.HasPrefix(path, "/api/v1/admin/license") {
		return true
	}
	for _, root := range []string{"/api/v1/admin/users", "/api/v1/admin/invitations", "/api/v1/admin/audit-events", "/api/v1/admin/mail-deliveries"} {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}
func (h *Handler) verifyAccount(ctx context.Context) error {
	if h == nil || h.Service == nil || h.initializationError != nil || h.Licensing == nil || h.Licensing.Service == nil {
		return errors.New("SERVICE_NOT_READY")
	}
	if h.RuntimeReadiness != nil {
		if e := h.RuntimeReadiness(); e != nil {
			return e
		}
	}
	if e := h.Licensing.Service.VerifyStored(ctx); e != nil {
		return e
	}
	if h.Service.Config.ModelsRuntime() {
		if h.Models == nil || h.Deployments == nil || h.Nodes.Service.Inference != h.Deployments.Service {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Models.Service.VerifyStoredVersions(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.DatasetRuntime() {
		if h.Datasets == nil {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Datasets.Service.VerifyStored(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.TrainingRuntime() {
		if h.Training == nil || h.Nodes.Service.Training != h.Training {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Training.VerifyStored(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.LifecycleRuntime() {
		if h.ModelPreparations == nil || h.Training.Holdouts != h.ModelPreparations || h.Nodes.Service.Preparations != h.ModelPreparations {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.ModelPreparations.VerifyStored(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.SSORuntime() {
		if h.SSO == nil || h.SSO.Service == nil || h.Service.Sources != h.SSO.Service {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.SSO.Service.VerifyStored(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.KnowledgeRuntime() {
		if h.Knowledge == nil || h.Knowledge.Service == nil || h.Knowledge.Service.Distribution != h.Distribution.Service {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Knowledge.Service.VerifyStored(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.PluginsRuntime() {
		if h.Plugins == nil || h.Plugins.Service == nil {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Plugins.Service.VerifyStored(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.WorkflowsRuntime() {
		if h.Workflows == nil || h.Workflows.Service == nil || h.Workflows.Service.Knowledge != h.Knowledge.Service || (h.Service.Config.PluginsRuntime() && h.Workflows.Service.Plugins != h.Plugins.Service) || (!h.Service.Config.PluginsRuntime() && h.Workflows.Service.Plugins != nil) {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Workflows.Service.VerifyStored(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.PortalRuntime() {
		if h.Applications == nil || h.Notifications == nil || h.Preferences == nil || h.Applications.Service.Workflows != h.Workflows.Service || !h.Notifications.Service.Available() {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Notifications.Service.VerifyStored(ctx); e != nil {
			return e
		}
		if e := h.Preferences.Service.VerifyStored(ctx); e != nil {
			return e
		}
	}
	if h.Service.Config.ReleasesRuntime() {
		if h.Releases == nil || h.Releases.Service == nil {
			return errors.New("SERVICE_NOT_READY")
		}
		if e := h.Releases.Service.VerifyStored(ctx); e != nil {
			return e
		}
	} else if h.Releases != nil {
		return errors.New("SERVICE_NOT_READY")
	}
	return nil
}

// Only deployment-selected public origins are exposed; provider policy and keys remain private.
func ssoCallbackOrigins(c config.Config) []string {
	origins := []string{}
	if !c.SSORuntime() && !c.FullPlatformRuntime() {
		return origins
	}
	for _, origin := range []string{c.Server.PortalOrigin, c.Server.AdminOrigin} {
		if !config.SSOOrigin(origin) {
			return []string{}
		}
		if len(origins) == 0 || origins[0] != origin {
			origins = append(origins, origin)
		}
	}
	return origins
}
