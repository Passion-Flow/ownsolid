package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// The versioned import contract limits each binary write to 8 MiB.
const modelChunkLimit = 8 << 20

var modelImports = resource{key: "model-imports", columns: []column{
	{"ID", "id"}, {"NAME", "name"}, {"STATE", "state"}, {"UPLOADED", "uploaded_bytes"},
	{"EXPECTED", "expected_bytes"}, {"MODEL ID", "model_id"}, {"ERROR", "error_code"},
}}

type modelCatalog struct {
	Digest    string `json:"catalog_digest"`
	Chunk     int64  `json:"chunk_bytes"`
	Minimum   int64  `json:"minimum_bundle_bytes"`
	Maximum   int64  `json:"maximum_bundle_bytes"`
	Available bool   `json:"storage_available"`
	Catalog   struct {
		License string `json:"license"`
	} `json:"catalog"`
}

type modelImport struct {
	ID       string `json:"id"`
	Digest   string `json:"catalog_digest"`
	Expected int64  `json:"expected_bytes"`
	Uploaded int64  `json:"uploaded_bytes"`
	State    string `json:"state"`
}

type startModelImport struct {
	Workspace string `json:"workspace_id"`
	Name      string `json:"name"`
	Digest    string `json:"catalog_digest"`
	Expected  int64  `json:"expected_bytes"`
	Accepted  bool   `json:"license_acknowledged"`
	Reason    string `json:"reason"`
}

func (c *apiClient) modelCatalog(ctx context.Context, cfg settings) (modelCatalog, json.RawMessage, error) {
	var raw json.RawMessage
	var catalog modelCatalog
	if err := c.get(ctx, cfg.adminPath(models)+"/catalog", nil, &raw); err != nil {
		return catalog, nil, err
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return catalog, nil, errors.New("the server returned an invalid model catalog")
	}
	digest, err := hex.DecodeString(catalog.Digest)
	if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != catalog.Digest ||
		catalog.Chunk < 1 || catalog.Chunk > modelChunkLimit || catalog.Minimum < 1 ||
		catalog.Maximum < catalog.Minimum || catalog.Catalog.License == "" {
		return catalog, nil, errors.New("the server returned invalid model catalog limits or license")
	}
	return catalog, raw, nil
}

func decodeModelImport(raw json.RawMessage, id, workspace string) (modelImport, error) {
	var record modelImport
	if err := validateScope(raw, id, workspace); err != nil {
		return record, err
	}
	if err := json.Unmarshal(raw, &record); err != nil || record.Expected < 1 ||
		record.Uploaded < 0 || record.Uploaded > record.Expected || record.State == "" {
		return record, errors.New("the server returned an invalid model import")
	}
	return record, nil
}

func openModelBundle(path string) (*os.File, int64, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, 0, fmt.Errorf("open model bundle: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("inspect model bundle: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 {
		file.Close()
		return nil, 0, errors.New("the model bundle must be a nonempty regular file")
	}
	return file, info.Size(), nil
}

func (c *apiClient) transferModel(ctx context.Context, path, workspace string, record modelImport, file *os.File, chunk int64) error {
	if record.Uploaded != record.Expected && record.Uploaded%chunk != 0 {
		return errors.New("the server returned a model import offset that is outside a chunk boundary")
	}
	offset := record.Uploaded
	if offset > 0 {
		offset = ((offset - 1) / chunk) * chunk
	}
	req := request{method: http.MethodPost, path: path + "/" + record.ID + "/chunks", status: http.StatusOK, header: make(http.Header)}
	req.header.Set("Content-Type", "application/octet-stream")
	if err := c.prepareWrite(ctx, &req); err != nil {
		return err
	}
	buffer := make([]byte, chunk)
	for offset < record.Expected {
		if err := ctx.Err(); err != nil {
			return err
		}
		length := min(chunk, record.Expected-offset)
		req.body = buffer[:length]
		if _, err := file.ReadAt(req.body, offset); err != nil {
			return fmt.Errorf("read model bundle at byte %d: %w", offset, err)
		}
		sum := sha256.Sum256(req.body)
		req.header.Set("X-Chunk-Offset", strconv.FormatInt(offset, 10))
		req.header.Set("X-Chunk-SHA256", hex.EncodeToString(sum[:]))
		var raw json.RawMessage
		if err := c.sendModelChunk(ctx, &req, &raw); err != nil {
			return fmt.Errorf("chunk at byte %d: %w", offset, err)
		}
		next, err := decodeModelImport(raw, record.ID, workspace)
		if err != nil {
			return err
		}
		offset += length
		if next.Expected != record.Expected || next.Digest != record.Digest ||
			next.State != "uploading" || next.Uploaded < offset || next.Uploaded < record.Uploaded {
			return errors.New("the server returned an inconsistent model import offset or state")
		}
		record.Uploaded = next.Uploaded
	}
	return nil
}

func (c *apiClient) sendModelChunk(ctx context.Context, req *request, raw *json.RawMessage) error {
	err := c.request(ctx, *req, raw)
	var problem *apiError
	if !errors.As(err, &problem) || problem.code != "CSRF_INVALID" {
		return err
	}
	if err := c.prepareWrite(ctx, req); err != nil {
		return err
	}
	return c.request(ctx, *req, raw)
}
