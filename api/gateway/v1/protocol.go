// Package v1 owns the metadata-only Go/Rust gateway authority and receipt protocol.
package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"unicode/utf8"
)

const MaxContextTokens uint32 = 65536
const MaxOutputTokens uint32 = 512
const MaxRequestSeconds uint64 = 300
const MaxOutcomeBytes = 4096
const MaxModelBindings = 16
const MaxMachineRequestBytes = 16384

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)
var modelPattern = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)

func ValidID(value string) bool {
	return uuidPattern.MatchString(value) && value != "00000000-0000-0000-0000-000000000000"
}
func ValidAlias(value string) bool { return aliasPattern.MatchString(value) }
func invalid() error               { return errors.New("GATEWAY_PROTOCOL_INVALID") }

type ModelsRequest struct {
	APIKey string `json:"api_key"`
}

func DecodeModelsRequest(b []byte) (ModelsRequest, error) {
	var out ModelsRequest
	if e := decodeRequest(b, &out, []string{"api_key"}); e != nil {
		return out, e
	}
	if len(out.APIKey) == 0 || len(out.APIKey) > 256 {
		return out, invalid()
	}
	return out, nil
}
func DecodeAdmissionRequest(b []byte) (AdmissionRequest, error) {
	var out AdmissionRequest
	if e := decodeRequest(b, &out, []string{"request_id", "api_key", "model", "maximum_output_tokens", "stream"}); e != nil {
		return out, e
	}
	return out, out.Validate()
}
func decodeRequest(b []byte, out any, required []string) error {
	if len(b) == 0 || len(b) > MaxMachineRequestBytes || !utf8.Valid(b) {
		return invalid()
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if unique(d) != nil {
		return invalid()
	}
	if _, e := d.Token(); e != io.EOF {
		return invalid()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || len(fields) != len(required) {
		return invalid()
	}
	for _, name := range required {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
	}
	d = json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return invalid()
	}
	return nil
}

type AdmissionRequest struct {
	RequestID           string `json:"request_id"`
	APIKey              string `json:"api_key"`
	Model               string `json:"model"`
	MaximumOutputTokens uint32 `json:"maximum_output_tokens"`
	Stream              bool   `json:"stream"`
}

func (r AdmissionRequest) Validate() error {
	if !ValidID(r.RequestID) || !ValidAlias(r.Model) || r.MaximumOutputTokens == 0 || r.MaximumOutputTokens > MaxOutputTokens || len(r.APIKey) == 0 || len(r.APIKey) > 256 {
		return invalid()
	}
	return nil
}

type AliasBinding struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Created            uint64 `json:"created"`
	DeploymentID       string `json:"deployment_id"`
	DeploymentRevision uint64 `json:"deployment_revision"`
	BackendModel       string `json:"backend_model"`
}

func (b AliasBinding) Validate() error {
	if !ValidID(b.ID) || !ValidID(b.DeploymentID) || !ValidAlias(b.Name) || b.DeploymentRevision == 0 || !modelPattern.MatchString(b.BackendModel) {
		return invalid()
	}
	return nil
}

type ModelsAuthorization struct {
	GatewayID   string         `json:"gateway_id"`
	NodeID      string         `json:"node_id"`
	WorkspaceID string         `json:"workspace_id"`
	Models      []AliasBinding `json:"models"`
}
type Admission struct {
	RequestID             string       `json:"request_id"`
	GatewayID             string       `json:"gateway_id"`
	NodeID                string       `json:"node_id"`
	WorkspaceID           string       `json:"workspace_id"`
	Binding               AliasBinding `json:"binding"`
	MaximumContextTokens  uint32       `json:"maximum_context_tokens"`
	MaximumRequestSeconds uint64       `json:"maximum_request_seconds"`
}
type Usage struct {
	PromptTokens     uint32 `json:"prompt_tokens"`
	CompletionTokens uint32 `json:"completion_tokens"`
	TotalTokens      uint32 `json:"total_tokens"`
}

// Field order and explicit nulls are the canonical receipt representation shared with Rust.
type Outcome struct {
	RequestID           string  `json:"request_id"`
	Status              string  `json:"status"`
	ErrorCode           *string `json:"error_code"`
	Usage               *Usage  `json:"usage"`
	ElapsedMilliseconds uint64  `json:"elapsed_milliseconds"`
	OutputStarted       bool    `json:"output_started"`
}

var statuses = map[string]bool{"succeeded": true, "client_cancelled": true, "upstream_timeout": true, "upstream_error": true, "invalid_response": true, "gateway_shutdown": true, "usage_storage_failed": true, "incomplete": true}
var errorCodes = map[string]bool{"client_cancelled": true, "context_length_exceeded": true, "upstream_unavailable": true, "upstream_timeout": true, "upstream_response_invalid": true, "gateway_unavailable": true, "gateway_shutdown": true, "usage_storage_unavailable": true, "gateway_interrupted": true}

func (o Outcome) Validate() error {
	if !ValidID(o.RequestID) || !statuses[o.Status] || o.ElapsedMilliseconds > 3600000 {
		return invalid()
	}
	if o.Status == "succeeded" {
		if o.ErrorCode != nil || o.Usage == nil {
			return invalid()
		}
	} else if o.ErrorCode == nil || !errorCodes[*o.ErrorCode] {
		return invalid()
	}
	if u := o.Usage; u != nil && (u.PromptTokens == 0 || u.TotalTokens > MaxContextTokens || u.CompletionTokens > MaxOutputTokens || uint64(u.PromptTokens)+uint64(u.CompletionTokens) != uint64(u.TotalTokens)) {
		return invalid()
	}
	return nil
}
func (o Outcome) Canonical() ([]byte, error) {
	if e := o.Validate(); e != nil {
		return nil, e
	}
	return json.Marshal(o)
}
func (o Outcome) Digest() (string, error) {
	b, e := o.Canonical()
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

type Acknowledgment struct {
	RequestID       string `json:"request_id"`
	CanonicalSHA256 string `json:"canonical_sha256"`
}

// DecodeOutcome permits only the two explicit nullable fields; the human JSON decoder remains stricter.
func DecodeOutcome(b []byte) (Outcome, error) {
	var out Outcome
	if len(b) == 0 || len(b) > MaxOutcomeBytes || !utf8.Valid(b) {
		return out, invalid()
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	if e := unique(decoder); e != nil {
		return out, invalid()
	}
	if _, e := decoder.Token(); e != io.EOF {
		return out, invalid()
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil {
		return out, invalid()
	}
	if len(fields) != 6 {
		return out, invalid()
	}
	for _, name := range []string{"request_id", "status", "error_code", "usage", "elapsed_milliseconds", "output_started"} {
		value, ok := fields[name]
		if !ok || (name != "error_code" && name != "usage" && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return out, invalid()
		}
	}
	if !bytes.Equal(bytes.TrimSpace(fields["usage"]), []byte("null")) {
		var usage map[string]json.RawMessage
		if json.Unmarshal(fields["usage"], &usage) != nil || len(usage) != 3 {
			return out, invalid()
		}
		for _, name := range []string{"prompt_tokens", "completion_tokens", "total_tokens"} {
			value, ok := usage[name]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return out, invalid()
			}
		}
	}
	strict := json.NewDecoder(bytes.NewReader(b))
	strict.DisallowUnknownFields()
	if strict.Decode(&out) != nil {
		return out, invalid()
	}
	return out, out.Validate()
}
func unique(d *json.Decoder) error {
	token, e := d.Token()
	if e != nil {
		return e
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		names := map[string]bool{}
		for d.More() {
			token, e := d.Token()
			if e != nil {
				return e
			}
			name, ok := token.(string)
			if !ok || names[name] {
				return invalid()
			}
			names[name] = true
			if e = unique(d); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = unique(d); e != nil {
				return e
			}
		}
	default:
		return invalid()
	}
	_, e = d.Token()
	return e
}
