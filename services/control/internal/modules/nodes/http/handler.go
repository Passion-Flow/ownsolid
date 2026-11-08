package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentadmission"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	lifecyclewire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	trainingwire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

type Handler struct {
	ModelFileWithin         func(context.Context, transaction.Executor, nodes.Node, nodes.Task, wire.ModelFileRequest) (httpserver.Download, error)
	ModelManifestWithin     func(context.Context, transaction.Executor, nodes.Node, nodes.Task) (wire.ModelCatalog, error)
	BeginPreparationUpload  func(context.Context, string, string, string, string, []byte) (lifecyclewire.Upload, error)
	PreparationChunk        func(context.Context, string, string, string, string, string, lifecyclewire.ChunkRequest, io.Reader) (lifecyclewire.ChunkAcceptance, error)
	FinishPreparationUpload func(context.Context, string, string, string, string, string) (lifecyclewire.OutputAcceptance, error)
	PreparationStop         func(context.Context, string, string, string, string, lifecyclewire.StopReceipt, []byte) (lifecyclewire.StopAcceptance, error)
	PreparationInputFile    func(context.Context, string, string, string, lifecyclewire.InputFileRequest) (httpserver.Download, error)
	PreparationEvents       func(context.Context, string, string, string, string, [][]byte) ([]lifecyclewire.EventAcceptance, error)
	Service                 *nodes.Service
	// Admission admits every agent call before it reaches the service (ADR 0018). The
	// caller of Machine admits HTTPS calls; the handler uses it for claim seats.
	Admission *agentadmission.Controller
	ComponentArtifact       func(context.Context, string, string, string, string) (httpserver.Download, error)
	OpenArtifact            func() (*os.File, error)
	Artifact                func(context.Context, pgx.Tx, nodes.Node, nodes.Task) (httpserver.Download, error)
	ModelFile               func(context.Context, pgx.Tx, nodes.Node, nodes.Task, wire.ModelFileRequest) (httpserver.Download, error)
	ModelManifest           func(context.Context, pgx.Tx, nodes.Node, nodes.Task) (wire.ModelCatalog, error)
	TrainingInputFile       func(context.Context, string, string, string, trainingwire.InputFileRequest) (httpserver.Download, error)
	TrainingEvents          func(context.Context, string, string, string, string, [][]byte) ([]trainingwire.EventAcceptance, error)
	BeginTrainingUpload     func(context.Context, string, string, string, string, string, []byte) (trainingwire.Upload, error)
	TrainingChunk           func(context.Context, string, string, string, string, string, trainingwire.ChunkRequest, io.Reader) (trainingwire.ChunkAcceptance, error)
	FinishTrainingUpload    func(context.Context, string, string, string, string, string) (trainingwire.OutputAcceptance, error)
	TrainingStop            func(context.Context, string, string, string, string, trainingwire.StopReceipt, []byte) (trainingwire.StopAcceptance, error)
}

// pacingPath is the pacing of this cell's agents (ADR 0013).
const pacingPath = "/api/v1/admin/agent-pacing"

// shedPath is how much agent traffic this cell refuses (ADR 0043).
const shedPath = "/api/v1/admin/agent-shed"

func (h *Handler) agentPacing(r *http.Request, a identity.Auth, request string) (any, int, error) {
	switch r.Method {
	case http.MethodGet:
		out, e := h.Service.AgentPacing(r.Context(), a)
		return out, 200, e
	case http.MethodPut:
		var input struct {
			wire.Pacing
			Version int64  `json:"version"`
			Reason  string `json:"reason"`
		}
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.UpdateAgentPacing(r.Context(), a, input.Pacing, input.Version, input.Reason, request)
		return out, 200, e
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}

func (h *Handler) agentShed(r *http.Request, a identity.Auth, request string) (any, int, error) {
	switch r.Method {
	case http.MethodGet:
		out, e := h.Service.AgentShed(r.Context(), a)
		return out, 200, e
	case http.MethodPut:
		var input struct {
			Level   string `json:"level"`
			Version int64  `json:"version"`
			Reason  string `json:"reason"`
		}
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.UpdateAgentShed(r.Context(), a, input.Level, input.Version, input.Reason, request)
		return out, 200, e
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}

// tasksPath is where an administrator reads tasks across every node.
const tasksPath = "/api/v1/admin/tasks"

// adminTasks serves the task reads. Starting a task stays under its node.
func (h *Handler) adminTasks(r *http.Request, a identity.Auth) (any, int, error) {
	if r.Method != "GET" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, tasksPath), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == "":
		out, e := h.Service.AdminTasks(r.Context(), a, r.URL.Query())
		return out, 200, e
	case len(parts) == 1 && id(parts[0]):
		out, e := h.Service.AdminTask(r.Context(), a, parts[0])
		return out, 200, e
	case len(parts) == 2 && id(parts[0]) && parts[1] == "log":
		out, e := h.Service.AdminTaskLog(r.Context(), a, parts[0], r.URL.Query())
		return out, 200, e
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}

func Owns(path string) bool {
	for _, prefix := range []string{"/api/v1/admin/nodes", "/api/v1/admin/node-join-tokens", pacingPath, shedPath, tasksPath} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	if strings.HasPrefix(path, "/api/v1/workspaces/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		return len(parts) >= 2 && parts[1] == "nodes"
	}
	return false
}
// rackNetwork names the rack an address enrolls from: its source network, because each rack of a
// leaf-spine fabric has a subnet of its own. An address that does not parse keys no limit.
func rackNetwork(host string, policy config.RatesPolicy) (string, bool) {
	address, e := netip.ParseAddr(host)
	if e != nil {
		return "", false
	}
	address = address.Unmap()
	bits := policy.AgentEnrollRackIPv6Prefix
	if address.Is4() {
		bits = policy.AgentEnrollRackIPv4Prefix
	}
	network, e := address.Prefix(bits)
	if e != nil {
		return "", false
	}
	return network.String(), true
}

// certificates serves the two certificate paths of a node, which check the connection's
// certificate themselves because a node may have to come here without one.
func (h *Handler) certificates(r *http.Request, parts []string, credential, request string, peer agentidentity.Peer) (any, int, error) {
	switch {
	case len(parts) == 3:
		var input wire.CertificateRenewal
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.RenewCertificate(r.Context(), parts[1], credential, request, input, peer)
		return out, 200, e
	case len(parts) == 4 && parts[3] == wire.CertificateConfirmationsPath:
		out, e := h.Service.ConfirmCertificate(r.Context(), parts[1], credential, peer)
		return out, 200, e
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}

func id(v string) bool     { _, e := uuid.Parse(v); return e == nil && len(v) == 36 }
func reason(v string) bool { return strings.TrimSpace(v) != "" && utf8.RuneCountInString(v) <= 500 }
func (h *Handler) Machine(r *http.Request, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	if r.Method != "POST" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if r.URL.RawQuery != "" {
		return nil, 0, bad
	}
	path := r.URL.Path
	if path == "/agent/v1/enroll" {
		// Join tokens are guessable only by trying, so enrollment keeps a limit shared by
		// every control process, on top of its admission.
		policy := h.Service.Identity.Config.Policy().Rates
		host := httpserver.RequestClientIP(r)
		// A rack that powers on enrolls together, from one network, and waits its turn
		// here before any enrollment work (ADR 0050).
		limits := []identity.Limit{{Purpose: "agent.enroll.ip", Identifier: host, Count: policy.AgentEnrollIPCount, Window: config.Seconds(policy.AgentEnrollIPWindowSeconds)}}
		if rack, known := rackNetwork(host, policy); known {
			limits = append(limits, identity.Limit{Purpose: "agent.enroll.rack", Identifier: rack, Count: policy.AgentEnrollRackCount, Window: config.Seconds(policy.AgentEnrollRackWindowSeconds)})
		}
		if _, e := h.Service.Identity.Admit(r.Context(), limits...); e != nil {
			return nil, 0, e
		}
		var input wire.Enrollment
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Enroll(r.Context(), input, request)
		return out, 201, e
	}
	parts := strings.Split(strings.TrimPrefix(path, "/agent/v1/"), "/")
	if len(parts) < 3 || parts[0] != "nodes" || !id(parts[1]) {
		return nil, 0, bad
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.Count(values[0], " ") != 1 {
		return nil, 0, errors.New("AGENT_UNAUTHORIZED")
	}
	credential := strings.TrimPrefix(values[0], "Bearer ")
	if !security.ValidToken(credential) {
		return nil, 0, errors.New("AGENT_UNAUTHORIZED")
	}
	peer := agentidentity.PeerOf(r.TLS)
	if parts[2] == wire.CertificatesPath {
		return h.certificates(r, parts, credential, request, peer)
	}
	if e := h.Service.CheckPeer(r.Context(), parts[1], peer); e != nil {
		return nil, 0, e
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "model-preparation-input-files" {
		return h.preparationInput(r, parts, credential)
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "model-preparation-events" {
		return h.preparationEvents(r, parts, credential)
	}
	if PreparationUpload(path, r.Method) {
		return h.preparationUpload(r, parts, credential)
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "model-preparation-stops" {
		return h.preparationStop(r, parts, credential)
	}
	if TrainingUpload(path, r.Method) {
		return h.trainingUpload(r, parts, credential)
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "training-stops" {
		return h.trainingStop(r, parts, credential)
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "training-input-files" {
		if h.TrainingInputFile == nil {
			return nil, 0, errors.New("TRAINING_UNAVAILABLE")
		}
		var raw json.RawMessage
		if e := httpserver.DecodeJSON(r, &raw); e != nil {
			return nil, 0, e
		}
		var input trainingwire.InputFileRequest
		if trainingwire.DecodeStrict(raw, &input) != nil || input.Validate() != nil {
			return nil, 0, errors.New("TRAINING_TRANSFER_INVALID")
		}
		out, e := h.TrainingInputFile(r.Context(), parts[1], credential, parts[3], input)
		return out, 200, e
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "training-events" {
		if h.TrainingEvents == nil {
			return nil, 0, errors.New("TRAINING_UNAVAILABLE")
		}
		var raw json.RawMessage
		if e := httpserver.DecodeJSON(r, &raw); e != nil {
			return nil, 0, e
		}
		var input trainingwire.EventSubmission
		if trainingwire.DecodeStrict(raw, &input) != nil || len(input.Events) < 1 || len(input.Events) > trainingwire.MaximumEventBatch {
			return nil, 0, errors.New("TRAINING_EVENT_INVALID")
		}
		var fields map[string]json.RawMessage
		var events []json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(fields["events"], &events) != nil {
			return nil, 0, errors.New("TRAINING_EVENT_INVALID")
		}
		values := make([][]byte, len(events))
		for index, event := range events {
			values[index] = event
		}
		out, e := h.TrainingEvents(r.Context(), parts[1], credential, parts[3], input.LeaseToken, values)
		return map[string]any{"items": out}, 200, e
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "model-files" {
		var input wire.ModelFileRequest
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		if _, allowed := wire.ModelFileForName(input.File); !allowed || input.Offset < 0 || input.Offset%wire.ModelTransferChunkBytes != 0 || input.Length < 1 || input.Length > wire.ModelTransferChunkBytes {
			return nil, 0, errors.New("VALIDATION_FAILED")
		}
		var download httpserver.Download
		var e error
		if h.ModelFileWithin != nil {
			e = h.Service.WithLeaseExecutor(r.Context(), parts[1], credential, parts[3], input.LeaseToken, func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task) error {
				if !wire.IsInferenceProfile(n.ExecutionProfile) || t.Operation != wire.InferenceOperation("deploy") && t.Operation != wire.DerivedInferenceOperation {
					return errors.New("AGENT_OPERATION_UNSUPPORTED")
				}
				command, e := h.Service.ReadInferenceCommandWithin(ctx, q, n, t)
				if e != nil {
					return e
				}
				if e = input.ValidateForCommand(command); e != nil {
					return e
				}
				download, e = h.ModelFileWithin(ctx, q, n, t, input)
				return e
			})
		} else {
			e = h.Service.WithLease(r.Context(), parts[1], credential, parts[3], input.LeaseToken, func(tx pgx.Tx, n nodes.Node, t nodes.Task) error {
				if h.ModelFile == nil || !wire.IsInferenceProfile(n.ExecutionProfile) || t.Operation != wire.InferenceOperation("deploy") && t.Operation != wire.DerivedInferenceOperation {
					return errors.New("AGENT_OPERATION_UNSUPPORTED")
				}
				command, e := h.Service.ReadInferenceCommand(r.Context(), tx, n, t)
				if e != nil {
					return e
				}
				if e = input.ValidateForCommand(command); e != nil {
					return e
				}
				download, e = h.ModelFile(r.Context(), tx, n, t, input)
				return e
			})
		}
		if e != nil {
			if download.File != nil {
				download.File.Close()
			}
			return nil, 0, e
		}
		return download, 200, nil
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "model-manifest" {
		var input wire.LeaseRenewal
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		var catalog wire.ModelCatalog
		var e error
		if h.ModelManifestWithin != nil {
			e = h.Service.WithLeaseExecutor(r.Context(), parts[1], credential, parts[3], input.LeaseToken, func(ctx context.Context, q transaction.Executor, n nodes.Node, t nodes.Task) error {
				if !wire.IsInferenceProfile(n.ExecutionProfile) || t.Operation != wire.InferenceOperation("deploy") {
					return errors.New("AGENT_OPERATION_UNSUPPORTED")
				}
				var e error
				catalog, e = h.ModelManifestWithin(ctx, q, n, t)
				return e
			})
		} else {
			e = h.Service.WithLease(r.Context(), parts[1], credential, parts[3], input.LeaseToken, func(tx pgx.Tx, n nodes.Node, t nodes.Task) error {
				if h.ModelManifest == nil || !wire.IsInferenceProfile(n.ExecutionProfile) || t.Operation != wire.InferenceOperation("deploy") {
					return errors.New("AGENT_OPERATION_UNSUPPORTED")
				}
				var e error
				catalog, e = h.ModelManifest(r.Context(), tx, n, t)
				return e
			})
		}
		if e != nil {
			return nil, 0, e
		}
		return catalog, 200, e
	}
	if len(parts) == 3 && parts[2] == "heartbeat" {
		var input wire.Handshake
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Heartbeat(r.Context(), parts[1], credential, input)
		return out, 200, e
	}
	if len(parts) == 4 && parts[2] == "tasks" && parts[3] == "claims" {
		// An empty object claims at once; wait_seconds long-polls, the session fallback.
		var input wire.ClaimRequest
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		if e := input.Validate(); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.ClaimWaiting(r.Context(), parts[1], credential, request, input, h.claimSeat)
		return out, 200, e
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == wire.TaskAcknowledgementsPath {
		var input wire.TaskAcknowledgementRequest
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.Acknowledge(r.Context(), parts[1], credential, parts[3], input, request)
		return out, 200, e
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && (parts[4] == "renewals" || parts[4] == "artifact") {
		var input wire.LeaseRenewal
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		if parts[4] == "renewals" {
			out, e := h.Service.Renew(r.Context(), parts[1], credential, parts[3], input.LeaseToken)
			return out, 200, e
		}
		if h.ComponentArtifact != nil {
			download, e := h.ComponentArtifact(r.Context(), parts[1], credential, parts[3], input.LeaseToken)
			if e != nil {
				if download.File != nil {
					download.File.Close()
				}
				return nil, 0, e
			}
			if download.File == nil {
				return nil, 0, errors.New("COMPONENT_ASSET_UNAVAILABLE")
			}
			return download, 200, nil
		}

		var file *os.File
		var download httpserver.Download
		e := h.Service.WithLease(r.Context(), parts[1], credential, parts[3], input.LeaseToken, func(tx pgx.Tx, n nodes.Node, t nodes.Task) error {
			if (t.Operation != wire.ComponentOperation("install") && t.Operation != wire.ComponentOperation("upgrade")) || !wire.IsManagementProfile(n.ExecutionProfile) {
				return errors.New("AGENT_OPERATION_UNSUPPORTED")
			}
			if h.Artifact != nil {
				var e error
				download, e = h.Artifact(r.Context(), tx, n, t)
				file = download.File
				return e
			}
			if h.OpenArtifact == nil {
				return errors.New("COMPONENT_ASSET_UNAVAILABLE")
			}
			var e error
			file, e = h.OpenArtifact()
			return e
		})
		if e != nil {
			if file != nil {
				file.Close()
			}
			return nil, 0, e
		}
		if download.File != nil {
			return download, 200, nil
		}
		return httpserver.Download{File: file, Size: wire.NodeExporterArchiveBytes, SHA256: wire.NodeExporterSHA256}, 200, nil
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == wire.TaskProgressPath {
		var input wire.TaskProgress
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.RecordTaskProgress(r.Context(), parts[1], credential, parts[3], input)
		return out, 200, e
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == wire.GatewayActivationPath {
		var input wire.GatewayActivation
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.ActivateGateway(r.Context(), parts[1], credential, parts[3], input, request)
		return out, 200, e
	}
	if len(parts) == 5 && parts[2] == "tasks" && id(parts[3]) && parts[4] == "results" {
		var input wire.Result
		if e := httpserver.DecodeJSON(r, &input); e != nil {
			return nil, 0, e
		}
		state, e := h.Service.Complete(r.Context(), parts[1], credential, parts[3], input, request)
		return map[string]string{"state": state}, 200, e
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
func (h *Handler) Human(r *http.Request, a identity.Auth, request string) (any, int, error) {
	path := r.URL.Path
	bad := errors.New("VALIDATION_FAILED")
	admin := strings.HasPrefix(path, "/api/v1/admin/")
	if admin && (a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin) {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if !admin && a.Me.Audience != "portal" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if path == pacingPath {
		return h.agentPacing(r, a, request)
	}
	if path == shedPath {
		return h.agentShed(r, a, request)
	}
	if path == tasksPath || strings.HasPrefix(path, tasksPath+"/") {
		return h.adminTasks(r, a)
	}
	tokens := "/api/v1/admin/node-join-tokens"
	if path == tokens {
		if r.Method == "GET" {
			out, e := h.Service.Tokens(r.Context(), a, r.URL.Query())
			return out, 200, e
		}
		if r.Method == "POST" {
			var input struct {
				WorkspaceID      string `json:"workspace_id"`
				NodeName         string `json:"node_name"`
				ExecutionProfile string `json:"execution_profile"`
			}
			if e := httpserver.DecodeJSON(r, &input); e != nil {
				return nil, 0, e
			}
			if !id(input.WorkspaceID) {
				return nil, 0, bad
			}
			out, e := h.Service.MintForProfile(r.Context(), a, input.WorkspaceID, input.NodeName, input.ExecutionProfile, request)
			return out, 201, e
		}
	}
	if strings.HasPrefix(path, tokens+"/") {
		parts := strings.Split(strings.TrimPrefix(path, tokens+"/"), "/")
		if len(parts) == 2 && id(parts[0]) && parts[1] == "revocations" && r.Method == "POST" {
			var input struct {
				Reason string `json:"reason"`
			}
			if e := httpserver.DecodeJSON(r, &input); e != nil {
				return nil, 0, e
			}
			if !reason(input.Reason) {
				return nil, 0, bad
			}
			out, e := h.Service.RevokeToken(r.Context(), a, parts[0], input.Reason, request)
			return out, 200, e
		}
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	workspace := ""
	parts := []string{}
	if admin {
		parts = strings.Split(strings.TrimPrefix(path, "/api/v1/admin/nodes"), "/")
		if len(parts) > 0 && parts[0] == "" {
			parts = parts[1:]
		}
	} else {
		all := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		if len(all) < 2 || !id(all[0]) || all[1] != "nodes" {
			return nil, 0, bad
		}
		workspace = all[0]
		parts = all[2:]
	}
	if len(parts) == 0 && r.Method == "GET" {
		out, e := h.Service.List(r.Context(), a, workspace, r.URL.Query())
		return out, 200, e
	}
	if admin && len(parts) == 1 && parts[0] == "summary" && r.Method == "GET" {
		out, e := h.Service.Counts(r.Context(), a)
		return out, 200, e
	}
	if len(parts) < 1 || !id(parts[0]) {
		return nil, 0, bad
	}
	node := parts[0]
	if len(parts) == 1 {
		if r.Method == "GET" {
			out, e := h.Service.Get(r.Context(), a, workspace, node)
			return out, 200, e
		}
		if admin && r.Method == "PATCH" {
			var input struct {
				Name    string `json:"name"`
				Status  string `json:"status"`
				Version int64  `json:"version"`
				Reason  string `json:"reason"`
			}
			if e := httpserver.DecodeJSON(r, &input); e != nil {
				return nil, 0, e
			}
			if !workspaces.ValidVersion(input.Version) || !workspaces.ValidReason(input.Reason) {
				return nil, 0, bad
			}
			out, e := h.Service.Update(r.Context(), a, node, input.Name, input.Status, input.Version, input.Reason, request)
			return out, 200, e
		}
	}
	if len(parts) == 2 && parts[1] == "tasks" {
		if r.Method == "GET" {
			out, e := h.Service.Tasks(r.Context(), a, workspace, node, r.URL.Query())
			return out, 200, e
		}
		if admin && r.Method == "POST" {
			var input struct {
				Operation string `json:"operation"`
			}
			if e := httpserver.DecodeJSON(r, &input); e != nil {
				return nil, 0, e
			}
			keys := r.Header.Values("Idempotency-Key")
			if len(keys) != 1 || !id(keys[0]) {
				return nil, 0, bad
			}
			out, e := h.Service.CreateTask(r.Context(), a, node, input.Operation, keys[0], request)
			return out, 202, e
		}
	}
	return nil, 0, errors.New("RESOURCE_NOT_FOUND")
}
