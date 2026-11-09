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

// CSVImportCandidate is an opt-in PostgreSQL adapter for the module's
// isolated source receipt schema. Production does not register it.
type CSVImportCandidate struct {
	service  *Service
	importer *SourceImporter
	slots    chan struct{}
}

// NewCSVImportCandidate enables the isolated CSV flow on an existing service.
func NewCSVImportCandidate(s *Service) (*CSVImportCandidate, error) {
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
	}, nil)
	if err != nil {
		return nil, err
	}
	return &CSVImportCandidate{s, importer, make(chan struct{}, policy.Concurrency)}, nil
}

// CSVImportRequest declares the immutable original upload.
type CSVImportRequest struct {
	Name   string
	SHA256 string
	Bytes  int64
}

// CSVImportResult pairs the existing editable draft with its committed receipt.
type CSVImportResult struct {
	Draft  Draft              `json:"draft"`
	Report SourceImportReport `json:"report"`
}

// Import streams a CSV source, then commits the draft, report and inspection
// together. The caller owns the input reader and any transport read deadline.
func (c *CSVImportCandidate) Import(ctx context.Context, a identity.Auth, workspace, dataset string, input CSVImportRequest, key string, body io.Reader, requestID string) (CSVImportResult, error) {
	s := c.service
	policy := s.policy()
	if !wire.ValidID(key) || !validDraftSourceName(input.Name) || !wire.ValidDigest(input.SHA256) || input.Bytes < 1 || body == nil {
		return CSVImportResult{}, failure("VALIDATION_FAILED")
	}
	if input.Bytes > policy.Limits.MaximumBytes {
		return CSVImportResult{}, failure("DATASET_FILE_TOO_LARGE")
	}
	intent := fingerprint(struct {
		Format string
		Input  CSVImportRequest
	}{"csv_utf8_draft_v1", input}, a.Me.User.ID)
	var authority *string
	var result CSVImportResult
	err := s.csvImportTransaction(ctx, a, workspace, dataset, key, input, intent,
		func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, previous CSVImportResult) error {
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
		return CSVImportResult{}, failure("DATASET_UPLOAD_BUSY")
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(policy.UploadSeconds))
	defer cancel()
	object := uuid.NewString()
	directory, err := s.Store.allocateCSVObject(object, input.Bytes+policy.Limits.MaximumBytes+1)
	if err != nil {
		return CSVImportResult{}, err
	}
	keepFiles := false
	defer func() {
		if !keepFiles {
			s.Store.discardCSVObject(directory)
		}
	}()
	report, err := c.stage(bounded, directory, body, input, policy.Limits)
	if err != nil {
		return CSVImportResult{}, err
	}
	draftID := uuid.NewString()
	err = s.csvImportTransaction(ctx, a, workspace, dataset, key, input, intent,
		func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, previous CSVImportResult) error {
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
			const reason = "Import CSV source"
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
			_, err = tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `
				INSERT INTO dataset_csv_import_candidates(tenant_id,workspace_id,dataset_id,draft_id,report)
				VALUES($1,$2,$3,$4,$5::jsonb)`, w.TenantID, w.ID, dataset, draftID, raw))
			if err == nil {
				result = CSVImportResult{Draft: d, Report: report}
			}
			return err
		})
	// A lost commit acknowledgement can leave a valid reference. Preserve this
	// attempt's files until the outcome can be reconciled with the same key.
	keepFiles = err == nil && result.Draft.ID == draftID || err != nil && err.Error() == string(transaction.CommitUnknown)
	return committed(result, err)
}

func (s *Service) csvImportTransaction(ctx context.Context, a identity.Auth, workspace, dataset, key string, input CSVImportRequest, intent []byte, apply func(context.Context, transaction.Executor, workspaces.Workspace, CSVImportResult) error) error {
	return s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if w.TenantID != s.Store.namespace.TenantID {
			return failure("DATASET_STORAGE_UNAVAILABLE")
		}
		if err := s.mongoAttachmentFence(ctx, tx, a, w, dataset); err != nil {
			return err
		}
		target, err := lockedDataset(ctx, tx, w, dataset)
		if err != nil {
			return err
		}
		var id, object string
		var original, raw []byte
		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `
			SELECT d.id::text, d.source_object_id::text, d.creation_fingerprint, c.report
			FROM dataset_drafts d LEFT JOIN dataset_csv_import_candidates c
			  ON (c.tenant_id,c.workspace_id,c.dataset_id,c.draft_id)=(d.tenant_id,d.workspace_id,d.dataset_id,d.id)
			WHERE d.tenant_id=$1 AND d.workspace_id=$2 AND d.dataset_id=$3
			  AND d.created_by=$4 AND d.creation_key=$5`, w.TenantID, w.ID, dataset, a.Me.User.ID, key)).Scan(&id, &object, &original, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			if target.RetiredAt != nil {
				return failure("DATASET_RETIRED")
			}
			return apply(ctx, tx, w, CSVImportResult{})
		}
		if err != nil {
			return err
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
		var report SourceImportReport
		if json.Unmarshal(raw, &report) != nil || draft.SourceReceivedAt == nil ||
			report.SourceBytes != input.Bytes || report.SourceSHA256 != input.SHA256 ||
			report.OutputBytes != draft.ExpectedBytes || report.OutputSHA256 != draft.SourceSHA256 {
			return failure("DATASET_DATA_INVALID")
		}
		if _, err = s.Store.Source(object, report.OutputSHA256, report.OutputBytes); err != nil {
			return err
		}
		if err = s.Store.verifyCSVOriginal(object, report); err != nil {
			return err
		}
		return apply(ctx, tx, w, CSVImportResult{Draft: draft, Report: report})
	})
}

const rawCSVFile = "source.csv"

func (s *Store) allocateCSVObject(object string, bytes int64) (string, error) {
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
		s.discardCSVObject(directory)
		return "", err
	}
	return directory, nil
}

func (s *Store) discardCSVObject(directory string) {
	_ = s.root.Remove(filepath.Join(directory, rawCSVFile))
	_ = s.root.Remove(filepath.Join(directory, "source.jsonl"))
	_ = s.root.Remove(directory)
	_ = s.sync("objects")
}

func (s *Store) verifyCSVOriginal(object string, report SourceImportReport) error {
	// Source already validated the object ID in this directory.
	path := filepath.Join(s.path, "objects", object, rawCSVFile)
	if _, err := privatePath(path, s.path, false, report.SourceBytes); err != nil {
		return failure("DATASET_FILE_INVALID")
	}
	digest, bytes, err := fileDigest(path, report.SourceBytes)
	if err != nil || digest != report.SourceSHA256 || bytes != report.SourceBytes {
		return failure("DATASET_FILE_INVALID")
	}
	return nil
}

func (c *CSVImportCandidate) stage(ctx context.Context, directory string, body io.Reader, input CSVImportRequest, limits wire.Limits) (SourceImportReport, error) {
	store := c.service.Store
	raw, err := store.root.OpenFile(filepath.Join(directory, rawCSVFile), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return SourceImportReport{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	defer raw.Close()
	writer := csvOriginalWriter{file: raw}
	path := filepath.Join(store.path, directory, "source.jsonl")
	report, err := c.importer.ImportUpload(ctx, io.TeeReader(body, &writer), path, SourceImportOptions{
		Format: SourceCSV, ExpectedBytes: input.Bytes, ExpectedSHA256: input.SHA256,
	})
	if writer.failed {
		return SourceImportReport{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	if err != nil {
		return SourceImportReport{}, csvImportError(err)
	}
	if report.Rows > limits.MaximumRows {
		return SourceImportReport{}, failure("DATASET_ROW_LIMIT")
	}
	if err = validateCSVDraftRows(ctx, path, limits); err != nil {
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

type csvOriginalWriter struct {
	file   *os.File
	failed bool
}

func (w *csvOriginalWriter) Write(p []byte) (int, error) {
	n, err := w.file.Write(p)
	if err != nil {
		w.failed = true
	}
	return n, err
}

func validateCSVDraftRows(ctx context.Context, path string, limits wire.Limits) error {
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

func csvImportError(err error) error {
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
	case "DATASET_IMPORT_COMMIT_UNCERTAIN", "DATASET_STORAGE_PATH_INVALID", "DATASET_OUTPUT_CONFLICT":
		return failure("DATASET_STORAGE_UNAVAILABLE")
	default:
		return failure("DATASET_VALIDATION_FAILED")
	}
}
