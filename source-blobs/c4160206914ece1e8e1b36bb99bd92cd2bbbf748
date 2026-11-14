package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	lifecycle "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
)

func Owns(path string) bool {
	if strings.HasPrefix(path, "/api/v1/admin/distribution/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/workspaces/") {
		p := strings.Split(strings.TrimPrefix(path, "/api/v1/workspaces/"), "/")
		return len(p) >= 2 && p[1] == "distribution"
	}
	return false
}

// decode requires exact canonical field names, all declared fields, and the shared duplicate/null/size checks.
func decode(r *http.Request, v any) error {
	body, e := io.ReadAll(io.LimitReader(r.Body, httpserver.MaxJSONBytes+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if len(body) > httpserver.MaxJSONBytes {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil {
		return errors.New("VALIDATION_FAILED")
	}
	// Every field is required unless its tag says omitempty, and no other field is accepted.
	expected := reflect.TypeOf(v).Elem()
	known := map[string]bool{}
	for i := 0; i < expected.NumField(); i++ {
		name, options, _ := strings.Cut(expected.Field(i).Tag.Get("json"), ",")
		known[name] = true
		if _, ok := fields[name]; !ok && options != "omitempty" {
			return errors.New("VALIDATION_FAILED")
		}
	}
	for name := range fields {
		if !known[name] {
			return errors.New("VALIDATION_FAILED")
		}
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return httpserver.DecodeJSON(r, v)
}
func (h *Handler) Human(r *http.Request, a identity.Auth, request string) (any, int, error) {
	bad := errors.New("VALIDATION_FAILED")
	missing := errors.New("RESOURCE_NOT_FOUND")
	isAdmin := strings.HasPrefix(r.URL.Path, "/api/v1/admin/distribution/")
	var workspace string
	var parts []string
	if isAdmin {
		if a.Me.Audience != "admin" || !a.Me.User.IsPlatformAdmin {
			return nil, 0, errors.New("ACCESS_DENIED")
		}
		all := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/distribution/"), "/")
		if len(all) < 3 || all[0] != "workspaces" || !wire.ValidID(all[1]) {
			return nil, 0, bad
		}
		workspace = all[1]
		parts = all[2:]
	} else {
		if a.Me.Audience != "portal" {
			return nil, 0, missing
		}
		all := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/workspaces/"), "/")
		if len(all) < 3 || !wire.ValidID(all[0]) || all[1] != "distribution" {
			return nil, 0, bad
		}
		workspace = all[0]
		parts = all[2:]
	}
	if r.URL.RawPath != "" || len(parts) == 0 || r.Method != http.MethodGet && r.Method != http.MethodPost {
		return nil, 0, bad
	}
	if len(parts) == 1 && r.Method == http.MethodGet {
		switch parts[0] {
		case "gateways":
			if !isAdmin {
				return nil, 0, missing
			}
			v, e := h.Service.GatewayPage(r.Context(), a, workspace, r.URL.Query())
			return v, 200, e
		case "aliases":
			v, e := h.Service.ReadAliasPage(r.Context(), a, workspace, r.URL.Query())
			return v, 200, e
		case "keys":
			v, e := h.Service.KeyPage(r.Context(), a, workspace, r.URL.Query())
			return v, 200, e
		case "requests":
			v, e := h.Service.RequestPage(r.Context(), a, workspace, r.URL.Query())
			return v, 200, e
		default:
			return nil, 0, missing
		}
	}
	if len(parts) == 2 && parts[0] == "requests" && parts[1] == "export" && r.Method == http.MethodGet {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return nil, 0, bad
		}
		page, err := h.Service.ExportUsagePage(r.Context(), a, workspace, values)
		if err != nil {
			return nil, 0, err
		}
		return memoryAttachment(page.CSV, "gateway-usage.csv", "text/csv; charset=utf-8"), http.StatusOK, nil
	}
	if len(parts) == 3 && parts[0] == "aliases" && wire.ValidID(parts[1]) && parts[2] == "targets" && r.Method == http.MethodGet && isAdmin {
		v, e := h.Service.TargetHistory(r.Context(), a, workspace, parts[1], r.URL.Query())
		return v, 200, e
	}
	if r.URL.RawQuery != "" {
		return nil, 0, bad
	}
	intent := ""
	aliasWrite := len(parts) == 3 && parts[0] == "aliases" &&
		(parts[2] == "cutovers" || isAdmin && parts[2] == "description")
	if r.Method == http.MethodPost && (len(parts) == 1 || len(parts) == 3 && parts[0] == "requests" && parts[2] == "recoveries" || aliasWrite || len(parts) == 3 && parts[0] == "keys" && parts[2] == "rotate") {
		keys := r.Header.Values("Idempotency-Key")
		if len(keys) != 1 || !wire.ValidID(keys[0]) {
			return nil, 0, bad
		}
		intent = keys[0]
	}
	if len(parts) == 1 && r.Method == http.MethodPost {
		switch parts[0] {
		case "gateways":
			if !isAdmin {
				return nil, 0, missing
			}
			var input struct {
				NodeID string `json:"node_id"`
				Name   string `json:"name"`
				// The node declares where callers reach a gateway its agent runs.
				ClientOrigin string `json:"client_origin,omitempty"`
				Reason       string `json:"reason"`
			}
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Provision(r.Context(), a, distribution.ProvisionRequest{WorkspaceID: workspace, NodeID: input.NodeID, Name: input.Name, ClientOrigin: input.ClientOrigin, Reason: input.Reason}, intent, request)
			status := 200
			if v.Created {
				status = 201
			}
			return v, status, e
		case "aliases":
			if !isAdmin {
				return nil, 0, missing
			}
			var input struct {
				GatewayID          string `json:"gateway_id"`
				DeploymentID       string `json:"deployment_id"`
				DeploymentRevision int64  `json:"deployment_revision"`
				Name               string `json:"name"`
				Reason             string `json:"reason"`
				Description        string `json:"description,omitempty"`
			}
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.Publish(r.Context(), a, distribution.PublishRequest{WorkspaceID: workspace, GatewayID: input.GatewayID, DeploymentID: input.DeploymentID, DeploymentRevision: input.DeploymentRevision, Name: input.Name, Reason: input.Reason, Description: input.Description}, intent, request)
			return v, 200, e
		case "keys":
			var input struct {
				AliasID    string `json:"alias_id"`
				Label      string `json:"label"`
				TTLSeconds int    `json:"ttl_seconds"`
				Reason     string `json:"reason"`
			}
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.MintKey(r.Context(), a, distribution.KeyRequest{WorkspaceID: workspace, AliasID: input.AliasID, Label: input.Label, TTLSeconds: input.TTLSeconds, Reason: input.Reason}, intent, request)
			status := 200
			if v.Created {
				status = 201
			}
			return v, status, e
		default:
			return nil, 0, missing
		}
	}
	if len(parts) < 2 || !wire.ValidID(parts[1]) {
		return nil, 0, bad
	}
	id := parts[1]
	if len(parts) == 3 && parts[0] == "aliases" && parts[2] == "ca" && r.Method == http.MethodGet {
		bundle, err := h.Service.AliasCertificateAuthority(r.Context(), a, workspace, id)
		if err != nil {
			return nil, 0, err
		}
		return memoryAttachment(bundle, "model-gateway-ca.pem", "application/octet-stream"), http.StatusOK, nil
	}
	if (len(parts) == 3 || len(parts) == 4 && parts[3] == "preview") &&
		parts[0] == "aliases" && parts[2] == "cutovers" && r.Method == http.MethodPost && isAdmin {
		var input distribution.CutoverRequest
		if e := decodeCutover(r, &input); e != nil {
			return nil, 0, e
		}
		if len(parts) == 4 {
			v, e := h.Service.PreviewCutover(r.Context(), a, workspace, id, input)
			return v, http.StatusOK, e
		}
		v, e := h.Service.Cutover(r.Context(), a, workspace, id, input, intent, request)
		return v, 200, e
	}
	if len(parts) == 2 && r.Method == http.MethodGet {
		switch parts[0] {
		case "gateways":
			if !isAdmin {
				return nil, 0, missing
			}
			v, e := h.Service.Gateway(r.Context(), a, workspace, id)
			return v, 200, e
		case "aliases":
			v, e := h.Service.ReadAlias(r.Context(), a, workspace, id)
			return v, 200, e
		case "keys":
			v, e := h.Service.Key(r.Context(), a, workspace, id)
			return v, 200, e
		case "requests":
			v, e := h.Service.Request(r.Context(), a, workspace, id)
			return v, 200, e
		default:
			return nil, 0, missing
		}
	}
	if len(parts) == 3 && r.Method == http.MethodPost {
		if parts[0] == "aliases" && parts[2] == "description" && isAdmin {
			var input distribution.AliasDescriptionEdit
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			result, e := h.Service.EditAliasDescription(r.Context(), a, workspace, id, input, intent, request)
			return result, http.StatusOK, e
		}
		if parts[0] == "keys" && parts[2] == "rotate" {
			var input distribution.RotateRequest
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.RotateKey(r.Context(), a, workspace, id, input, intent, request)
			status := 200
			if v.Created {
				status = 201
			}
			return v, status, e
		}
		if parts[0] == "aliases" && parts[2] == "disable" && isAdmin {
			var input struct {
				ExpectedRevision int64  `json:"expected_revision"`
				Reason           string `json:"reason"`
			}
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.DisableAlias(r.Context(), a, workspace, id, input.ExpectedRevision, input.Reason, request)
			return v, 200, e
		}
		var input struct {
			Reason string `json:"reason"`
		}
		if parts[0] == "gateways" && parts[2] == "revoke" && isAdmin {
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.RevokeGateway(r.Context(), a, workspace, id, input.Reason, request)
			return v, 200, e
		}
		if parts[0] == "keys" && parts[2] == "revoke" {
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.RevokeKey(r.Context(), a, workspace, id, input.Reason, request)
			return v, 200, e
		}
		if parts[0] == "requests" && parts[2] == "recoveries" && isAdmin {
			if e := decode(r, &input); e != nil {
				return nil, 0, e
			}
			v, e := h.Service.CreateRecoveryChallenge(r.Context(), a, workspace, id, input.Reason, intent, request)
			return v, 200, e
		}
	}
	if isAdmin && len(parts) >= 4 && parts[0] == "requests" && parts[2] == "recoveries" && wire.ValidID(parts[3]) {
		recovery := parts[3]
		if len(parts) == 4 && r.Method == http.MethodGet {
			v, e := h.Service.RecoveryChallenge(r.Context(), a, workspace, id, recovery)
			return v, 200, e
		}
		if len(parts) == 5 && r.Method == http.MethodPost {
			switch parts[4] {
			case "fence":
				var input struct {
					Receipt json.RawMessage `json:"receipt"`
					Reason  string          `json:"reason"`
				}
				if e := decode(r, &input); e != nil {
					return nil, 0, e
				}
				receipt, e := wire.DecodeFenceReceipt(input.Receipt)
				if e != nil {
					return nil, 0, bad
				}
				v, e := h.Service.AcceptRecoveryFence(r.Context(), a, workspace, id, recovery, receipt, input.Reason, request)
				return v, 200, e
			case "confirm":
				var input struct {
					DeploymentRevision int64  `json:"deployment_revision"`
					Reason             string `json:"reason"`
				}
				if e := decode(r, &input); e != nil {
					return nil, 0, e
				}
				v, e := h.Service.ConfirmRecovery(r.Context(), a, workspace, id, recovery, input.DeploymentRevision, input.Reason, request)
				return v, 200, e
			}
		}
	}
	return nil, 0, missing
}

func decodeCutover(r *http.Request, out *distribution.CutoverRequest) error {
	media, params, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(r.Header.Values("Content-Type")) != 1 || len(params) > 1 || len(params) == 1 && params["charset"] != "utf-8" {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, httpserver.MaxJSONBytes+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if len(raw) > httpserver.MaxJSONBytes {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	if lifecycle.DecodeStrict(raw, out) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}

func memoryAttachment(raw []byte, filename, contentType string) httpserver.MemoryDownload {
	digest := sha256.Sum256(raw)
	return httpserver.MemoryDownload{
		Bytes: raw, SHA256: hex.EncodeToString(digest[:]),
		FileName: filename, ContentType: contentType,
	}
}
