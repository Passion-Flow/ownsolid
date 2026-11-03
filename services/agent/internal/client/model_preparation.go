package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

var preparationTaskBase = regexp.MustCompile(`^/agent/v1/nodes/([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})/tasks/([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})$`)
var preparationPath = regexp.MustCompile(`^/agent/v1/nodes/[a-f0-9-]{36}/tasks/[a-f0-9-]{36}/(model-preparation-input-files|model-preparation-events|model-preparation-stops|model-preparation-uploads(/[a-f0-9-]{36}/(chunks|finish))?)$`)
var safePreparationCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`)

// ConfigurePreparationOutputs is restart-required and called before concurrent requests.
// A separate bounded transport permits long independent verification and lease renewal.
func (c *Client) ConfigurePreparationOutputs(seconds int) error {
	if seconds < 1 || seconds > 3600 || c.preparationHTTP != nil {
		return errors.New("MODEL_PREPARATION_AGENT_CONFIGURATION_INVALID")
	}
	transport, ok := c.HTTP.Transport.(*http.Transport)
	if !ok {
		return errors.New("MODEL_PREPARATION_AGENT_CONFIGURATION_INVALID")
	}
	cloned := transport.Clone()
	cloned.ResponseHeaderTimeout = time.Duration(seconds) * time.Second
	copied := *c.HTTP
	copied.Transport = cloned
	copied.Timeout = time.Duration(seconds) * time.Second
	c.preparationHTTP = &copied
	return nil
}
func preparationDigest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func preparationProblem(response *http.Response, b []byte) error {
	var p Problem
	if json.Unmarshal(b, &p) != nil || !safePreparationCode.MatchString(p.Code) {
		return errors.New("CONTROL_RESPONSE_REJECTED")
	}
	p.Status = response.StatusCode
	return &p
}
func (c *Client) preparationSend(ctx context.Context, path, media string, body []byte, headers map[string]string) (*http.Response, error) {
	if !preparationPath.MatchString(path) {
		return nil, errors.New("AGENT_PATH_INVALID")
	}
	r, e := http.NewRequestWithContext(ctx, "POST", c.Origin+path, bytes.NewReader(body))
	if e != nil {
		return nil, errors.New("AGENT_REQUEST_INVALID")
	}
	r.Header.Set("Content-Type", media)
	r.Header.Set("Authorization", "Bearer "+c.Credential)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	connection := c.HTTP
	if c.preparationHTTP != nil && (strings.HasSuffix(path, "/model-preparation-uploads") || strings.HasSuffix(path, "/finish") || strings.HasSuffix(path, "/model-preparation-stops")) {
		connection = c.preparationHTTP
	}
	response, e := connection.Do(r)
	if e != nil {
		return nil, errors.New("CONTROL_CONNECTION_FAILED")
	}
	return response, nil
}
func (c *Client) preparationJSON(ctx context.Context, path string, body []byte, maximum int64, out any) error {
	if int64(len(body)) > maximum {
		return errors.New("AGENT_PAYLOAD_INVALID")
	}
	response, e := c.preparationSend(ctx, path, "application/json", body, nil)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, wire.MaximumUploadSubmissionBytes+1))
	if e != nil || int64(len(b)) > wire.MaximumUploadSubmissionBytes {
		return errors.New("CONTROL_RESPONSE_INVALID")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return preparationProblem(response, b)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &envelope) != nil || len(envelope.Data) == 0 || wire.DecodeStrict(envelope.Data, out) != nil {
		return errors.New("CONTROL_RESPONSE_INVALID")
	}
	return nil
}

// PreparationFile writes a new private file and retains incomplete bytes after failure.
// The manager passes only a descriptor already selected from its immutable command/catalog.
func (c *Client) PreparationFile(ctx context.Context, base, lease, kind string, asset datawire.Asset, destination string) error {
	if !preparationTaskBase.MatchString(base) || asset.SizeBytes < 1 || asset.SizeBytes > wire.MaximumModelBytes || !datawire.ValidDigest(asset.SHA256) {
		return errors.New("MODEL_PREPARATION_TRANSFER_INVALID")
	}
	if kind == "model" {
		approved, ok := agentwire.ModelFileForName(asset.Path)
		if !ok || approved.SizeBytes != asset.SizeBytes || approved.SHA256 != asset.SHA256 {
			return errors.New("MODEL_PREPARATION_INPUT_INVALID")
		}
	}
	first := wire.InputFileRequest{LeaseToken: lease, Kind: kind, Asset: asset.Path, Length: min(wire.TransferChunkBytes, asset.SizeBytes)}
	if first.ValidateAsset(asset) != nil {
		return errors.New("MODEL_PREPARATION_TRANSFER_INVALID")
	}
	f, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	defer f.Close()
	hash := sha256.New()
	for offset := int64(0); offset < asset.SizeBytes; {
		input := first
		input.Offset = offset
		input.Length = min(wire.TransferChunkBytes, asset.SizeBytes-offset)
		body, _ := json.Marshal(input)
		response, e := c.preparationSend(ctx, base+"/model-preparation-input-files", "application/json", body, nil)
		if e != nil {
			return e
		}
		if response.StatusCode != 200 {
			b, _ := io.ReadAll(io.LimitReader(response.Body, 65537))
			response.Body.Close()
			return preparationProblem(response, b)
		}
		if response.ContentLength != input.Length || response.Header.Get("Content-Type") != "application/octet-stream" {
			response.Body.Close()
			return errors.New("CONTROL_RESPONSE_INVALID")
		}
		b, e := io.ReadAll(io.LimitReader(response.Body, input.Length+1))
		response.Body.Close()
		if e != nil || int64(len(b)) != input.Length || response.Header.Get("X-Artifact-SHA256") != preparationDigest(b) {
			return errors.New("CONTROL_RESPONSE_INVALID")
		}
		if _, e = io.Copy(io.MultiWriter(f, hash), bytes.NewReader(b)); e != nil {
			return errors.New("MODEL_PREPARATION_AGENT_IO_FAILED")
		}
		offset += input.Length
	}
	if hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return errors.New("MODEL_PREPARATION_INPUT_INVALID")
	}
	if f.Sync() != nil {
		return errors.New("MODEL_PREPARATION_AGENT_IO_FAILED")
	}
	return nil
}
func (c *Client) PreparationEvents(ctx context.Context, base, lease string, command wire.Command, rows int64, events [][]byte) ([]wire.EventAcceptance, error) {
	if !preparationTaskBase.MatchString(base) || command.Validate() != nil || len(events) < 1 || len(events) > wire.MaximumEventBatch {
		return nil, errors.New("MODEL_PREPARATION_EVENT_INVALID")
	}
	values := make([]wire.Event, len(events))
	for i, raw := range events {
		event, e := wire.DecodeEvent(raw, command.Scope.PreparationID, command.AttemptID, rows)
		if e != nil {
			return nil, e
		}
		values[i] = event
	}
	body, _ := json.Marshal(wire.EventSubmission{LeaseToken: lease, Events: values})
	var out struct {
		Items []wire.EventAcceptance `json:"items"`
	}
	if e := c.preparationJSON(ctx, base+"/model-preparation-events", body, int64(wire.MaximumEventBatch*wire.MaximumEventBytes+4096), &out); e != nil {
		return nil, e
	}
	if len(out.Items) != len(values) {
		return nil, errors.New("CONTROL_RESPONSE_INVALID")
	}
	for i, v := range out.Items {
		if v.PreparationID != command.Scope.PreparationID || v.AttemptID != command.AttemptID || v.Sequence != values[i].Sequence {
			return nil, errors.New("CONTROL_RESPONSE_INVALID")
		}
	}
	return out.Items, nil
}
func (c *Client) BeginPreparationUpload(ctx context.Context, base, lease string, command wire.Command, metadata []byte) (wire.Upload, error) {
	var out wire.Upload
	if !preparationTaskBase.MatchString(base) || command.Validate() != nil || !bytes.Equal(bytes.TrimSpace(metadata), metadata) {
		return out, errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	var expected struct {
		PreparationID string           `json:"preparation_id"`
		AttemptID     string           `json:"attempt_id"`
		Files         []datawire.Asset `json:"model_files"`
	}
	if json.Unmarshal(metadata, &expected) != nil || expected.PreparationID != command.Scope.PreparationID || expected.AttemptID != command.AttemptID || len(expected.Files) != len(wire.ApprovedBaseFiles()) {
		return out, errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	seen := map[string]bool{}
	var total int64
	for _, asset := range expected.Files {
		if !wire.ModelAssetAllowed(asset.Path) || seen[asset.Path] || asset.SizeBytes < 1 || asset.SizeBytes > command.Recipe.Resources.MaximumOutputBytes || !datawire.ValidDigest(asset.SHA256) {
			return out, errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
		}
		seen[asset.Path] = true
		total += asset.SizeBytes
	}
	if total > command.Recipe.Resources.MaximumOutputBytes || int64(len(expected.Files)) > command.Recipe.Resources.MaximumOutputFiles {
		return out, errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	token, _ := json.Marshal(lease)
	body := append([]byte(`{"lease_token":`+string(token)+`,"metadata":`), metadata...)
	body = append(body, '}')
	if _, e := wire.DecodeUploadSubmission(body); e != nil {
		return out, e
	}
	if e := c.preparationJSON(ctx, base+"/model-preparation-uploads", body, wire.MaximumUploadSubmissionBytes, &out); e != nil {
		return out, e
	}
	if !datawire.ValidID(out.ID) || out.PreparationID != command.Scope.PreparationID || out.AttemptID != command.AttemptID || out.ManifestSHA256 != preparationDigest(metadata) || out.ChunkBytes != wire.TransferChunkBytes || len(out.Files) < 1 || int64(len(out.Files)) > wire.MaximumModelFiles || !reflect.DeepEqual(out.Files, expected.Files) || out.ReceivedBytes < 0 || out.ReceivedBytes > total || (out.State != "uploading" && out.State != "verifying" && out.State != "accepted") {
		return wire.Upload{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
func (c *Client) PreparationChunk(ctx context.Context, base, lease, upload string, input wire.ChunkRequest, body []byte) (wire.ChunkAcceptance, error) {
	var out wire.ChunkAcceptance
	if !preparationTaskBase.MatchString(base) || !datawire.ValidID(upload) || input.Validate() != nil || int64(len(body)) != input.Length || preparationDigest(body) != input.SHA256 {
		return out, errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	headers := map[string]string{"X-Model-Preparation-Lease-Token": lease, "X-Model-Preparation-Asset": input.Asset, "X-Model-Preparation-SHA256": input.SHA256, "X-Model-Preparation-Offset": strconv.FormatInt(input.Offset, 10)}
	response, e := c.preparationSend(ctx, base+"/model-preparation-uploads/"+upload+"/chunks", "application/octet-stream", body, headers)
	if e != nil {
		return out, e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(raw) > 65536 {
		return out, errors.New("CONTROL_RESPONSE_INVALID")
	}
	if response.StatusCode != 200 {
		return out, preparationProblem(response, raw)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || wire.DecodeStrict(envelope.Data, &out) != nil || out.UploadID != upload || out.Asset != input.Asset || out.Offset != input.Offset || out.SizeBytes != input.Length || out.SHA256 != input.SHA256 {
		return wire.ChunkAcceptance{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
func (c *Client) FinishPreparationUpload(ctx context.Context, base, lease string, upload wire.Upload) (wire.OutputAcceptance, error) {
	var out wire.OutputAcceptance
	if !preparationTaskBase.MatchString(base) || !datawire.ValidID(upload.ID) {
		return out, errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
	}
	body, _ := json.Marshal(agentwire.LeaseRenewal{LeaseToken: lease})
	if e := c.preparationJSON(ctx, base+"/model-preparation-uploads/"+upload.ID+"/finish", body, 4096, &out); e != nil {
		return out, e
	}
	if out.UploadID != upload.ID || out.PreparationID != upload.PreparationID || out.AttemptID != upload.AttemptID || out.ManifestSHA256 != upload.ManifestSHA256 {
		return wire.OutputAcceptance{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
func (c *Client) PreparationStop(ctx context.Context, base, lease string, command wire.Command, receipt wire.StopReceipt, outcome []byte) (wire.StopAcceptance, error) {
	var out wire.StopAcceptance
	matches := preparationTaskBase.FindStringSubmatch(base)
	if len(matches) != 3 || receipt.ValidateFor(command, matches[1], matches[2]) != nil {
		return out, errors.New("MODEL_PREPARATION_STOP_INVALID")
	}
	if len(outcome) == 0 {
		outcome = []byte("null")
	} else {
		if receipt.OutcomeSHA256 == nil || *receipt.OutcomeSHA256 != preparationDigest(outcome) || !bytes.Equal(bytes.TrimSpace(outcome), outcome) {
			return out, errors.New("MODEL_PREPARATION_STOP_INVALID")
		}
		request, e := command.ContainerRequest()
		if e != nil {
			return out, e
		}
		var status struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(outcome, &status) != nil {
			return out, errors.New("MODEL_PREPARATION_REPORT_INVALID")
		}
		if status.Status == "prepared" {
			var report wire.PreparedResult
			if wire.DecodeStrict(outcome, &report) != nil || report.PreparationID != command.Scope.PreparationID || report.AttemptID != command.AttemptID || report.ConfigurationSHA256 != command.ConfigurationSHA256 {
				return out, errors.New("MODEL_PREPARATION_REPORT_INVALID")
			}
		} else if _, e = wire.DecodeFailedResult(outcome, request); e != nil {
			return out, e
		}
	}
	token, _ := json.Marshal(lease)
	proof, _ := json.Marshal(receipt)
	body := append([]byte(`{"lease_token":`+string(token)+`,"receipt":`+string(proof)+`,"outcome":`), outcome...)
	body = append(body, '}')
	if _, e := wire.DecodeStopSubmission(body); e != nil {
		return out, e
	}
	if e := c.preparationJSON(ctx, base+"/model-preparation-stops", body, wire.MaximumStopSubmissionBytes, &out); e != nil {
		return out, e
	}
	if out.PreparationID != command.Scope.PreparationID || out.AttemptID != command.AttemptID || out.ReceiptID != receipt.ReceiptID || !out.GPUReleased || !strings.Contains("|quality_failed|awaiting_review|approved|rejected|failed|cancelled|", "|"+out.State+"|") {
		return wire.StopAcceptance{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
