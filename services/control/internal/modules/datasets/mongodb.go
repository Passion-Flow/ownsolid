package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/mongodatasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type MongoDBRequest struct {
	SourceID        string `json:"source_id"`
	PromptField     string `json:"prompt_field"`
	CompletionField string `json:"completion_field"`
	Conversion      string `json:"conversion"`
	Reason          string `json:"reason"`
}

func (r MongoDBRequest) mapping() mongodatasets.Mapping {
	return mongodatasets.Mapping{Prompt: r.PromptField, Completion: r.CompletionField, Conversion: r.Conversion}
}

type MongoDBPublicSource struct {
	ID     string   `json:"id"`
	Fields []string `json:"fields"`
}
type MongoDBImport struct {
	ID           string         `json:"id"`
	Request      MongoDBRequest `json:"request"`
	CreationKey  string         `json:"creation_key"`
	SourceID     string         `json:"source_id"`
	State        string         `json:"state"`
	AttemptCount int            `json:"attempt_count"`
	SHA256       *string        `json:"source_sha256"`
	Bytes        *int64         `json:"source_bytes"`
	Rows         *int64         `json:"source_rows"`
	FirstID      *string        `json:"first_id"`
	LastID       *string        `json:"last_id"`
	DraftID      *string        `json:"draft_id"`
	ErrorCode    *string        `json:"error_code"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Consistency  string         `json:"consistency"`
	objectID     string
	attemptID    string
	busy         bool
	intent       []byte
	sourceGrant  []byte
	retained     []string
}

const mongoImportFields = `id::text,source_id,state,attempt_count,source_sha256,source_bytes,source_rows,first_id,last_id,draft_id::text,error_code,updated_at,object_id::text,attempt_id::text,COALESCE(lease_expires_at>clock_timestamp(),false),intent,request,creation_key::text,source_grant,retained_objects`

func scanMongoImport(row transaction.Row) (MongoDBImport, error) {
	var out MongoDBImport
	var raw, grant, retainedRaw []byte
	e := row.Scan(&out.ID, &out.SourceID, &out.State, &out.AttemptCount, &out.SHA256, &out.Bytes, &out.Rows, &out.FirstID, &out.LastID, &out.DraftID, &out.ErrorCode, &out.UpdatedAt, &out.objectID, &out.attemptID, &out.busy, &out.intent, &raw, &out.CreationKey, &grant, &retainedRaw)
	if errors.Is(e, sql.ErrNoRows) {
		return MongoDBImport{}, failure("DATASET_NOT_FOUND")
	}
	if e != nil {
		return MongoDBImport{}, e
	}
	if json.Unmarshal(raw, &out.Request) != nil || json.Unmarshal(retainedRaw, &out.retained) != nil {
		return MongoDBImport{}, failure("DATASET_DATA_INVALID")
	}
	out.sourceGrant = grant
	if !mongoReceiptValid(out) {
		return MongoDBImport{}, failure("DATASET_DATA_INVALID")
	}
	out.Consistency = "snapshot-session"
	return out, nil
}

// The first source variant has PostgreSQL business persistence. Reject another
// selected dialect before opening a MongoDB connection or mutating private files.
func (s *Service) mongoTransaction(ctx context.Context, a identity.Auth, workspace string, write bool, operation func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	repo, e := s.repository()
	if e != nil {
		return e
	}
	if repo.Dialect() != transaction.PostgreSQL || s.DB == nil {
		return failure("MONGODB_BUSINESS_DATABASE_UNSUPPORTED")
	}
	return s.transaction(ctx, a, workspace, write, operation)
}

func (s *Service) mongoSource(w workspaces.Workspace, id string) (config.MongoDBSource, error) {
	p := s.Identity.Config.MongoDB
	if !p.Enabled {
		return config.MongoDBSource{}, failure("MONGODB_DISABLED")
	}
	for _, source := range p.Sources {
		if source.ID == id && source.TenantID == w.TenantID && source.WorkspaceID == w.ID {
			return source, nil
		}
	}
	return config.MongoDBSource{}, failure("ACCESS_DENIED")
}
func (s *Service) MongoDBSources(ctx context.Context, a identity.Auth, workspace, dataset string) ([]MongoDBPublicSource, error) {
	out := []MongoDBPublicSource{}
	e := s.mongoTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		if !s.Identity.Config.MongoDB.Enabled {
			return nil
		}
		for _, source := range s.Identity.Config.MongoDB.Sources {
			if source.TenantID == w.TenantID && source.WorkspaceID == w.ID {
				out = append(out, MongoDBPublicSource{source.ID, source.Fields})
			}
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func (s *Service) mongoAuthorize(ctx context.Context, a identity.Auth, workspace, dataset string, input MongoDBRequest) (config.MongoDBSource, error) {
	var source config.MongoDBSource
	if !validReason(input.Reason) {
		return source, failure("VALIDATION_FAILED")
	}
	e := s.mongoTransaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		d, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		if d.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		source, e = s.mongoSource(w, input.SourceID)
		if e != nil {
			return e
		}
		return input.mapping().Validate(source)
	})
	return source, e
}

// Preflight reads at most one projected document; no returned source value or credential escapes.
func (s *Service) MongoDBPreflight(ctx context.Context, a identity.Auth, workspace, dataset string, input MongoDBRequest) (mongodatasets.Result, error) {
	source, e := s.mongoAuthorize(ctx, a, workspace, dataset, input)
	if e != nil {
		return mongodatasets.Result{}, e
	}
	release, e := s.mongoSlot(ctx)
	if e != nil {
		return mongodatasets.Result{}, e
	}
	defer release()
	captured, e := mongodatasets.CaptureSource(source, s.Identity.Config.MongoDB)
	if e != nil {
		return mongodatasets.Result{}, e
	}
	if _, e = s.mongoSourceBinding(ctx, a, workspace, dataset, source, captured); e != nil {
		return mongodatasets.Result{}, e
	}
	result, e := captured.Read(ctx, input.mapping(), s.policy().Limits, true)
	if e != nil {
		return mongodatasets.Result{}, e
	}
	if _, e = s.mongoSourceBinding(ctx, a, workspace, dataset, source, captured); e != nil {
		return mongodatasets.Result{}, e
	}
	result.Data = nil
	return result, nil
}

// A session advisory lock bounds source reads across replicas without a business transaction.
// The reserved connection is explicitly released on every return and carries no secret/query values.
func (s *Service) mongoSlot(ctx context.Context) (func(), error) {
	c, e := s.DB.Acquire(ctx)
	if e != nil {
		return nil, failure("STORAGE_UNAVAILABLE")
	}
	for slot := 0; slot < s.Identity.Config.MongoDB.MaximumConcurrent; slot++ {
		var acquired bool
		e = c.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtext('projectone.mongodb.source'),$1)", slot).Scan(&acquired)
		if e != nil {
			c.Release()
			return nil, failure("STORAGE_UNAVAILABLE")
		}
		if acquired {
			return func() {
				bounded, cancel := context.WithTimeout(context.Background(), time.Duration(s.policy().CommitSeconds)*time.Second)
				defer cancel()
				var ok bool
				err := c.QueryRow(bounded, "SELECT pg_advisory_unlock(hashtext('projectone.mongodb.source'),$1)", slot).Scan(&ok)
				if err != nil || !ok {
					_ = c.Conn().Close(bounded)
				}
				c.Release()
			}, nil
		}
	}
	c.Release()
	return nil, failure("MONGODB_BUSY")
}
func (s *Service) MongoDBGetImport(ctx context.Context, a identity.Auth, workspace, dataset, id string) (MongoDBImport, error) {
	if !wire.ValidID(id) {
		return MongoDBImport{}, failure("DATASET_NOT_FOUND")
	}
	var out MongoDBImport
	e := s.mongoTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		var e error
		out, e = scanMongoImport(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+mongoImportFields+" FROM mongodb_dataset_imports WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4 AND requested_by=$5", w.TenantID, w.ID, dataset, id, a.Me.User.ID)))
		if e != nil {
			return e
		}
		return s.mongoRecheckReceipt(ctx, tx, a, w, out)
	})
	if e != nil {
		return MongoDBImport{}, e
	}
	return out, nil
}
func (s *Service) mongoReserve(ctx context.Context, a identity.Auth, workspace, dataset string, input MongoDBRequest, source config.MongoDBSource, captured *mongodatasets.SnapshotSource, key, requestID string) (MongoDBImport, error) {
	var out MongoDBImport
	intent := fingerprint(struct {
		Input      MongoDBRequest
		Source     config.MongoDBSource
		Credential string
	}{input, source, captured.Fingerprint()}, a.Me.User.ID)
	p := s.Identity.Config.MongoDB
	leaseSeconds := p.TimeoutSeconds + 3*p.CloseSeconds + 2*s.policy().UploadSeconds + 4*s.policy().CommitSeconds
	e := s.mongoTransaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		d, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		if d.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		grant, e := s.mongoGrant(ctx, tx, a, w, source, captured)
		if e != nil {
			return e
		}
		out, e = scanMongoImport(tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+mongoImportFields+" FROM mongodb_dataset_imports WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND requested_by=$4 AND creation_key=$5 FOR UPDATE", w.TenantID, w.ID, dataset, a.Me.User.ID, key)))
		if e == nil {
			if e = s.mongoRecheckReceipt(ctx, tx, a, w, out); e != nil {
				return e
			}
			sameIntent := sameFingerprint(intent, out.intent)
			var legacy config.MongoDBSource
			if wire.DecodeStrict(out.sourceGrant, &legacy) == nil {
				// Preserve explicit replay of pre-versioned receipts without
				// inventing historical credential provenance for them.
				sameIntent = sameFingerprint(fingerprint(struct {
					Input  MongoDBRequest
					Source config.MongoDBSource
				}{input, source}, a.Me.User.ID), out.intent)
			}
			if !sameIntent {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			if out.State == "imported" {
				return nil
			}
			if out.busy {
				return failure("MONGODB_BUSY")
			}
			if out.AttemptCount >= p.MaximumAttempts {
				return failure("MONGODB_RETRY_LIMIT")
			}
			out.attemptID = uuid.NewString()
			out.AttemptCount++
			if out.SHA256 == nil {
				out.objectID = uuid.NewString()
				out.State = "reading"
			} else {
				out.State = "exported"
			}
			_, e = tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE mongodb_dataset_imports SET attempt_id=$2,attempt_count=$3,object_id=$4,state=$5,lease_expires_at=clock_timestamp()+make_interval(secs=>$6),error_code=NULL,retained_objects=retained_objects||jsonb_build_array($4::uuid::text),updated_at=clock_timestamp() WHERE id=$1`, out.ID, out.attemptID, out.AttemptCount, out.objectID, out.State, leaseSeconds))
		} else if e.Error() == "DATASET_NOT_FOUND" {
			var count int
			if e = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT count(*) FROM mongodb_dataset_imports WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3", w.TenantID, w.ID, dataset)).Scan(&count); e != nil {
				return e
			}
			if count >= p.MaximumImportsPerDataset {
				return failure("DATASET_LIMIT_REACHED")
			}
			out.ID = uuid.NewString()
			out.objectID = uuid.NewString()
			out.attemptID = uuid.NewString()
			out.State = "reading"
			out.SourceID = input.SourceID
			raw, _ := json.Marshal(input)
			_, e = tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `INSERT INTO mongodb_dataset_imports(id,tenant_id,workspace_id,dataset_id,requested_by,creation_key,intent,request,source_grant,source_id,state,attempt_id,attempt_count,lease_expires_at,object_id,retained_objects) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$13,$9,'reading',$10,1,clock_timestamp()+make_interval(secs=>$11),$12,jsonb_build_array($12::uuid::text))`, out.ID, w.TenantID, w.ID, dataset, a.Me.User.ID, key, intent, raw, input.SourceID, out.attemptID, leaseSeconds, out.objectID, grant))
		}
		if e != nil {
			return e
		}
		return auditEvent(ctx, tx, a, w, "dataset.mongodb_import_requested", "mongodb_import", out.ID, input.Reason, requestID)
	})
	if e != nil {
		return MongoDBImport{}, e
	}
	return out, nil
}
func (s *Service) MongoDBImport(ctx context.Context, a identity.Auth, workspace, dataset string, input MongoDBRequest, key, requestID string) (MongoDBImport, error) {
	if !wire.ValidID(key) {
		return MongoDBImport{}, failure("VALIDATION_FAILED")
	}
	if s.Store == nil || s.Processor == nil {
		return MongoDBImport{}, failure("DATASET_STORAGE_UNAVAILABLE")
	}
	source, e := s.mongoAuthorize(ctx, a, workspace, dataset, input)
	if e != nil {
		return MongoDBImport{}, e
	}
	// Take the slot before reserving a receipt so denied admission creates no partial intent.
	release, e := s.mongoSlot(ctx)
	if e != nil {
		return MongoDBImport{}, e
	}
	defer release()
	captured, e := mongodatasets.CaptureSource(source, s.Identity.Config.MongoDB)
	if e != nil {
		return MongoDBImport{}, e
	}
	receipt, e := s.mongoReserve(ctx, a, workspace, dataset, input, source, captured, key, requestID)
	if e != nil || receipt.State == "imported" {
		return receipt, e
	}
	fail := func(cause error) (MongoDBImport, error) {
		// An unknown commit may already have recorded the checkpoint/attachment.
		// Keep its lease and intent intact for explicit readback; do not compensate.
		if mongoUnknown(cause) {
			return MongoDBImport{}, mongoOutcome(cause)
		}
		bounded, cancel := context.WithTimeout(context.Background(), time.Duration(s.policy().CommitSeconds)*time.Second)
		defer cancel()
		recorded := s.mongoRecordFailure(bounded, a, workspace, dataset, receipt, safeMongoImportError(cause))
		if mongoUnknown(recorded) {
			return MongoDBImport{}, mongoOutcome(recorded)
		}
		return MongoDBImport{}, mongoOutcome(cause)
	}
	if receipt.SHA256 == nil {
		result, readError := captured.Read(ctx, input.mapping(), s.policy().Limits, false)
		if readError != nil {
			return fail(readError)
		}
		sum := sha256.Sum256(result.Data)
		digest := hex.EncodeToString(sum[:])
		size := int64(len(result.Data))
		bounded, cancel := context.WithTimeout(ctx, time.Duration(s.policy().UploadSeconds)*time.Second)
		_, e = s.Store.PutSource(bounded, receipt.objectID, receipt.attemptID, digest, size, bytes.NewReader(result.Data))
		cancel()
		if e != nil {
			return fail(e)
		}
		e = s.mongoTransaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
			if _, e := s.mongoGrant(ctx, tx, a, w, source, captured); e != nil {
				return e
			}
			d, e := lockedDataset(ctx, tx, w, dataset)
			if e != nil {
				return e
			}
			if d.RetiredAt != nil {
				return failure("DATASET_RETIRED")
			}
			tag, e := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, `UPDATE mongodb_dataset_imports SET state='exported',source_sha256=$3,source_bytes=$4,source_rows=$5,first_id=$6,last_id=$7,updated_at=clock_timestamp() WHERE id=$1 AND attempt_id=$2 AND state='reading' AND lease_expires_at>clock_timestamp()`, receipt.ID, receipt.attemptID, digest, size, result.Rows, result.FirstID, result.LastID))
			if e != nil {
				return e
			}
			if tag != 1 {
				return failure("DATASET_ATTEMPT_STALE")
			}
			return nil
		})
		if e != nil {
			return fail(e)
		}
		receipt.SHA256 = &digest
		receipt.Bytes = &size
	}
	// Reopen through the pinned store root and verify the exact descriptor passed to M08.
	before, e := privatePath(filepath.Join(s.Store.path, objectPath(receipt.objectID)), s.Store.path, false, *receipt.Bytes)
	if e != nil {
		return fail(e)
	}
	file, e := s.Store.root.OpenFile(objectPath(receipt.objectID), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return fail(failure("DATASET_FILE_INVALID"))
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 || opened.Size() != *receipt.Bytes {
		return fail(failure("DATASET_FILE_INVALID"))
	}
	hash := sha256.New()
	n, e := io.Copy(hash, io.LimitReader(boundedContextReader{ctx, file}, *receipt.Bytes+1))
	if e != nil || n != *receipt.Bytes || hex.EncodeToString(hash.Sum(nil)) != *receipt.SHA256 {
		return fail(failure("DATASET_FILE_INVALID"))
	}
	if _, e = file.Seek(0, io.SeekStart); e != nil {
		return fail(failure("DATASET_FILE_INVALID"))
	}
	ctx = withMongoAttachment(ctx, receipt)
	if e = s.mongoCheckAttachment(ctx, a, workspace, dataset, receipt); e != nil {
		return fail(e)
	}
	draft, e := s.CreateDraft(ctx, a, workspace, dataset, DraftRequest{SourceName: "mongodb-" + receipt.ID + ".jsonl", SourceSHA256: *receipt.SHA256, ExpectedBytes: *receipt.Bytes, Reason: input.Reason}, receipt.ID, requestID)
	if e != nil {
		return fail(e)
	}
	if e = s.mongoBindDraft(ctx, a, workspace, dataset, receipt, draft.ID); e != nil {
		return fail(e)
	}
	_, e = s.Upload(ctx, a, workspace, dataset, draft.ID, *receipt.SHA256, *receipt.Bytes, file, requestID)
	if e != nil {
		return fail(e)
	}
	e = s.mongoTransaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := s.mongoAttachmentWithin(ctx, tx, a, w, dataset, receipt); e != nil {
			return e
		}
		d, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		if d.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		tag, e := tx.Exec(ctx, transaction.SQL(transaction.PostgreSQL, "UPDATE mongodb_dataset_imports SET state='imported',draft_id=$3,lease_expires_at=NULL,error_code=NULL,updated_at=clock_timestamp() WHERE id=$1 AND attempt_id=$2 AND state='exported' AND lease_expires_at>clock_timestamp()", receipt.ID, receipt.attemptID, draft.ID))
		if e != nil {
			return e
		}
		if tag != 1 {
			return failure("DATASET_ATTEMPT_STALE")
		}
		return auditEvent(ctx, tx, a, w, "dataset.mongodb_imported", "mongodb_import", receipt.ID, input.Reason, requestID)
	})
	if e != nil {
		return fail(e)
	}
	return s.MongoDBGetImport(ctx, a, workspace, dataset, receipt.ID)
}
func safeMongoImportError(e error) string {
	switch e.Error() {
	case "MONGODB_TIMEOUT", "MONGODB_CANCELLED", "MONGODB_CONNECT_FAILED", "MONGODB_READ_FAILED", "MONGODB_ACCESS_DENIED", "MONGODB_SNAPSHOT_EXPIRED", "MONGODB_SNAPSHOT_UNSUPPORTED", "MONGODB_IDENTIFIER_INVALID", "MONGODB_ORDER_INVALID", "MONGODB_LIMIT_EXCEEDED", "MONGODB_SOURCE_EMPTY", "MONGODB_FIELD_INVALID", "MONGODB_TYPE_INVALID", "MONGODB_RECORD_INVALID", "MONGODB_CONFIGURATION_INVALID", "MONGODB_SOURCE_CHANGED":
		return e.Error()
	}
	return safeDatasetError(e)
}

// MongoDBImports returns only the current actor's bounded receipt inventory.
func (s *Service) MongoDBImports(ctx context.Context, a identity.Auth, workspace, dataset string) ([]MongoDBImport, error) {
	out := []MongoDBImport{}
	e := s.mongoTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, tx, w, dataset); e != nil {
			return e
		}
		rows, e := tx.Query(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT "+mongoImportFields+" FROM mongodb_dataset_imports WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND requested_by=$4 ORDER BY created_at DESC,id DESC LIMIT $5", w.TenantID, w.ID, dataset, a.Me.User.ID, s.Identity.Config.MongoDB.MaximumImportsPerDataset))
		if e != nil {
			return e
		}
		defer rows.Close()
		// PostgreSQL cannot run membership-authority queries on this same
		// transaction while its result stream is open. Inventory is policy-bounded.
		retained := []MongoDBImport{}
		for rows.Next() {
			v, e := scanMongoImport(rows)
			if e != nil {
				return e
			}
			retained = append(retained, v)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		rows.Close()
		for _, v := range retained {
			if e = s.mongoRecheckReceipt(ctx, tx, a, w, v); e != nil {
				switch e.Error() {
				case "ACCESS_DENIED", "MONGODB_SOURCE_CHANGED", "MONGODB_DISABLED":
					continue
				default:
					return e
				}
			}
			out = append(out, v)
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
