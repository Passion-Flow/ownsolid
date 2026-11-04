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
	"regexp"
	"strconv"
	"strings"
	"syscall"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

var trainingTaskBase = regexp.MustCompile(`^/agent/v1/nodes/([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})/tasks/([a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})$`)
var trainingPath = regexp.MustCompile(`^/agent/v1/nodes/[a-f0-9-]{36}/tasks/[a-f0-9-]{36}/(training-input-files|training-events|training-stops|training-uploads(/[a-f0-9-]{36}/(chunks|finish))?)$`)
var safeTrainingCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,79}$`)

func trainingDigest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }
func trainingProblem(response *http.Response, b []byte) error {
	var p Problem
	if json.Unmarshal(b, &p) != nil || !safeTrainingCode.MatchString(p.Code) {
		return errors.New("CONTROL_RESPONSE_REJECTED")
	}
	p.Status = response.StatusCode
	return &p
}
func (c *Client) trainingSend(ctx context.Context, path, media string, body []byte, headers map[string]string) (*http.Response, error) {
	if !trainingPath.MatchString(path) {
		return nil, errors.New("AGENT_PATH_INVALID")
	}
	r, e := http.NewRequestWithContext(ctx, "POST", c.target(path), bytes.NewReader(body))
	if e != nil {
		return nil, errors.New("AGENT_REQUEST_INVALID")
	}
	r.Header.Set("Content-Type", media)
	r.Header.Set("Authorization", "Bearer "+c.Credential)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	response, e := c.HTTP.Do(r)
	if e != nil {
		return nil, errors.New("CONTROL_CONNECTION_FAILED")
	}
	return response, nil
}
func (c *Client) trainingJSON(ctx context.Context, path string, body []byte, maximum int, out any) error {
	if len(body) > maximum {
		return errors.New("AGENT_PAYLOAD_INVALID")
	}
	response, e := c.trainingSend(ctx, path, "application/json", body, nil)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	b, e := io.ReadAll(io.LimitReader(response.Body, wire.MaximumUploadSubmissionBytes+1))
	if e != nil || len(b) > wire.MaximumUploadSubmissionBytes {
		return errors.New("CONTROL_RESPONSE_INVALID")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return trainingProblem(response, b)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &envelope) != nil || len(envelope.Data) == 0 || wire.DecodeStrict(envelope.Data, out) != nil {
		return errors.New("CONTROL_RESPONSE_INVALID")
	}
	return nil
}

// TrainingFile writes a new private file and retains incomplete bytes after failure.
// The manager passes only a descriptor already selected from its immutable command/catalog.
func (c *Client) TrainingFile(ctx context.Context, base, lease, kind string, asset datawire.Asset, destination string) error {
	if !trainingTaskBase.MatchString(base) || asset.SizeBytes < 1 || asset.SizeBytes > wire.MaximumCheckpointBytes || !datawire.ValidDigest(asset.SHA256) {
		return errors.New("TRAINING_TRANSFER_INVALID")
	}
	if kind == "model" {
		approved, ok := agentwire.ModelFileForName(asset.Path)
		if !ok || approved.SizeBytes != asset.SizeBytes || approved.SHA256 != asset.SHA256 {
			return errors.New("TRAINING_INPUT_INVALID")
		}
	}
	first := wire.InputFileRequest{LeaseToken: lease, Kind: kind, Asset: asset.Path, Length: min(wire.TransferChunkBytes, asset.SizeBytes)}
	if first.ValidateAsset(asset) != nil {
		return errors.New("TRAINING_TRANSFER_INVALID")
	}
	f, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return errors.New("TRAINING_AGENT_IO_FAILED")
	}
	defer f.Close()
	hash := sha256.New()
	for offset := int64(0); offset < asset.SizeBytes; {
		input := first
		input.Offset = offset
		input.Length = min(wire.TransferChunkBytes, asset.SizeBytes-offset)
		body, _ := json.Marshal(input)
		response, e := c.trainingSend(ctx, base+"/training-input-files", "application/json", body, nil)
		if e != nil {
			return e
		}
		if response.StatusCode != 200 {
			b, _ := io.ReadAll(io.LimitReader(response.Body, 65537))
			response.Body.Close()
			return trainingProblem(response, b)
		}
		if response.ContentLength != input.Length || response.Header.Get("Content-Type") != "application/octet-stream" {
			response.Body.Close()
			return errors.New("CONTROL_RESPONSE_INVALID")
		}
		b, e := io.ReadAll(io.LimitReader(response.Body, input.Length+1))
		response.Body.Close()
		if e != nil || int64(len(b)) != input.Length || response.Header.Get("X-Artifact-SHA256") != trainingDigest(b) {
			return errors.New("CONTROL_RESPONSE_INVALID")
		}
		if _, e = io.Copy(io.MultiWriter(f, hash), bytes.NewReader(b)); e != nil {
			return errors.New("TRAINING_AGENT_IO_FAILED")
		}
		offset += input.Length
	}
	if hex.EncodeToString(hash.Sum(nil)) != asset.SHA256 {
		return errors.New("TRAINING_INPUT_INVALID")
	}
	if f.Sync() != nil {
		return errors.New("TRAINING_AGENT_IO_FAILED")
	}
	return nil
}
func (c *Client) TrainingEvents(ctx context.Context, base, lease string, command wire.Command, events [][]byte) ([]wire.EventAcceptance, error) {
	if !trainingTaskBase.MatchString(base) || command.Validate() != nil || len(events) < 1 || len(events) > wire.MaximumEventBatch {
		return nil, errors.New("TRAINING_EVENT_INVALID")
	}
	values := make([]wire.Event, len(events))
	for i, raw := range events {
		event, e := wire.DecodeEvent(raw, command.Scope.JobID, command.AttemptID, command.Configuration.MaximumSteps)
		if e != nil {
			return nil, e
		}
		values[i] = event
	}
	body, _ := json.Marshal(wire.EventSubmission{LeaseToken: lease, Events: values})
	var out struct {
		Items []wire.EventAcceptance `json:"items"`
	}
	if e := c.trainingJSON(ctx, base+"/training-events", body, wire.MaximumEventBatch*wire.MaximumEventBytes+4096, &out); e != nil {
		return nil, e
	}
	if len(out.Items) != len(values) {
		return nil, errors.New("CONTROL_RESPONSE_INVALID")
	}
	for i, v := range out.Items {
		if v.JobID != command.Scope.JobID || v.AttemptID != command.AttemptID || v.Sequence != values[i].Sequence {
			return nil, errors.New("CONTROL_RESPONSE_INVALID")
		}
	}
	return out.Items, nil
}
func (c *Client) BeginTrainingUpload(ctx context.Context, base, lease, kind string, metadata []byte) (wire.Upload, error) {
	var out wire.Upload
	if !trainingTaskBase.MatchString(base) || !bytes.Equal(bytes.TrimSpace(metadata), metadata) {
		return out, errors.New("TRAINING_UPLOAD_INVALID")
	}
	token, _ := json.Marshal(lease)
	category, _ := json.Marshal(kind)
	body := append([]byte(`{"lease_token":`+string(token)+`,"kind":`+string(category)+`,"metadata":`), metadata...)
	body = append(body, '}')
	if _, e := wire.DecodeUploadSubmission(body); e != nil {
		return out, e
	}
	if e := c.trainingJSON(ctx, base+"/training-uploads", body, wire.MaximumUploadSubmissionBytes, &out); e != nil {
		return out, e
	}
	if !datawire.ValidID(out.ID) || out.Kind != kind || out.ManifestSHA256 != trainingDigest(metadata) || out.ChunkBytes != wire.TransferChunkBytes || len(out.Files) < 1 || len(out.Files) > wire.MaximumCheckpointFiles {
		return wire.Upload{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
func (c *Client) TrainingChunk(ctx context.Context, base, lease, upload string, input wire.ChunkRequest, body []byte) (wire.ChunkAcceptance, error) {
	var out wire.ChunkAcceptance
	if !trainingTaskBase.MatchString(base) || !datawire.ValidID(upload) || input.Validate() != nil || int64(len(body)) != input.Length || trainingDigest(body) != input.SHA256 {
		return out, errors.New("TRAINING_UPLOAD_INVALID")
	}
	headers := map[string]string{"X-Training-Lease-Token": lease, "X-Training-Asset": input.Asset, "X-Training-SHA256": input.SHA256, "X-Training-Offset": strconv.FormatInt(input.Offset, 10)}
	response, e := c.trainingSend(ctx, base+"/training-uploads/"+upload+"/chunks", "application/octet-stream", body, headers)
	if e != nil {
		return out, e
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(raw) > 65536 {
		return out, errors.New("CONTROL_RESPONSE_INVALID")
	}
	if response.StatusCode != 200 {
		return out, trainingProblem(response, raw)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil || wire.DecodeStrict(envelope.Data, &out) != nil || out.UploadID != upload || out.Asset != input.Asset || out.Offset != input.Offset || out.SizeBytes != input.Length || out.SHA256 != input.SHA256 {
		return wire.ChunkAcceptance{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
func (c *Client) FinishTrainingUpload(ctx context.Context, base, lease string, upload wire.Upload) (wire.OutputAcceptance, error) {
	var out wire.OutputAcceptance
	if !trainingTaskBase.MatchString(base) || !datawire.ValidID(upload.ID) {
		return out, errors.New("TRAINING_UPLOAD_INVALID")
	}
	body, _ := json.Marshal(agentwire.LeaseRenewal{LeaseToken: lease})
	if e := c.trainingJSON(ctx, base+"/training-uploads/"+upload.ID+"/finish", body, 4096, &out); e != nil {
		return out, e
	}
	if out.UploadID != upload.ID || !datawire.ValidID(out.ReferenceID) || out.Kind != upload.Kind || out.GlobalStep != upload.GlobalStep {
		return wire.OutputAcceptance{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
func (c *Client) TrainingStop(ctx context.Context, base, lease string, command wire.Command, receipt wire.StopReceipt, outcome []byte) (wire.StopAcceptance, error) {
	var out wire.StopAcceptance
	matches := trainingTaskBase.FindStringSubmatch(base)
	if len(matches) != 3 || receipt.ValidateFor(command, matches[1], matches[2]) != nil {
		return out, errors.New("TRAINING_STOP_INVALID")
	}
	if len(outcome) == 0 {
		if receipt.OutcomeSHA256 != nil {
			return out, errors.New("TRAINING_STOP_INVALID")
		}
		outcome = []byte("null")
	} else {
		if receipt.OutcomeSHA256 == nil || *receipt.OutcomeSHA256 != trainingDigest(outcome) || !bytes.Equal(bytes.TrimSpace(outcome), outcome) {
			return out, errors.New("TRAINING_STOP_INVALID")
		}
		request, e := command.ContainerRequest()
		if e != nil {
			return out, e
		}
		if _, e = wire.DecodeResult(outcome, request, agentwire.ApprovedModel().Repository); e != nil {
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
	if e := c.trainingJSON(ctx, base+"/training-stops", body, wire.MaximumStopSubmissionBytes, &out); e != nil {
		return out, e
	}
	if out.JobID != command.Scope.JobID || out.AttemptID != command.AttemptID || out.ReceiptID != receipt.ReceiptID || !out.GPUReleased || !strings.Contains("|succeeded|failed|cancelled|", "|"+out.State+"|") {
		return wire.StopAcceptance{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return out, nil
}
