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
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)

// modelRangeAttempts bounds how often one range is requested while control answers 503
// or refuses it with a retry hint.
// The task is not retried on top of this, so these attempts are the only retry layer.
const modelRangeAttempts = 3

var modelTaskPath = regexp.MustCompile(`^/agent/v1/nodes/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}/tasks/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}/model-files$`)

// ModelFile downloads compiled catalog bytes only, in separately fenced bounded ranges.
func (c *Client) ModelFile(ctx context.Context, path, lease string, approved wire.ModelFile, destination string) error {
	known, ok := wire.ModelFileForName(approved.Path)
	if !ok || known != approved || !modelTaskPath.MatchString(path) {
		return errors.New("INFERENCE_MODEL_INVALID")
	}
	var manifest wire.ModelCatalog
	if e := c.Post(ctx, strings.TrimSuffix(path, "model-files")+"model-manifest", wire.LeaseRenewal{LeaseToken: lease}, &manifest); e != nil {
		return errors.New("INFERENCE_MODEL_INVALID")
	}
	encoded, _ := json.Marshal(manifest)
	digest := sha256.Sum256(encoded)
	if hex.EncodeToString(digest[:]) != wire.ModelCatalogDigest() {
		return errors.New("INFERENCE_MODEL_INVALID")
	}
	return c.downloadModelFile(ctx, path, lease, approved, destination)
}

// DerivedModelFile binds ranges to the full typed delegated command rather than the base-only manifest.
func (c *Client) DerivedModelFile(ctx context.Context, path, lease string, command wire.InferenceCommand, approved wire.ModelFile, destination string) error {
	known, ok := command.ModelFile(approved.Path)
	if command.Derived == nil || command.Validate(wire.DerivedInferenceOperation) != nil || command.Action != "deploy" || !ok || known != approved || !modelTaskPath.MatchString(path) {
		return errors.New("INFERENCE_MODEL_INVALID")
	}
	input := wire.ModelFileRequest{LeaseToken: lease, File: approved.Path, Offset: 0, Length: min(wire.ModelTransferChunkBytes, approved.SizeBytes)}
	if input.ValidateForCommand(command) != nil {
		return errors.New("INFERENCE_MODEL_INVALID")
	}
	return c.downloadModelFile(ctx, path, lease, approved, destination)
}
func (c *Client) downloadModelFile(ctx context.Context, path, lease string, approved wire.ModelFile, destination string) error {
	file, e := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return errors.New("INFERENCE_IO_FAILED")
	}
	defer file.Close()
	hash := sha256.New()
	for offset := int64(0); offset < approved.SizeBytes; {
		if ctx.Err() != nil {
			return errors.New("INFERENCE_CANCELLED")
		}
		input := wire.ModelFileRequest{LeaseToken: lease, File: approved.Path, Offset: offset, Length: min(wire.ModelTransferChunkBytes, approved.SizeBytes-offset)}
		content, e := c.modelRangeWithRetry(ctx, path, input)
		if e != nil {
			return errors.New("INFERENCE_MODEL_INVALID")
		}
		if _, e = io.Copy(io.MultiWriter(file, hash), bytes.NewReader(content)); e != nil {
			return errors.New("INFERENCE_IO_FAILED")
		}
		offset += int64(len(content))
	}
	if hex.EncodeToString(hash.Sum(nil)) != approved.SHA256 {
		return errors.New("INFERENCE_MODEL_INVALID")
	}
	if file.Chmod(0444) != nil || file.Sync() != nil {
		return errors.New("INFERENCE_IO_FAILED")
	}
	return nil
}

// modelRangeWithRetry requests one range up to modelRangeAttempts times while control
// answers 503 or refuses it with a retry hint, waiting at least the hint between tries.
func (c *Client) modelRangeWithRetry(ctx context.Context, path string, input wire.ModelFileRequest) ([]byte, error) {
	var content []byte
	var e error
	var delay time.Duration
	for attempt := 0; attempt < modelRangeAttempts; attempt++ {
		if attempt > 0 && !sleep(ctx, delay) {
			break
		}
		content, e = c.modelRange(ctx, path, input)
		if e == nil || ctx.Err() != nil {
			break
		}
		var problem *Problem
		hint, hinted := RetryHint(e)
		if errors.As(e, &problem) && problem.Status != 503 && !hinted {
			break
		}
		delay = FullJitter(settings.Seconds(c.Policy().BackoffInitialSeconds), settings.Seconds(c.Policy().BackoffMaxSeconds), attempt)
		if hinted {
			delay = max(delay, AfterHint(hint))
		}
	}
	return content, e
}

func (c *Client) modelRange(ctx context.Context, path string, input wire.ModelFileRequest) ([]byte, error) {
	b, _ := json.Marshal(input)
	r, e := http.NewRequestWithContext(ctx, "POST", c.target(path), bytes.NewReader(b))
	if e != nil {
		return nil, errors.New("CONTROL_REQUEST_INVALID")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+c.Credential)
	response, e := c.HTTP.Do(r)
	if e != nil {
		return nil, errors.New("CONTROL_CONNECTION_FAILED")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		var p Problem
		b, e := io.ReadAll(io.LimitReader(response.Body, 65537))
		if e != nil || len(b) > 65536 || json.Unmarshal(b, &p) != nil {
			return nil, errors.New("CONTROL_RESPONSE_INVALID")
		}
		p.Status, p.RetryAfter = response.StatusCode, retryAfter(response.Header)
		return nil, &p
	}
	if response.ContentLength != input.Length || response.Header.Get("Content-Type") != "application/octet-stream" {
		return nil, errors.New("CONTROL_RESPONSE_INVALID")
	}
	b, e = io.ReadAll(io.LimitReader(response.Body, input.Length+1))
	sum := sha256.Sum256(b)
	if e != nil || int64(len(b)) != input.Length || response.Header.Get("X-Artifact-SHA256") != hex.EncodeToString(sum[:]) {
		return nil, errors.New("CONTROL_RESPONSE_INVALID")
	}
	return b, nil
}
