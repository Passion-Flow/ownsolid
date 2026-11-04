package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/artifactstores"
	artifacthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/artifactstores/http"
	releasehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/releases/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentgateway"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	applicationhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/cloudinventory"
	cloudhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/cloudinventory/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	componenthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/components/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	datasethttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	deploymenthttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	distributionhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	registryhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	licensehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing/http"
	middlewarehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/middleware/http"
	monitoringhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications"
	notificationhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/notifications/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences"
	preferencehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/preferences/http"
	privateclusterhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/privateclusters/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/taskdeliveries"
	taskdeliveryhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/taskdeliveries/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	knowledgehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	preparationhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	modelhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/models/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	pluginhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	ssohttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	traininghttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/training/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	workflowhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	workspacehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/health"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"strconv"
)

type Handler struct {
	Releases                   *releasehttp.Handler
	PrivateClusters            *privateclusterhttp.Handler
	Monitoring                 *monitoringhttp.Handler
	TaskDeliveries             *taskdeliveries.Service
	Middleware                 *middlewarehttp.Handler
	ImageRegistry              *imageregistry.Service
	ArtifactStores             *artifactstores.Service
	AWS                        *cloudhttp.Handler
	Licensing                  *licensehttp.Handler
	licenseInitializationError error
	initializationError        error
	SchemaReadiness            func(context.Context) error
	RuntimeReadiness           func() error
	Locales                    *localization.Registry
	Applications               *applicationhttp.Handler
	Notifications              *notificationhttp.Handler
	Preferences                *preferencehttp.Handler

	Plugins           *pluginhttp.Handler
	Workflows         *workflowhttp.Handler
	Knowledge         *knowledgehttp.Handler
	SSO               *ssohttp.Handler
	ModelPreparations *modelpreparations.Service
	Training          *training.Service
	Datasets          *datasethttp.Handler
	Service           *identity.Service
	active            chan struct{}
	Workspaces        *workspacehttp.Handler
	Nodes             *nodehttp.Handler
	// AgentSessions serves the agent session stream (ADR 0007).
	AgentSessions *agentgateway.Gateway
	Components    *componenthttp.Handler
	Models            *modelhttp.Handler
	Deployments       *deploymenthttp.Handler
	Distribution      *distributionhttp.Handler
	// TelemetryLogs is nil when no log store is configured.
	TelemetryLogs *telemetrystore.Logs
}

func New(s *identity.Service) *Handler {
	licenseService, licenseError := licensing.FromConfig(s)
	s.BusinessAdmission = licenseService
	if licenseError != nil {
		s.BusinessAdmission = nil
	}
	ssoService, ok := s.Sources.(*sso.Service)
	var ssoError error
	if !ok {
		ssoService, ssoError = sso.New(s)
	}
	w := &workspaces.Service{DB: s.DB, Identity: s}
	n := &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}
	c := &components.Service{Nodes: n, ArchiveFile: s.Config.Components.NodeExporterArchiveFile, CatalogDirectory: s.Config.Components.NodeExporterCatalogDirectory}
	var telemetryLogs *telemetrystore.Logs
	if s.Config.Telemetry != nil {
		// The address was validated with the configuration, so this cannot fail here.
		if telemetryLogs, _ = telemetrystore.Open(s.Config.Telemetry.Settings(), slog.Default()); telemetryLogs != nil {
			c.LogStore = telemetryLogs
		}
	}
	n.Components = c
	m := &models.Service{DB: s.DB, Identity: s, Workspaces: w, RootDirectory: s.Config.Models.ArtifactRoot}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
	distributionService := &distribution.Service{DB: s.DB, Identity: s, Workspaces: w, Nodes: n, Deployments: d}
	datasetService := &datasets.Service{DB: s.DB, Identity: s, Workspaces: w}
	trainingService := &training.Service{DB: s.DB, Identity: s, Workspaces: w, Nodes: n, Models: m, Datasets: datasetService, Inference: d}
	preparationService, preparationError := modelpreparations.New(s.DB, s, w, datasetService)
	if preparationService != nil {
		preparationService.Training = trainingService
		preparationService.Nodes = n
		preparationService.Models = m
		preparationService.Inference = d
		n.Preparations = preparationService
		trainingService.Holdouts = preparationService
	}
	n.Training = trainingService
	m.Referenced = func(ctx context.Context, tx transaction.Executor, tenant, workspace, model string) (bool, error) {
		found, e := distributionService.HasModelReferences(ctx, tx, tenant, workspace, model)
		if e != nil || found {
			return found, e
		}
		found, e = trainingService.Referenced(ctx, tx, tenant, workspace, model)
		if e != nil || found {
			return found, e
		}
		if preparationService == nil {
			return false, errors.New("MODEL_PREPARATION_UNAVAILABLE")
		}
		return preparationService.Referenced(ctx, tx, tenant, workspace, model)
	}
	machine := &nodehttp.Handler{Service: n, OpenArtifact: c.OpenArtifact, Artifact: c.TaskArtifact, ComponentArtifact: c.DownloadArtifact}
	sessions, sessionError := agentgateway.New(nodehttp.SessionBackend{Handler: machine}, agentgateway.Policy{MaxSessions: s.Config.Policy().Nodes.MaximumLiveNodes, ExchangeSeconds: s.Config.Policy().HTTP.RequestSeconds}, httpserver.PublicProblem)
	if preparationService != nil {
		machine.PreparationInputFile = preparationService.InputFile
		machine.PreparationEvents = preparationService.RecordEvents
		machine.BeginPreparationUpload = preparationService.BeginUpload
		machine.PreparationChunk = preparationService.UploadChunk
		machine.FinishPreparationUpload = preparationService.FinishUpload
		machine.PreparationStop = preparationService.RecordStop
	}
	machine.TrainingInputFile = trainingService.InputFile
	machine.TrainingEvents = trainingService.RecordEvents
	machine.BeginTrainingUpload = trainingService.BeginUpload
	machine.TrainingChunk = trainingService.UploadChunk
	machine.FinishTrainingUpload = trainingService.FinishUpload
	machine.TrainingStop = trainingService.RecordStop
	machine.ModelManifestWithin = func(ctx context.Context, q transaction.Executor, node nodes.Node, task nodes.Task) (wire.ModelCatalog, error) {
		command, e := n.ReadInferenceCommandWithin(ctx, q, node, task)
		if e != nil {
			return wire.ModelCatalog{}, e
		}
		if command.Action != "deploy" || command.Derived != nil {
			return wire.ModelCatalog{}, errors.New("AGENT_OPERATION_UNSUPPORTED")
		}
		if e = m.ReadyWithin(ctx, q, node.TenantID, node.WorkspaceID, command.ModelID); e != nil {
			return wire.ModelCatalog{}, e
		}
		return wire.ApprovedModel(), nil
	}
	machine.ModelFileWithin = func(ctx context.Context, q transaction.Executor, node nodes.Node, task nodes.Task, input wire.ModelFileRequest) (httpserver.Download, error) {
		command, e := n.ReadInferenceCommandWithin(ctx, q, node, task)
		if e != nil {
			return httpserver.Download{}, e
		}
		if command.Action != "deploy" {
			return httpserver.Download{}, errors.New("AGENT_OPERATION_UNSUPPORTED")
		}
		if command.Derived != nil {
			return m.OpenDerivedTransferWithin(ctx, q, node.TenantID, node.WorkspaceID, command, input)
		}
		return m.OpenTransferWithin(ctx, q, node.TenantID, node.WorkspaceID, command.ModelID, input)
	}
	knowledgeService := &knowledge.Service{DB: s.DB, Identity: s, Workspaces: w, Distribution: distributionService}
	pluginService := &plugins.Service{DB: s.DB, Identity: s, Workspaces: w, Models: m}
	workflowService := &workflows.Service{Plugins: pluginService, DB: s.DB, Identity: s, Workspaces: w, Knowledge: knowledgeService, Distribution: distributionService}
	localeRegistry, localeError := s.LocaleRegistry()
	return &Handler{TelemetryLogs: telemetryLogs, AWS: &cloudhttp.Handler{Service: cloudinventory.New(s, w)}, Licensing: &licensehttp.Handler{Service: licenseService}, licenseInitializationError: licenseError, initializationError: errors.Join(licenseError, ssoError, preparationError, localeError, sessionError, n.CheckRecoveryOwners()), AgentSessions: sessions, Locales: localeRegistry, Applications: &applicationhttp.Handler{Service: &applications.Service{DB: s.DB, Identity: s, Workspaces: w, Workflows: workflowService}}, Notifications: &notificationhttp.Handler{Service: &notifications.Service{DB: s.DB, Identity: s}}, Preferences: &preferencehttp.Handler{Service: &preferences.Service{DB: s.DB, Identity: s}}, Plugins: &pluginhttp.Handler{Service: pluginService}, Workflows: &workflowhttp.Handler{Service: workflowService}, Knowledge: &knowledgehttp.Handler{Service: knowledgeService}, SSO: &ssohttp.Handler{Service: ssoService}, ModelPreparations: preparationService, Training: trainingService, Datasets: &datasethttp.Handler{Service: datasetService}, Distribution: &distributionhttp.Handler{Service: distributionService}, Deployments: &deploymenthttp.Handler{Service: d}, Models: &modelhttp.Handler{Service: m}, Service: s, active: make(chan struct{}, s.Config.Policy().HTTP.MaxActiveRequests), Workspaces: &workspacehttp.Handler{Service: w}, Nodes: machine, Components: &componenthttp.Handler{Service: c}}
}

func Problem(w http.ResponseWriter, requestID string, e error) { httpserver.Problem(w, requestID, e) }

func (h *Handler) problem(w http.ResponseWriter, requestID string, e error) {
	httpserver.ProblemWithPolicy(w, requestID, e, h.Service.Config.Policy().HTTP)
}

func success(w http.ResponseWriter, status int, data any) {
	if download, ok := data.(httpserver.MemoryDownload); ok {
		w.Header().Set("Content-Type", download.ContentType)
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.FileName}))
		w.Header().Set("Content-Length", strconv.Itoa(len(download.Bytes)))
		w.Header().Set("X-Artifact-SHA256", download.SHA256)
		w.WriteHeader(status)
		w.Write(download.Bytes)
		return
	}
	if download, ok := data.(httpserver.Download); ok {
		defer download.File.Close()
		contentType := download.ContentType
		if contentType == "" {
			contentType = "application/gzip"
		}
		w.Header().Set("Content-Type", contentType)
		if download.FileName != "" {
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": download.FileName}))
		}
		w.Header().Set("Content-Length", strconv.FormatInt(download.Size, 10))
		w.Header().Set("X-Artifact-SHA256", download.SHA256)
		w.WriteHeader(status)
		io.CopyN(w, io.NewSectionReader(download.File, download.Offset, download.Size), download.Size)
		return
	}
	if status == 204 {
		w.WriteHeader(204)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func peer(r *http.Request) string { return httpserver.RequestClientIP(r) }
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.serveHTTP(w, r, false)
}

// MachineHandler selects the dedicated socket's policy inside composition.
// A request header or browser credential cannot select this entrance.
func (h *Handler) MachineHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.serveHTTP(w, r, true)
	})
}

// withCellRelativePath gives modules the path they route on. A percent-encoded scope never
// resolves, so RawPath, when set, ends in the same cell-relative text.
func withCellRelativePath(r *http.Request, inner string) *http.Request {
	out := r.Clone(r.Context())
	if out.URL.RawPath != "" {
		out.URL.RawPath = cellroute.Relative(out.URL.RawPath)
	}
	out.URL.Path = inner
	return out
}

func machineRoute(path string) bool {
	return strings.HasPrefix(path, "/agent/v1/") || strings.HasPrefix(path, "/gateway/v1/") || path == wire.SessionPath
}

func machineHealthRequest(r *http.Request) bool {
	if r == nil || r.URL == nil || r.Method != http.MethodGet || r.URL.Path != "/health/live" && r.URL.Path != "/health/ready" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 || r.Body != nil && r.Body != http.NoBody {
		return false
	}
	for name := range r.Header {
		if strings.EqualFold(name, "Transfer-Encoding") {
			return false
		}
	}
	return true
}

func (h *Handler) serveHTTP(w http.ResponseWriter, r *http.Request, machineIngress bool) {
	requestID := uuid.NewString()
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	c := h.Service.Config
	var connection httpserver.RequestTransport
	var transportError error
	if machineIngress {
		if c.MachineServer == nil {
			transportError = errors.New("ORIGIN_REJECTED")
		} else {
			connection, transportError = httpserver.ResolveMachineRequest(r, *c.MachineServer)
		}
	} else {
		connection, transportError = httpserver.ResolveBrowserRequest(r, c.Server)
	}
	httpserver.AuditTransport(r.Context(), requestID, connection, transportError == nil)
	if transportError != nil {
		h.problem(w, requestID, transportError)
		return
	}
	if inner, ok := cellroute.Resolve(r.URL.Path, wire.LocalScope()); !ok {
		h.problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
		return
	} else if inner != r.URL.Path {
		r = withCellRelativePath(r, inner)
	}
	if machineIngress && !machineRoute(r.URL.Path) && !machineHealthRequest(r) || !machineIngress && c.MachineServer != nil && machineRoute(r.URL.Path) {
		h.problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
		return
	}
	r = httpserver.WithRequestTransport(r, connection)
	audience, origin := connection.Audience, connection.Origin
	// An agent session lives for hours, so it holds no request slot or request deadline.
	// The gateway bounds sessions itself and checks every exchange like the HTTPS paths.
	if agentgateway.Owns(r) {
		if connection.Scheme != "https" {
			h.problem(w, requestID, errors.New("ORIGIN_REJECTED"))
			return
		}
		if audience != "admin" || h.AgentSessions == nil {
			h.problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
			return
		}
		h.AgentSessions.ServeHTTP(w, r, httpserver.RequestClientIP(r))
		return
	}
	// A long-polled claim mostly waits, so it is bounded by the session slots instead.
	if nodehttp.ClaimRoute(r.URL.Path, r.Method) && h.AgentSessions != nil {
		release, ok := h.AgentSessions.Hold()
		if !ok {
			h.problem(w, requestID, errors.New("AUTH_CAPACITY_EXCEEDED"))
			return
		}
		defer release()
	} else {
		select {
		case h.active <- struct{}{}:
			defer func() { <-h.active }()
		default:
			h.problem(w, requestID, errors.New("AUTH_CAPACITY_EXCEEDED"))
			return
		}
	}
	requestSeconds := c.Policy().HTTP.RequestSeconds
	if datasethttp.SourceUpload(r.URL.Path, r.Method) {
		policy := c.Policy().Datasets
		requestSeconds = policy.UploadSeconds + 2*policy.CommitSeconds
		controller := http.NewResponseController(w)
		if e := controller.SetReadDeadline(time.Now().Add(config.Seconds(policy.UploadSeconds))); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
		if e := controller.SetWriteDeadline(time.Now().Add(config.Seconds(requestSeconds))); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
	}
	if nodehttp.TrainingUpload(r.URL.Path, r.Method) {
		policy := c.Policy().Training
		requestSeconds = policy.UploadSeconds + 2*policy.TransactionSeconds
		controller := http.NewResponseController(w)
		if e := controller.SetReadDeadline(time.Now().Add(config.Seconds(policy.UploadSeconds))); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
		if e := controller.SetWriteDeadline(time.Now().Add(config.Seconds(requestSeconds))); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
	}
	if nodehttp.PreparationUpload(r.URL.Path, r.Method) || nodehttp.PreparationStopRoute(r.URL.Path, r.Method) || preparationhttp.ReviewRoute(r.URL.Path, r.Method) {
		policy := c.Policy().ModelLifecycle
		operationSeconds := policy.OutputSeconds
		if strings.HasSuffix(r.URL.Path, "/chunks") {
			operationSeconds = policy.TransferSeconds
		}
		requestSeconds = operationSeconds + 2*policy.TransactionSeconds
		controller := http.NewResponseController(w)
		if e := controller.SetReadDeadline(time.Now().Add(config.Seconds(policy.TransferSeconds))); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
		if e := controller.SetWriteDeadline(time.Now().Add(config.Seconds(requestSeconds))); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
	}
	if budget := artifactTransferBudget(c, r); budget > 0 {
		requestSeconds = budget
		controller := http.NewResponseController(w)
		deadline := time.Now().Add(config.Seconds(budget))
		if e := controller.SetReadDeadline(deadline); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
		if e := controller.SetWriteDeadline(deadline); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
	}
	if budget := registryRequestBudget(c, r); budget > 0 {
		requestSeconds = budget
		controller := http.NewResponseController(w)
		deadline := time.Now().Add(config.Seconds(budget))
		if e := controller.SetReadDeadline(deadline); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
		if e := controller.SetWriteDeadline(deadline); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
	}
	if nodehttp.ClaimRoute(r.URL.Path, r.Method) {
		// A long-polled claim holds the request open for its wait before it answers.
		requestSeconds += wire.MaxClaimWaitSeconds
		if e := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(config.Seconds(requestSeconds))); e != nil && !errors.Is(e, http.ErrNotSupported) {
			h.problem(w, requestID, errors.New("SERVICE_NOT_READY"))
			return
		}
	}
	if taskdeliveryhttp.Owns(r.URL.Path) && c.TaskEvents.Enabled {
		requestSeconds = c.TaskEvents.RequestSeconds
	}
	if budget := h.middlewareRequestBudget(r.URL.Path); budget > 0 {
		requestSeconds = budget
	}
	if budget := h.monitoringRequestBudget(r.URL.Path); budget > 0 {
		requestSeconds = budget
	}
	ctx, cancel := context.WithTimeout(r.Context(), config.Seconds(requestSeconds))
	defer cancel()
	r = r.WithContext(ctx)
	if r.URL.Path == "/runtime-config.json" {
		if r.Method != http.MethodGet {
			h.problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
			return
		}
		success(w, http.StatusOK, map[string]any{"topology": map[string]any{"size": c.Topology.EffectiveSize()}, "sso_callback_origins": ssoCallbackOrigins(c), "transport": map[string]any{"mode": c.Server.EffectiveTransportMode(), "plaintext": c.Server.EffectiveTransportMode() == config.TransportHTTP}, "private_clusters": h.privateClusterPublicPolicy(), "monitoring": h.monitoringPublicPolicy(audience), "middleware": h.middlewarePublicPolicy(), "task_events": taskDeliveryPublicPolicy(c), "image_registry": registryPublicPolicy(c), "artifact_storage": artifactPublicPolicy(c), "workspaces": c.Policy().Workspaces, "ui": c.Policy().UI, "request_timeout_milliseconds": c.Policy().HTTP.RequestSeconds * 1000, "component_default_version": wire.NodeExporterVersion, "component_max_scrapes": wire.MaxComponentScrapeRequests, "component_default_config": wire.DefaultComponentConfig()})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/v1/localization/") {
		h.Locales.ServeHTTPWithPolicy(w, r, h.Service.Config.Policy().HTTP)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/agent/v1/") {
		if connection.Scheme != "https" {
			h.problem(w, requestID, errors.New("ORIGIN_REJECTED"))
			return
		}
		if audience != "admin" {
			h.problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
			return
		}
		if registryhttp.MachineOwns(r.URL.Path) {
			data, status, e := (&registryhttp.Handler{Service: h.ImageRegistry}).Machine(r)
			if e != nil {
				h.problem(w, requestID, e)
				return
			}
			success(w, status, data)
			return
		}
		data, status, e := h.Nodes.Machine(r, requestID)
		if e != nil {
			h.problem(w, requestID, e)
			return
		}
		success(w, status, data)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/gateway/v1/") {
		if connection.Scheme != "https" {
			h.problem(w, requestID, errors.New("ORIGIN_REJECTED"))
			return
		}
		if audience != "admin" {
			h.problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
			return
		}
		data, status, e := h.Distribution.Machine(r, requestID)
		if e != nil {
			h.problem(w, requestID, e)
			return
		}
		success(w, status, data)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/health/") {
		data, status, e := h.api(w, r, audience, origin, requestID)
		if e != nil {
			h.problem(w, requestID, e)
			return
		}
		if status != 0 {
			success(w, status, data)
		}
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		h.problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
		return
	}
	root := c.Server.PortalAssets
	if audience == "admin" {
		root = c.Server.AdminAssets
	}
	if !httpserver.Assets(w, r, root) {
		h.problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
	}
}
func (h *Handler) api(w http.ResponseWriter, r *http.Request, audience, origin, requestID string) (any, int, error) {
	s := h.Service
	ctx := r.Context()
	path := r.URL.Path
	bad := errors.New("VALIDATION_FAILED")
	if strings.HasPrefix(path, "/health/") {
		if path == "/health/ready" && r.Method == "GET" {
			if e := h.VerifyStored(ctx); e != nil {
				return nil, 0, errors.New("SERVICE_NOT_READY")
			}
		}
		if h.SchemaReadiness == nil && s.Storage == nil && s.DB != nil {
			return health.Check(ctx, s.DB, r.Method, path)
		}
		store, e := s.Repository()
		if e != nil && path != "/health/live" {
			return nil, 0, errors.New("SERVICE_NOT_READY")
		}
		return health.CheckSelected(ctx, store, h.SchemaReadiness, r.Method, path)
	}
	if strings.HasPrefix(path, "/api/v1/admin/") && audience != "admin" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if audience == "admin" && (path == "/api/v1/auth/register" || path == "/api/v1/auth/invitation-validations") {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	cookies, e := httpserver.NewCookiePolicy(s.Config.Server.EffectiveTransportMode())
	if e != nil {
		return nil, 0, e
	}
	session, ok := cookies.Read(r, audience, httpserver.SessionCookie)
	if !ok {
		return nil, 0, errors.New("AUTH_REQUIRED")
	}
	contextCookie, contextOK := cookies.Read(r, audience, httpserver.CSRFContextCookie)
	if r.Method == "GET" && path == "/api/v1/auth/csrf" {
		if _, e := s.Admit(ctx, identity.Limit{Purpose: "csrf.ip", Identifier: peer(r), Count: s.Config.Policy().Rates.CSRFCount, Window: config.Seconds(s.Config.Policy().Rates.CSRFWindowSeconds)}); e != nil {
			return nil, 0, e
		}
		if !contextOK || !security.ValidToken(contextCookie) {
			contextCookie = security.Token()
		}
		token, exp := security.CSRF(s.HMACKey, audience, contextCookie, session, time.Now(), config.Seconds(s.Config.Policy().Identity.CSRFSeconds))
		cookies.Set(w, audience, httpserver.CSRFContextCookie, contextCookie, s.Config.Policy().Identity.CSRFSeconds)
		return map[string]any{"token": token, "expires_at": exp}, 200, nil
	}
	if r.Method == "POST" || r.Method == "PUT" || r.Method == "PATCH" || r.Method == "DELETE" {
		if len(r.Header.Values("Origin")) != 1 || r.Header.Get("Origin") != origin || len(r.Header.Values("Sec-Fetch-Site")) > 1 || (r.Header.Get("Sec-Fetch-Site") != "" && r.Header.Get("Sec-Fetch-Site") != "same-origin") {
			return nil, 0, errors.New("ORIGIN_REJECTED")
		}
		if len(r.Header.Values("X-CSRF-Token")) != 1 || !contextOK || !security.VerifyCSRF(s.HMACKey, audience, contextCookie, session, r.Header.Get("X-CSRF-Token"), time.Now(), config.Seconds(s.Config.Policy().Identity.CSRFSeconds)) {
			return nil, 0, errors.New("CSRF_INVALID")
		}
	}
	if monitoringhttp.Owns(path) {
		if r.Method != http.MethodGet {
			return nil, 0, errors.New("RESOURCE_NOT_FOUND")
		}
		if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin {
			return nil, 0, errors.New("ORIGIN_REJECTED")
		}
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Monitoring.Handle(r, a, requestID)
	}
	if e := h.licenseAdmission(r, audience, session); e != nil {
		return nil, 0, e
	}
	if privateclusterhttp.Owns(path) {
		if r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin {
			return nil, 0, errors.New("ORIGIN_REJECTED")
		}
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.PrivateClusters.Handle(r, a, requestID)
	}
	if licensehttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Licensing.Handle(r, a, requestID)
	}
	if artifacthttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		data, status, e := (&artifacthttp.Handler{Service: h.ArtifactStores}).Handle(r, a, requestID)
		if e != nil {
			return nil, 0, e
		}
		if download, ok := data.(httpserver.Download); ok {
			return nil, 0, httpserver.ServeDownload(w, r, status, download)
		}
		return data, status, nil
	}
	if registryhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return (&registryhttp.Handler{Service: h.ImageRegistry}).Handle(r, a, requestID)
	}
	if taskdeliveryhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return (&taskdeliveryhttp.Handler{Service: h.TaskDeliveries}).Handle(r, a, requestID)
	}
	if releasehttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Releases.Handle(r, a, requestID)
	}
	if middlewarehttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Middleware.Handle(w, r, a, requestID)
	}
	if cloudhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.AWS.Handle(r, a, requestID)
	}
	if ssohttp.Owns(path) {
		return h.SSO.Handle(w, r, audience, session, requestID)
	}
	if traininghttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return (&traininghttp.Handler{Service: h.Training}).Handle(r, a, requestID)
	}
	if preparationhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return (&preparationhttp.Handler{Service: h.ModelPreparations}).Handle(r, a, requestID)
	}
	if applicationhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Applications.Handle(r, a, requestID)
	}
	if notificationhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Notifications.Handle(r, a, requestID)
	}
	if preferencehttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Preferences.Handle(r, a, requestID)
	}
	if pluginhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Plugins.Handle(r, a, requestID)
	}
	if workflowhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Workflows.Handle(r, a, requestID)
	}
	if knowledgehttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Knowledge.Handle(r, a, requestID)
	}
	if datasethttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Datasets.Handle(r, a, requestID)
	}
	if distributionhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Distribution.Human(r, a, requestID)
	}
	if deploymenthttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Deployments.Handle(r, a, requestID)
	}
	if modelhttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Models.Handle(r, a, requestID)
	}
	if componenthttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Components.Handle(r, a, requestID)
	}
	if nodehttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Nodes.Human(r, a, requestID)
	}
	if path == "/api/v1/workspaces" || strings.HasPrefix(path, "/api/v1/workspaces/") || path == "/api/v1/admin/workspaces" || strings.HasPrefix(path, "/api/v1/admin/workspaces/") {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.Workspaces.Handle(w, r, a, requestID)
	}
	route := r.Method + " " + path
	switch route {
	case "POST /api/v1/auth/login":
		var v struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if e := decode(r, &v); e != nil {
			return nil, 0, e
		}
		email, _, e := security.Email(v.Email)
		if e != nil || len(v.Password) < 1 || len(v.Password) > security.MaximumPasswordInputBytes {
			return nil, 0, bad
		}
		if _, e = s.Admit(ctx, identity.Limit{Purpose: "login.email", Identifier: email, Count: s.Config.Policy().Rates.LoginEmailCount, Window: config.Seconds(s.Config.Policy().Rates.LoginEmailWindowSeconds)}, identity.Limit{Purpose: "login.ip." + audience, Identifier: peer(r), Count: s.Config.Policy().Rates.LoginIPCount, Window: config.Seconds(s.Config.Policy().Rates.LoginIPWindowSeconds)}); e != nil {
			return nil, 0, e
		}
		me, token, expiry, e := s.Login(ctx, email, v.Password, audience, session, requestID)
		if e != nil {
			return nil, 0, e
		}
		cookies.Set(w, audience, httpserver.SessionCookie, token, int(s.Absolute(audience)/time.Second))
		return map[string]any{"user": me.User, "workspace": me.Workspace, "audience": audience, "session": map[string]any{"absolute_expires_at": expiry, "idle_timeout_seconds": int(s.Idle(audience) / time.Second)}}, 200, nil
	case "POST /api/v1/auth/logout":
		b, e := io.ReadAll(io.LimitReader(r.Body, 1))
		if e != nil || len(b) != 0 {
			return nil, 0, bad
		}
		if e = s.Logout(ctx, session, audience, requestID); e != nil {
			return nil, 0, e
		}
		cookies.ClearIdentity(w, audience)
		return nil, 204, nil
	case "POST /api/v1/auth/invitation-validations":
		var v struct {
			Token string `json:"token"`
		}
		if e := decode(r, &v); e != nil {
			return nil, 0, e
		}
		if !wireToken(v.Token) {
			return nil, 0, bad
		}
		if _, e := s.Admit(ctx, identity.Limit{Purpose: "invite.validate.ip", Identifier: peer(r), Count: s.Config.Policy().Rates.TokenIPCount, Window: config.Seconds(s.Config.Policy().Rates.TokenIPWindowSeconds)}); e != nil {
			return nil, 0, e
		}
		data, e := s.ValidateInvitation(ctx, v.Token)
		return data, 200, e
	case "POST /api/v1/auth/register":
		var v struct {
			Token       string `json:"token"`
			DisplayName string `json:"display_name"`
			Password    string `json:"password"`
		}
		if e := decode(r, &v); e != nil {
			return nil, 0, e
		}
		if !wireToken(v.Token) || v.DisplayName == "" || v.Password == "" || len(v.Password) > security.MaximumPasswordInputBytes {
			return nil, 0, bad
		}
		if _, e := s.Admit(ctx, identity.Limit{Purpose: "register.ip", Identifier: peer(r), Count: s.Config.Policy().Rates.TokenIPCount, Window: config.Seconds(s.Config.Policy().Rates.TokenIPWindowSeconds)}); e != nil {
			return nil, 0, e
		}
		e := s.Register(ctx, v.Token, v.DisplayName, v.Password, requestID)
		return map[string]bool{"registered": true}, 201, e
	case "POST /api/v1/auth/password-reset-requests":
		var v struct {
			Email string `json:"email"`
		}
		if e := decode(r, &v); e != nil {
			return nil, 0, e
		}
		e := s.RequestReset(ctx, v.Email, audience, peer(r), requestID)
		return map[string]bool{"accepted": true}, 202, e
	case "POST /api/v1/auth/password-resets":
		var v struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if e := decode(r, &v); e != nil {
			return nil, 0, e
		}
		if !wireToken(v.Token) || v.Password == "" || len(v.Password) > security.MaximumPasswordInputBytes {
			return nil, 0, bad
		}
		if _, e := s.Admit(ctx, identity.Limit{Purpose: "reset.consume.ip", Identifier: peer(r), Count: s.Config.Policy().Rates.TokenIPCount, Window: config.Seconds(s.Config.Policy().Rates.TokenIPWindowSeconds)}); e != nil {
			return nil, 0, e
		}
		if e := s.Reset(ctx, v.Token, v.Password, audience, requestID); e != nil {
			return nil, 0, e
		}
		cookies.ClearIdentity(w, audience)
		return map[string]bool{"reset": true}, 200, nil
	}
	if path == "/api/v1/me" || strings.HasPrefix(path, "/api/v1/admin/") {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			if e.Error() == "AUTH_SESSION_EXPIRED" {
				cookies.Set(w, audience, httpserver.SessionCookie, "", -1)
			}
			return nil, 0, e
		}
		if route == "GET /api/v1/me" {
			return a.Me, 200, nil
		}
		if route == "PATCH /api/v1/me" {
			var v struct {
				DisplayName string `json:"display_name"`
				Locale      string `json:"locale"`
				Version     int64  `json:"version"`
			}
			if e = decode(r, &v); e != nil {
				return nil, 0, e
			}
			if !version(v.Version) {
				return nil, 0, bad
			}
			data, e := s.UpdateMe(ctx, a, v.DisplayName, v.Locale, v.Version, requestID)
			return data, 200, e
		}
		if strings.HasPrefix(path, "/api/v1/admin/") {
			if !a.Me.User.IsPlatformAdmin {
				return nil, 0, errors.New("ACCESS_DENIED")
			}
			parts := strings.Split(strings.TrimPrefix(path, "/api/v1/admin/"), "/")
			if r.Method == "GET" && len(parts) == 1 {
				data, e := s.ListForActor(ctx, a, parts[0], r.URL.Query())
				return data, 200, e
			}
			if len(parts) >= 2 && !validUUID(parts[1]) {
				return nil, 0, bad
			}
			if parts[0] == "users" && len(parts) == 2 && r.Method == "GET" {
				data, e := s.GetUser(ctx, parts[1])
				return data, 200, e
			}
			if parts[0] == "users" && len(parts) == 3 && parts[2] == "status" && r.Method == "PATCH" {
				var v struct {
					Status  string `json:"status"`
					Version int64  `json:"version"`
					Reason  string `json:"reason"`
				}
				if e = decode(r, &v); e != nil {
					return nil, 0, e
				}
				if !version(v.Version) || !reason(v.Reason) || (v.Status != "active" && v.Status != "disabled") {
					return nil, 0, bad
				}
				data, e := s.SetStatus(ctx, a, parts[1], v.Status, v.Version, v.Reason, requestID)
				return data, 200, e
			}
			if parts[0] == "invitations" && r.Method == "POST" {
				key := r.Header.Get("Idempotency-Key")
				if (len(parts) == 1 || (len(parts) == 3 && parts[2] == "resends")) && !validUUID(key) {
					return nil, 0, bad
				}
				if len(parts) == 1 {
					var v struct {
						Email  string `json:"email"`
						Locale string `json:"locale"`
					}
					if e = decode(r, &v); e != nil {
						return nil, 0, e
					}
					data, e := s.Invite(ctx, a, "", "createInvitation", key, v.Email, v.Locale, 0, requestID)
					return data, 201, e
				}
				if len(parts) == 3 && parts[2] == "resends" {
					var v struct {
						Version int64 `json:"version"`
					}
					if e = decode(r, &v); e != nil {
						return nil, 0, e
					}
					if !version(v.Version) {
						return nil, 0, bad
					}
					data, e := s.Invite(ctx, a, parts[1], "resendInvitation", key, "", "", v.Version, requestID)
					return data, 200, e
				}
				if len(parts) == 3 && parts[2] == "revocations" {
					var v struct {
						Version int64  `json:"version"`
						Reason  string `json:"reason"`
					}
					if e = decode(r, &v); e != nil {
						return nil, 0, e
					}
					if !version(v.Version) || !reason(v.Reason) {
						return nil, 0, bad
					}
					data, e := s.RevokeInvitation(ctx, a, parts[1], v.Version, v.Reason, requestID)
					return data, 200, e
				}
			}
		}
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
func version(v int64) bool    { return v >= 1 && v <= 9007199254740991 }
func reason(v string) bool    { return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= 500 }
func validUUID(v string) bool { _, e := uuid.Parse(v); return len(v) == 36 && e == nil }

func wireToken(v string) bool {
	if len(v) != 43 {
		return false
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}
