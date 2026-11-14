package datasets

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DraftSourceCandidate is an opt-in PostgreSQL adapter for the module's
// isolated source receipt schema. Production does not register it.
type DraftSourceCandidate struct {
	service  *Service
	importer *SourceImporter
	slots    chan struct{}
}

// NewDraftSourceCandidate enables the isolated draft flow on an existing service. Parquet requires an explicit decoder.
func NewDraftSourceCandidate(s *Service, parquet *ParquetImportConfig) (*DraftSourceCandidate, error) {
	if s == nil || s.Identity == nil || s.Store == nil || s.Processor == nil {
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	repo, err := s.repository()
	if err != nil {
		return nil, err
	}
	policy := s.policy()
	if repo.Dialect() != transaction.PostgreSQL || policy.Validate() != nil {
		return nil, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	limits := policy.Limits
	importer, err := NewSourceImporter(SourceImportLimits{
		SourceBytes: limits.MaximumBytes,
		OutputBytes: limits.MaximumBytes,
		RecordBytes: int(2*limits.MaximumLineBytes + 3),
		Fields:      3,
		Timeout:     config.Seconds(policy.UploadSeconds),
	}, parquet)
	if err != nil {
		return nil, err
	}
	return &DraftSourceCandidate{s, importer, make(chan struct{}, policy.Concurrency)}, nil
}

// DraftSourceRequest declares the immutable original upload.
type DraftSourceRequest struct {
	Name   string
	SHA256 string
	Bytes  int64
}

// DraftSourceResult pairs the existing editable draft with its committed receipt.
type DraftSourceResult struct {
	Draft  Draft              `json:"draft"`
	Report SourceImportReport `json:"report"`
}

// Import streams a supported source, then commits the draft, report and inspection
// together. The caller owns the input reader and any transport read deadline.
func (c *DraftSourceCandidate) Import(ctx context.Context, a identity.Auth, workspace, dataset string, format SourceFormat, input DraftSourceRequest, key string, body io.Reader, requestID string) (DraftSourceResult, error) {
	s := c.service
	policy := s.policy()
	if !wire.ValidID(key) || !validDraftSourceName(input.Name) || !wire.ValidDigest(input.SHA256) || input.Bytes < 1 || body == nil {
		return DraftSourceResult{}, failure("VALIDATION_FAILED")
	}
	if input.Bytes > policy.Limits.MaximumBytes {
		return DraftSourceResult{}, failure("DATASET_FILE_TOO_LARGE")
	}
	var kind string
	switch format {
	case SourceCSV:
		kind = "csv_utf8_draft_v1"
	case SourceParquet:
		if c.importer.parquet == nil {
			return DraftSourceResult{}, failure("DATASET_STORAGE_UNAVAILABLE")
		}
		kind = "parquet_draft_v1"
	default:
		return DraftSourceResult{}, failure("VALIDATION_FAILED")
	}
	intent := fingerprint(struct {
		Format string
		Input  DraftSourceRequest
	}{kind, input}, a.Me.User.ID)
	var authority *string
	var result DraftSourceResult
	err := s.draftSourceTransaction(ctx, a, workspace, dataset, key, format, input, intent,
		func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, previous DraftSourceResult) error {
			result = previous
			if previous.Draft.ID != "" {
				return nil
			}
			if err := s.checkDraftCapacity(ctx, tx, w); err != nil {
				return err
			}
			var err error
			authority, err = delegatedAuthority(ctx, tx, a, w)
			return err
		})
	if err != nil || result.Draft.ID != "" {
		return committed(result, err)
	}

	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	default:
		return DraftSourceResult{}, failure("DATASET_UPLOAD_BUSY")
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(policy.UploadSeconds))
	defer cancel()
	object := uuid.NewString()
	directory, err := s.Store.allocateDraftSourceObject(object, input.Bytes+policy.Limits.MaximumBytes+1)
	if err != nil {
		return DraftSourceResult{}, err
	}
	original := draftOriginalFile(format)
	keepFiles := false
	defer func() {
		if !keepFiles {
			s.Store.discardDraftSourceObject(directory, original)
		}
	}()
	report, err := c.stage(bounded, directory, original, format, body, input, policy.Limits)
	if err != nil {
		return DraftSourceResult{}, err
	}
	draftID := uuid.NewString()
	err = s.draftSourceTransaction(ctx, a, workspace, dataset, key, format, input, intent,
		func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, previous DraftSourceResult) error {
			result = previous
			if previous.Draft.ID != "" {
				return nil
			}
			current, err := delegatedAuthority(ctx, tx, a, w)
			if err != nil {
				return err
			}
			if (authority == nil) != (current == nil) || authority != nil && *authority != *current {
				return failure("DATASET_ATTEMPT_STALE")
			}
			if err = s.checkDraftCapacity(ctx, tx, w); err != nil {
				return err
			}
			reason := "Import CSV source"
			if format == SourceParquet {
				reason = "Import Parquet source"
			}
			draftInput := DraftRequest{SourceName: input.Name, ExpectedBytes: report.OutputBytes, SourceSHA256: report.OutputSHA256, Reason: reason}
			if err = insertDraft(ctx, tx, a, w, dataset, draftID, object, draftInput, key, intent, nil, current); err != nil {
				return err
			}
			if err = auditEvent(ctx, tx, a, w, "dataset.draft_created", "dataset_draft", draftID, reason, requestID); err != nil {
				return err
			}
			d, err := lockedDraft(ctx, tx, w, dataset, draftID)
			if err != nil {
				return err
			}
			d, err = s.completeSourceUpload(ctx, tx, a, w, d, object, "", current, requestID)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(report)
			if err != nil {
				return failure("DATASET_DATA_INVALID")
			}
			inserted, err := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `
				INSERT INTO dataset_draft_import_candidates(tenant_id,workspace_id,dataset_id,draft_id,site_id,cell_id,source_format,report)
				SELECT d.tenant_id,d.workspace_id,d.dataset_id,d.id,d.site_id,d.cell_id,$5,$6::jsonb
				FROM dataset_drafts d
				WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.dataset_id=$3 AND d.id=$4
				  AND d.site_id=local_site_id() AND d.cell_id=local_cell_id()`, w.TenantID, w.ID, dataset, draftID, string(format), raw))
			if err != nil {
				return err
			}
			if inserted != 1 {
				return failure("DATASET_NOT_FOUND")
			}
			result = DraftSourceResult{Draft: d, Report: report}
			return nil
		})
	// A lost commit acknowledgement can leave a valid reference. Preserve this
	// attempt's files until the outcome can be reconciled with the same key.
	keepFiles = err == nil && result.Draft.ID == draftID || err != nil && err.Error() == string(transaction.CommitUnknown)
	return committed(result, err)
}

// Read returns a scoped draft and its report only after verifying both
// immutable artifacts. It does not need an upload body or an active decoder.
func (c *DraftSourceCandidate) Read(ctx context.Context, a identity.Auth, workspace, dataset, id string) (DraftSourceResult, error) {
	var result DraftSourceResult
	s := c.service
	err := s.transaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.TenantID != s.Store.namespace.TenantID {
			return failure("DATASET_NOT_FOUND")
		}
		if _, err := lockedDraftSourceDataset(ctx, tx, w, dataset); err != nil {
			return err
		}
		draft, err := lockedDraft(ctx, tx, w, dataset, id)
		if err != nil {
			return err
		}
		var object, format string
		var raw []byte
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
			SELECT d.source_object_id::text,c.source_format,c.report
			FROM dataset_drafts d JOIN dataset_draft_import_candidates c
			  ON (c.tenant_id,c.workspace_id,c.dataset_id,c.draft_id,c.site_id,c.cell_id)
		     =(d.tenant_id,d.workspace_id,d.dataset_id,d.id,d.site_id,d.cell_id)
			WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.dataset_id=$3 AND d.id=$4
			  AND d.site_id=local_site_id() AND d.cell_id=local_cell_id()`,
			w.TenantID, w.ID, dataset, id)).Scan(&object, &format, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return failure("DATASET_NOT_FOUND")
		}
		if err != nil {
			return err
		}
		if err = draftAccess(a, w, draft, false); err != nil {
			return err
		}
		result, err = s.Store.verifyDraftSource(draft, object, SourceFormat(format), raw)
		return err
	})
	return committed(result, err)
}

func draftOriginalFile(format SourceFormat) string {
	switch format {
	case SourceCSV:
		return "source.csv"
	case SourceParquet:
		return "source.parquet"
	default:
		return ""
	}
}

func (s *Store) verifyDraftSource(draft Draft, object string, format SourceFormat, raw []byte) (DraftSourceResult, error) {
	original := draftOriginalFile(format)
	var report SourceImportReport
	if original == "" || json.Unmarshal(raw, &report) != nil || draft.SourceReceivedAt == nil ||
		report.SourceBytes < 1 || report.SourceBytes > wire.MaximumBytesCeiling || !wire.ValidDigest(report.SourceSHA256) ||
		report.OutputBytes != draft.ExpectedBytes || report.OutputSHA256 != draft.SourceSHA256 {
		return DraftSourceResult{}, failure("DATASET_DATA_INVALID")
	}
	if _, err := s.Source(object, report.OutputSHA256, report.OutputBytes); err != nil {
		return DraftSourceResult{}, err
	}
	if err := s.verifyDraftOriginal(object, original, report); err != nil {
		return DraftSourceResult{}, err
	}
	return DraftSourceResult{Draft: draft, Report: report}, nil
}

// The candidate is PostgreSQL-only. Resolve its cell from the database
// selected by the router, while retaining the authenticated workspace fence.
func lockedDraftSourceDataset(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, id string) (Dataset, error) {
	if !wire.ValidID(id) {
		return Dataset{}, failure("DATASET_NOT_FOUND")
	}
	return scanDataset(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+datasetFields+`
		FROM datasets WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3
		  AND site_id=local_site_id() AND cell_id=local_cell_id() FOR UPDATE`, w.TenantID, w.ID, id)))
}

func (s *Service) draftSourceTransaction(ctx context.Context, a identity.Auth, workspace, dataset, key string, format SourceFormat, input DraftSourceRequest, intent []byte, apply func(context.Context, transaction.Executor, workspaces.Workspace, DraftSourceResult) error) error {
	return s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.TenantID != s.Store.namespace.TenantID {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
		if err := s.mongoAttachmentFence(ctx, tx, a, w, dataset); err != nil {
			return err
		}
		target, err := lockedDraftSourceDataset(ctx, tx, w, dataset)
		if err != nil {
			return err
		}
		var id, object, storedFormat string
		var original, raw []byte
		var local bool
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
			SELECT d.id::text,d.source_object_id::text,d.creation_fingerprint,COALESCE(c.source_format,''),c.report,
			       d.site_id=local_site_id() AND d.cell_id=local_cell_id()
			FROM dataset_drafts d LEFT JOIN dataset_draft_import_candidates c
			  ON (c.tenant_id,c.workspace_id,c.dataset_id,c.draft_id,c.site_id,c.cell_id)
		     =(d.tenant_id,d.workspace_id,d.dataset_id,d.id,d.site_id,d.cell_id)
			WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.dataset_id=$3
			  AND d.created_by=$4 AND d.creation_key=$5`, w.TenantID, w.ID, dataset, a.Me.User.ID, key)).Scan(&id, &object, &original, &storedFormat, &raw, &local)
		if errors.Is(err, pgx.ErrNoRows) {
			if target.RetiredAt != nil {
				return failure("DATASET_RETIRED")
			}
			return apply(ctx, tx, w, DraftSourceResult{})
		}
		if err != nil {
			return err
		}
		if !local {
			return failure("DATASET_NOT_FOUND")
		}
		if !sameFingerprint(original, intent) {
			return failure("IDEMPOTENCY_CONFLICT")
		}
		draft, err := lockedDraft(ctx, tx, w, dataset, id)
		if err != nil {
			return err
		}
		if err = draftAccess(a, w, draft, true); err != nil {
			return err
		}
		if storedFormat != string(format) {
			return failure("DATASET_DATA_INVALID")
		}
		result, err := s.Store.verifyDraftSource(draft, object, format, raw)
		if err != nil {
			return err
		}
		if result.Report.SourceBytes != input.Bytes || result.Report.SourceSHA256 != input.SHA256 {
			return failure("DATASET_DATA_INVALID")
		}
		return apply(ctx, tx, w, result)
	})
}

func (s *Store) allocateDraftSourceObject(object string, bytes int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if err := s.reserve(bytes); err != nil {
		return "", err
	}
	directory := "objects/" + object
	if err := s.root.Mkdir(directory, 0700); err != nil {
		return "", failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if err := s.sync("objects"); err != nil {
		_ = s.root.Remove(directory)
		return "", err
	}
	return directory, nil
}

func (s *Store) discardDraftSourceObject(directory, original string) {
	_ = s.root.Remove(filepath.Join(directory, original))
	_ = s.root.Remove(filepath.Join(directory, "source.jsonl"))
	_ = s.root.Remove(directory)
	_ = s.sync("objects")
}

func (s *Store) verifyDraftOriginal(object, original string, report SourceImportReport) error {
	// Source already validated the object ID in this directory.
	path := filepath.Join(s.path, "objects", object, original)
	if _, err := privatePath(path, s.path, false, report.SourceBytes); err != nil {
		return failure("DATASET_FILE_INVALID")
	}
	digest, bytes, err := fileDigest(path, report.SourceBytes)
	if err != nil || digest != report.SourceSHA256 || bytes != report.SourceBytes {
		return failure("DATASET_FILE_INVALID")
	}
	return nil
}

func (c *DraftSourceCandidate) stage(ctx context.Context, directory, original string, format SourceFormat, body io.Reader, input DraftSourceRequest, limits wire.Limits) (SourceImportReport, error) {
	store := c.service.Store
	raw, err := store.root.OpenFile(filepath.Join(directory, original), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return SourceImportReport{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer raw.Close()
	writer := draftOriginalWriter{file: raw}
	path := filepath.Join(store.path, directory, "source.jsonl")
	var parquetOriginal *os.File
	if format == SourceParquet {
		parquetOriginal = raw
	} else {
		body = io.TeeReader(body, &writer)
	}
	report, err := c.importer.importSource(ctx, body, path, SourceImportOptions{
		Format: format, ExpectedBytes: input.Bytes, ExpectedSHA256: input.SHA256,
	}, parquetOriginal)
	if writer.failed {
		return SourceImportReport{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if err != nil {
		return SourceImportReport{}, draftSourceError(err)
	}
	if report.Rows > limits.MaximumRows {
		return SourceImportReport{}, failure("DATASET_ROW_LIMIT")
	}
	if err = validateDraftSourceRows(ctx, path, limits); err != nil {
		return SourceImportReport{}, err
	}
	if raw.Sync() != nil || raw.Close() != nil {
		return SourceImportReport{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if err = store.sync(directory); err != nil {
		return SourceImportReport{}, err
	}
	return report, datasetRuntimeError(ctx.Err())
}

type draftOriginalWriter struct {
	file   *os.File
	failed bool
}

func (w *draftOriginalWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	if err != nil {
		w.failed = true
	}
	return n, err
}

func validateDraftSourceRows(ctx context.Context, path string, limits wire.Limits) error {
	file, err := os.Open(path)
	if err != nil {
		return failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer file.Close()
	lines := bufio.NewScanner(boundedContextReader{ctx, file})
	lines.Buffer(nil, int(limits.MaximumLineBytes)+1)
	for lines.Scan() {
		if _, err := wire.DecodeRecord(lines.Bytes(), limits); err != nil {
			return failure("DATASET_VALIDATION_FAILED")
		}
	}
	if ctx.Err() != nil {
		return datasetRuntimeError(ctx.Err())
	}
	if lines.Err() != nil {
		return failure("DATASET_VALIDATION_FAILED")
	}
	return nil
}

func draftSourceError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return datasetRuntimeError(err)
	}
	code := err.Error()
	var issue SourceIssue
	if errors.As(err, &issue) {
		code = issue.Code
	}
	switch code {
	case sourceReadFailedCode:
		return failure("DATASET_UPLOAD_INCOMPLETE")
	case "DATASET_IMPORT_OUTPUT_LIMIT":
		return failure("DATASET_FILE_TOO_LARGE")
	case "DATASET_FILE_TOO_LARGE", "DATASET_SOURCE_CHANGED", "DATASET_STORAGE_UNAVAILABLE":
		return failure(code)
	case "DATASET_IMPORT_COMMIT_UNCERTAIN", "DATASET_STORAGE_PATH_INVALID", "DATASET_OUTPUT_CONFLICT",
		"DATASET_PARQUET_RUNTIME_UNAVAILABLE", "DATASET_PARQUET_STAGE_FAILED":
		return failure("DATASET_STORAGE_UNAVAILABLE")
	default:
		return failure("DATASET_VALIDATION_FAILED")
	}
}
