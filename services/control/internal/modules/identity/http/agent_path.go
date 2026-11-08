package http

import (
	"context"
	"errors"
	"log/slog"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	nodehttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentadmission"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentgateway"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// agentPath is what serves agents: the node service, the services an agent exchange reaches,
// admission and the session gateway. Control and an agent gateway process build it the same
// way (ADR 0048), so an exchange behaves the same wherever it is served.
type agentPath struct {
	workspaces   *workspaces.Service
	nodes        *nodes.Service
	components   *components.Service
	models       *models.Service
	deployments  *deployments.Service
	datasets     *datasets.Service
	training     *training.Service
	preparations *modelpreparations.Service
	machine      *nodehttp.Handler
	sessions     *agentgateway.Gateway
	logs         *telemetrystore.Logs
	metrics      *telemetrystore.MetricsPusher
	// err joins every construction failure; a path with one must not serve.
	err error
}

// composeCertificates gives control the agent CA. A gateway only verifies, so it never holds
// the key, even when the configuration file it shares with control names one.
func composeCertificates(n *nodes.Service, s *identity.Service, service string) error {
	if service != telemetrystore.ServiceControl || s.Config.AgentIdentity == nil || s.Config.DatabaseEngine() != config.PostgreSQLDatabaseEngine {
		return nil
	}
	bundle, key, e := s.Config.AgentAuthorityMaterial()
	if e != nil {
		return e
	}
	authority, e := agentidentity.NewAuthority(bundle, key, s.Config.AgentIdentity.Lifetime())
	if e != nil {
		return e
	}
	n.Certificates = authority
	return nil
}

// composeGateways lets nodes run the model gateways of their deployments (ADR 0052). Control
// signs their serving certificates; an agent gateway process only carries their tasks, so it
// never holds the gateway CA's key.
func composeGateways(n *nodes.Service, s *identity.Service, w *workspaces.Service, service string) error {
	if !s.Config.AgentsRunGateways() {
		return nil
	}
	owner := &distribution.Service{DB: s.DB, Identity: s, Workspaces: w, Nodes: n}
	n.Gateways = owner
	if service != telemetrystore.ServiceControl {
		return nil
	}
	bundle, key, e := s.Config.ModelGatewayAuthorityMaterial()
	if e != nil {
		return e
	}
	owner.Certificates, e = agentidentity.NewAuthority(bundle, key, s.Config.ModelGatewayIdentity.Lifetime())
	return e
}

// newAgentPath builds the agent path on s for the process that service names in its
// metrics. ready, when set, fences every session exchange (agentgateway.Policy.Ready).
func newAgentPath(s *identity.Service, service string, ready func() error) agentPath {
	w := &workspaces.Service{DB: s.DB, Identity: s}
	n := &nodes.Service{DB: s.DB, Identity: s, Workspaces: w}
	authorityError := composeCertificates(n, s, service)
	c := &components.Service{Nodes: n, ArchiveFile: s.Config.Components.NodeExporterArchiveFile, CatalogDirectory: s.Config.Components.NodeExporterCatalogDirectory}
	var telemetryLogs *telemetrystore.Logs
	var telemetryMetrics *telemetrystore.MetricsPusher
	if s.Config.Telemetry != nil {
		// The settings were validated with the configuration, so these cannot fail here.
		if telemetryLogs, _ = telemetrystore.Open(s.Config.Telemetry.Settings(), slog.Default()); telemetryLogs != nil {
			c.LogStore = telemetryLogs
		}
		if telemetryMetrics, _ = telemetrystore.OpenMetrics(s.Config.Telemetry.Settings(), service, slog.Default()); telemetryMetrics != nil && telemetryLogs != nil {
			telemetryMetrics.Add(telemetryLogs.Samples)
		}
	}
	n.Components = c
	m := &models.Service{DB: s.DB, Identity: s, Workspaces: w, RootDirectory: s.Config.Models.ArtifactRoot}
	d := &deployments.Service{Nodes: n, Models: m}
	n.Inference = d
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
	gatewayError := composeGateways(n, s, w, service)
	databaseSeats, admissionError := s.Config.AgentDatabaseSeats()
	var admission *agentadmission.Controller
	if admissionError == nil {
		admission, admissionError = agentadmission.New(s.Config.Policy().AgentAdmission, databaseSeats, slog.Default())
	}
	if telemetryMetrics != nil && admission != nil {
		telemetryMetrics.Add(admission.Samples)
	}
	if admission != nil {
		n.Shed = admission
	}
	machine := &nodehttp.Handler{Service: n, Admission: admission, OpenArtifact: c.OpenArtifact, Artifact: c.TaskArtifact, ComponentArtifact: c.DownloadArtifact}
	sessions, sessionError := agentgateway.New(nodehttp.SessionBackend{Handler: machine}, agentgateway.Policy{MaxSessions: s.Config.Policy().Nodes.MaximumLiveNodes, ExchangeSeconds: s.Config.Policy().HTTP.RequestSeconds, Ready: ready}, httpserver.PublicProblem)
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
	return agentPath{workspaces: w, nodes: n, components: c, models: m, deployments: d, datasets: datasetService, training: trainingService, preparations: preparationService, machine: machine, sessions: sessions, logs: telemetryLogs, metrics: telemetryMetrics, err: errors.Join(authorityError, gatewayError, preparationError, admissionError, sessionError, n.CheckRecoveryOwners())}
}

// AgentGateway is the agent path of an agent gateway process: the session stream and the
// node service behind it, without the console, the portal or control's workers (ADR 0048).
type AgentGateway struct {
	Nodes    *nodes.Service
	Sessions *agentgateway.Gateway
	// Logs and Metrics are nil when no telemetry stores are configured.
	Logs    *telemetrystore.Logs
	Metrics *telemetrystore.MetricsPusher
}

// NewAgentGateway builds the agent path for a gateway process. ready fences every session
// exchange: while it fails, the gateway refuses agents with its error.
func NewAgentGateway(s *identity.Service, ready func() error) (*AgentGateway, error) {
	if s == nil || s.DB == nil || ready == nil {
		return nil, errors.New("AGENT_GATEWAY_CONFIGURATION_INVALID")
	}
	p := newAgentPath(s, telemetrystore.ServiceAgentGateway, ready)
	if p.err != nil {
		return nil, p.err
	}
	return &AgentGateway{Nodes: p.nodes, Sessions: p.sessions, Logs: p.logs, Metrics: p.metrics}, nil
}
