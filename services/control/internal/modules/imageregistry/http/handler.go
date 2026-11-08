// Package http exposes closed registry routes to the existing authenticated
// Origin/CSRF/License entrance. Parent wiring must run that original middleware.
package http

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/deployments"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentidentity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

const adminRoot = "/api/v1/admin/image-registry"

type Handler struct {
	Service *imageregistry.Service
	// CheckPeer decides whether a connection's certificate may act for a node (ADR 0050).
	CheckPeer func(ctx context.Context, node string, peer agentidentity.Peer) error
}

func Owns(path string) bool {
	if path == adminRoot || strings.HasPrefix(path, adminRoot+"/") {
		return true
	}
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
	return strings.HasPrefix(path, "/api/v1/workspaces/") && len(parts) >= 3 && parts[1] == "image-registry"
}
func (h *Handler) Handle(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	missing := errors.New("RESOURCE_NOT_FOUND")
	if h == nil || h.Service == nil {
		return nil, 0, errors.New("REGISTRY_OWNER_REQUIRED")
	}
	if r.URL.RawPath != "" {
		return nil, 0, bad
	}
	query, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return nil, 0, bad
	}
	admin := strings.HasPrefix(r.URL.Path, adminRoot)
	workspace := query.Get("workspace_id")
	path := strings.TrimPrefix(r.URL.Path, adminRoot)
	if !admin {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/"), "/")
		if len(parts) != 3 || parts[1] != "image-registry" || parts[2] != "pins" || r.Method != http.MethodGet || a.Me.Audience != "portal" {
			return nil, 0, missing
		}
		workspace = parts[0]
		if len(query) > 1 || len(query["cursor"]) > 1 || len(query) == 1 && query["cursor"] == nil {
			return nil, 0, bad
		}
		out, e := h.Service.List(r.Context(), a, workspace, query.Get("cursor"))
		return out, 200, e
	}
	if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
		return nil, 0, errors.New("ACCESS_DENIED")
	}
	if len(query["workspace_id"]) != 1 || len(query["cursor"]) > 1 || len(query) > 2 {
		return nil, 0, bad
	}
	for k := range query {
		if k != "workspace_id" && k != "cursor" {
			return nil, 0, bad
		}
	}
	if r.Method == http.MethodGet {
		if path == "/pins" {
			out, e := h.Service.List(r.Context(), a, workspace, query.Get("cursor"))
			return out, 200, e
		}
		if query.Get("cursor") != "" {
			return nil, 0, bad
		}
		if path == "/release-images" {
			out, e := h.Service.ReleaseImages(r.Context(), a, workspace)
			return out, 200, e
		}
		parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
		if len(parts) == 2 && parts[0] == "pins" {
			out, e := h.Service.Get(r.Context(), a, workspace, parts[1])
			return out, 200, e
		}
		if len(parts) == 3 && parts[0] == "pins" && parts[2] == "references" {
			out, e := h.Service.References(r.Context(), a, workspace, parts[1])
			return out, 200, e
		}
		return nil, 0, missing
	}
	if r.Method != http.MethodPost || query.Get("cursor") != "" {
		return nil, 0, missing
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 {
		return nil, 0, bad
	}
	m := imageregistry.Mutation{WorkspaceID: workspace, Key: keys[0], RequestID: request}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if path == "/pins" {
		var in struct {
			AcknowledgeLicenses bool   `json:"acknowledge_licenses"`
			AssetID             string `json:"asset_id"`
			Reason              string `json:"reason"`
		}
		if e = httpserver.DecodeJSON(r, &in); e != nil {
			return nil, 0, e
		}
		m.Reason = in.Reason
		out, e := h.Service.Import(r.Context(), a, m, in.AssetID, in.AcknowledgeLicenses)
		return out, 201, e
	}
	if len(parts) < 3 || parts[0] != "pins" {
		return nil, 0, missing
	}
	pin := parts[1]
	action := parts[2]
	if action == "deployments" && len(parts) == 3 {
		var in deployments.CreateRequest
		if e = httpserver.DecodeJSON(r, &in); e != nil {
			return nil, 0, e
		}
		m.Reason = in.Reason
		out, e := h.Service.CreateDeployment(r.Context(), a, m, pin, in)
		return out, 201, e
	}
	if action == "offline" && len(parts) == 5 && (parts[4] == "manifest" || parts[4] == "ranges") {
		// POST range reads carry explicit current Admin CSRF but create no mutation.
		if parts[4] == "manifest" {
			var in struct{}
			if e = httpserver.DecodeJSON(r, &in); e != nil {
				return nil, 0, e
			}
			out, e := h.Service.OfflineManifest(r.Context(), a, workspace, pin, parts[3])
			return out, 200, e
		}
		var in struct {
			Offset int64 `json:"offset"`
			Length int64 `json:"length"`
		}
		if e = httpserver.DecodeJSON(r, &in); e != nil {
			return nil, 0, e
		}
		raw, sha, e := h.Service.OfflineRange(r.Context(), a, workspace, pin, parts[3], in.Offset, in.Length)
		if e != nil {
			return nil, 0, e
		}
		return httpserver.MemoryDownload{Bytes: raw, SHA256: strings.TrimPrefix(sha, "sha256:"), ContentType: "application/octet-stream", FileName: "registry-range.bin"}, 200, nil
	}
	var in struct {
		Reason          string `json:"reason"`
		RetryAbsent     bool   `json:"retry_absent,omitempty"`
		ReferenceID     string `json:"reference_id,omitempty"`
		InventorySHA256 string `json:"inventory_sha256,omitempty"`
		DeploymentID    string `json:"deployment_id,omitempty"`
	}
	if e = httpserver.DecodeJSON(r, &in); e != nil {
		return nil, 0, e
	}
	m.Reason = in.Reason
	if len(parts) != 3 {
		return nil, 0, missing
	}
	switch action {
	case "grant":
		out, e := h.Service.Grant(r.Context(), a, m, pin, in.DeploymentID)
		return struct {
			ID string `json:"id"`
		}{out}, 201, e
	case "abandon":
		out, e := h.Service.AbandonPrepared(r.Context(), a, m, pin)
		return out, 200, e
	case "discard":
		e := h.Service.DiscardFailed(r.Context(), a, m, pin, in.InventorySHA256)
		return struct{}{}, 200, e
	case "reconcile":
		out, e := h.Service.Reconcile(r.Context(), a, m, pin, in.RetryAbsent)
		return out, 200, e
	case "remove":
		out, e := h.Service.Remove(r.Context(), a, m, pin)
		return out, 200, e
	case "reconcile-removal":
		out, e := h.Service.ReconcileRemoval(r.Context(), a, m, pin)
		return out, 200, e
	case "revoke":
		e := h.Service.Revoke(r.Context(), a, m, pin)
		return struct{}{}, 200, e
	case "offline":
		out, e := h.Service.CreateOfflineReference(r.Context(), a, m, pin)
		return struct {
			ID string `json:"id"`
		}{out}, 201, e
	case "release-offline":
		e := h.Service.ReleaseOfflineReference(r.Context(), a, m, pin, in.ReferenceID)
		return struct{}{}, 200, e
	case "release-deployment":
		e := h.Service.ReleaseDeploymentReference(r.Context(), a, m, pin, in.DeploymentID)
		return struct{}{}, 200, e
	}
	return nil, 0, missing
}
func MachineOwns(path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/agent/v1/"), "/")
	return strings.HasPrefix(path, "/agent/v1/") && len(parts) == 6 && parts[0] == "nodes" && parts[2] == "tasks" && parts[4] == "image-registry" && (parts[5] == "manifest" || parts[5] == "ranges")
}
func (h *Handler) Machine(r *http.Request) (any, int, error) {
	if h == nil || h.Service == nil || !MachineOwns(r.URL.Path) || r.Method != http.MethodPost || r.URL.RawPath != "" || r.URL.RawQuery != "" {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/agent/v1/"), "/")
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") || strings.Count(values[0], " ") != 1 {
		return nil, 0, errors.New("AGENT_UNAUTHORIZED")
	}
	credential := strings.TrimPrefix(values[0], "Bearer ")
	if !security.ValidToken(credential) {
		return nil, 0, errors.New("AGENT_UNAUTHORIZED")
	}
	if h.CheckPeer == nil {
		return nil, 0, errors.New("RESOURCE_NOT_FOUND")
	}
	if e := h.CheckPeer(r.Context(), parts[1], agentidentity.PeerOf(r.TLS)); e != nil {
		return nil, 0, e
	}
	if parts[5] == "manifest" {
		var in wire.LeaseRenewal
		if e := httpserver.DecodeJSON(r, &in); e != nil {
			return nil, 0, e
		}
		out, e := h.Service.PullManifest(r.Context(), parts[1], credential, parts[3], in.LeaseToken)
		return out, 200, e
	}
	var in struct {
		Lease string `json:"lease_token"`
		wire.RegistryRange
	}
	if e := httpserver.DecodeJSON(r, &in); e != nil {
		return nil, 0, e
	}
	raw, sha, e := h.Service.PullRange(r.Context(), parts[1], credential, parts[3], in.Lease, in.RegistryRange)
	if e != nil {
		return nil, 0, e
	}
	return httpserver.MemoryDownload{Bytes: raw, SHA256: strings.TrimPrefix(sha, "sha256:"), FileName: "registry-range.bin", ContentType: "application/octet-stream"}, 200, nil
}
