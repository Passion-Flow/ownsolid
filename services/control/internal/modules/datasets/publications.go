package datasets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type rowMapping struct {
	EffectiveLineNumber int64  `json:"effective_line_number"`
	RowID               string `json:"row_id"`
	OriginalLineNumber  int64  `json:"original_line_number"`
	RowRevision         int64  `json:"row_revision"`
}

// Only validation performed on successfully read snapshot content may complete
// the job in the current transaction. SQL/scan errors must never acquire this
// marker merely because their message happens to start with DATASET_.
type snapshotValidationError struct{ code string }

func (e *snapshotValidationError) Error() string { return e.code }

func snapshot(ctx context.Context, tx transaction.Executor, draft string, limits wire.Limits) (content, rowMap []byte, contentDigest string, resultError error) {
	rows, e := tx.Query(ctx, datasetSQL(tx, "SELECT "+rowFields+" FROM dataset_rows WHERE draft_id=$1 AND NOT deleted ORDER BY line_number,id", `SELECT id,line_number,revision,source_id,prompt,completion,error_code,raw_preview,preview_replaced FROM dataset_rows WHERE draft_id=? AND NOT deleted ORDER BY line_number,id`, []any{draft}, []any{draft}, transaction.SQL(transaction.Dameng, dmPublicationRows, draft)))
	if e != nil {
		return nil, nil, "", e
	}
	defer func() {
		rows.Close()
		// A stream/close error takes precedence over a content validation error:
		// PostgreSQL may discover it while draining the remaining result rows.
		if e := rows.Err(); e != nil {
			_, validation := resultError.(*snapshotValidationError)
			if resultError == nil || validation {
				content, rowMap, contentDigest, resultError = nil, nil, "", e
			}
		}
	}()
	var data, mapping bytes.Buffer
	line := int64(0)
	for rows.Next() {
		r, e := scanRow(rows)
		if e != nil {
			return nil, nil, "", e
		}
		if r.ErrorCode != nil || r.SourceID == nil || r.Prompt == nil || r.Completion == nil {
			return nil, nil, "", &snapshotValidationError{"DATASET_VALIDATION_FAILED"}
		}
		line++
		if line > limits.MaximumRows {
			return nil, nil, "", &snapshotValidationError{"DATASET_ROW_LIMIT"}
		}
		raw, e := (wire.Record{SourceID: *r.SourceID, Prompt: *r.Prompt, Completion: *r.Completion}).Canonical(limits)
		if e != nil {
			switch e.Error() {
			case "DATASET_SOURCE_INVALID", "DATASET_TEXT_INVALID", "DATASET_LINE_TOO_LARGE":
				return nil, nil, "", &snapshotValidationError{e.Error()}
			}
			return nil, nil, "", e
		}
		data.Write(raw)
		data.WriteByte('\n')
		if int64(data.Len()) > limits.MaximumBytes {
			return nil, nil, "", &snapshotValidationError{"DATASET_FILE_TOO_LARGE"}
		}
		encoded, _ := json.Marshal(rowMapping{line, r.ID, r.LineNumber, r.Revision})
		mapping.Write(encoded)
		mapping.WriteByte('\n')
	}
	if rows.Err() != nil {
		return nil, nil, "", rows.Err()
	}
	if line == 0 {
		return nil, nil, "", &snapshotValidationError{"DATASET_EMPTY_SOURCE"}
	}
	digest := sha256.Sum256(data.Bytes())
	return data.Bytes(), mapping.Bytes(), hex.EncodeToString(digest[:]), nil
}

// Publish uses the established exact-deduplication publication path.
func (s *Service) Publish(ctx context.Context, a identity.Auth, workspace, dataset, draft string, input PublicationRequest, key, requestID string) (Job, error) {
	return s.PublishWithOptions(ctx, a, workspace, dataset, draft, input, PublicationOptions{}, key, requestID)
}

// PublishWithDeduplication selects the opt-in lexical rule for an existing draft.
func (s *Service) PublishWithDeduplication(ctx context.Context, a identity.Auth, workspace, dataset, draft string, input PublicationRequest, rule, key, requestID string) (Job, error) {
	if rule != NearPromptV1 {
		return Job{}, failure("VALIDATION_FAILED")
	}
	return s.PublishWithOptions(ctx, a, workspace, dataset, draft, input, PublicationOptions{Deduplication: rule}, key, requestID)
}

// PublicationOptions selects internal publication behavior independently of the public request.
type PublicationOptions struct {
	Deduplication   string `json:"deduplication,omitempty"`
	TestBasisPoints int    `json:"test_basis_points,omitempty"`
}

// PublishWithOptions queues an immutable publication, optionally reserving a test split.
func (s *Service) PublishWithOptions(ctx context.Context, a identity.Auth, workspace, dataset, draft string, input PublicationRequest, options PublicationOptions, key, requestID string) (Job, error) {
	var out Job
	if !wire.ValidID(key) || input.ExpectedRevision < 1 || !validReason(input.Reason) || wire.ValidateSplit(input.Seed, input.ValidationBasisPoints) != nil ||
		options.Deduplication != "" && options.Deduplication != NearPromptV1 ||
		options.TestBasisPoints != 0 && wire.ValidateThreeWaySplit(input.Seed, input.ValidationBasisPoints, options.TestBasisPoints) != nil {
		return out, failure("VALIDATION_FAILED")
	}
	intent := fingerprint(struct {
		PublicationRequest
		PublicationOptions
	}{input, options}, a.Me.User.ID)
	e := s.transaction(ctx, a, workspace, true, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		parent, e := lockedDataset(ctx, tx, w, dataset)
		if e != nil {
			return e
		}
		d, e := lockedDraft(ctx, tx, w, dataset, draft)
		if e != nil {
			return e
		}
		if !isOwner(a, w) {
			return failure("ACCESS_DENIED")
		}
		var id string
		var original []byte
		e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT id::text,creation_fingerprint FROM dataset_jobs WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND draft_id=$4 AND requested_by=$5 AND creation_key=$6", `SELECT id,creation_fingerprint FROM dataset_jobs WHERE tenant_id=? AND workspace_id=? AND dataset_id=? AND draft_id=? AND requested_by=? AND creation_key=?`, []any{w.TenantID, w.ID, dataset, draft, a.Me.User.ID, key}, []any{w.TenantID, w.ID, dataset, draft, a.Me.User.ID, key}, transaction.SQL(transaction.Dameng, dmPublicationFindIntent, w.TenantID, w.ID, dataset, draft, a.Me.User.ID, key))).Scan(&id, &original)
		if e == nil {
			if !sameFingerprint(original, intent) {
				return failure("IDEMPOTENCY_CONFLICT")
			}
			out, e = lockedJob(ctx, tx, w, dataset, id)
			return e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if parent.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		if e = editable(a, w, d); e != nil {
			return e
		}
		if d.Revision != input.ExpectedRevision {
			return failure("DATASET_REVISION_CONFLICT")
		}
		if d.InvalidRows == nil || *d.InvalidRows != 0 {
			return failure("DATASET_VALIDATION_FAILED")
		}
		var count int
		if e = tx.QueryRow(ctx, datasetSQL(tx, "SELECT count(*) FROM dataset_versions WHERE dataset_id=$1", `SELECT count(*) FROM dataset_versions WHERE dataset_id=?`, []any{dataset}, []any{dataset}, transaction.SQL(transaction.Dameng, dmPublicationCountLimit, dataset))).Scan(&count); e != nil {
			return e
		}
		if count >= s.policy().MaxVersionsPerDataset {
			return failure("DATASET_LIMIT_REACHED")
		}
		_, _, digest, e := snapshot(ctx, tx, draft, s.policy().Limits)
		if e != nil {
			return e
		}
		authority, e := delegatedAuthority(ctx, tx, a, w)
		if e != nil {
			return e
		}
		id = uuid.NewString()
		_, e = tx.Exec(ctx, datasetSQL(tx, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,creation_key,creation_fingerprint) VALUES($1,$2,$3,$4,$5,'publish',$6,$7,$8,$9,$10,$11,'pending',$12,$13,$14,$15,$16,$17,$18)`, `INSERT INTO dataset_jobs(id,tenant_id,workspace_id,dataset_id,draft_id,kind,requested_by,requested_audience,requested_epoch,membership_authority,reason,expected_revision,state,version_id,input_sha256,input_policy,split_seed,validation_basis_points,creation_key,creation_fingerprint) VALUES(?,?,?,?,?,'publish',?,?,?,?,?,?,'pending',?,?,CAST(? AS JSON),?,?,?,?)`, []any{id, w.TenantID, w.ID, dataset, draft, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, input.Reason, d.Revision, uuid.NewString(), digest, s.jobPolicyBytes(options), input.Seed, input.ValidationBasisPoints, key, intent}, []any{id, w.TenantID, w.ID, dataset, draft, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, input.Reason, d.Revision, uuid.NewString(), digest, s.jobPolicyBytes(options), input.Seed, input.ValidationBasisPoints, key, intent}, transaction.SQL(transaction.Dameng, dmPublicationQueue, id, w.TenantID, w.ID, dataset, draft, a.Me.User.ID, a.Me.Audience, a.Epoch, authority, input.Reason, d.Revision, uuid.NewString(), digest, string(s.jobPolicyBytes(options)), input.Seed, input.ValidationBasisPoints, key, intent)))
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, datasetSQL(tx, "UPDATE dataset_drafts SET state='publishing',current_job_id=$2,error_code=NULL,error_line_number=NULL,updated_at=clock_timestamp() WHERE id=$1", `UPDATE dataset_drafts SET state='publishing',current_job_id=?,error_code=NULL,error_line_number=NULL,updated_at=CURRENT_TIMESTAMP(6) WHERE id=?`, []any{draft, id}, []any{id, draft}, transaction.SQL(transaction.Dameng, dmPublicationAttachDraft, id, draft))); e != nil {
			return e
		}
		if e = auditEvent(ctx, tx, a, w, "dataset.publication_requested", "dataset_job", id, input.Reason, requestID); e != nil {
			return e
		}
		out, e = lockedJob(ctx, tx, w, dataset, id)
		return e
	})
	return committed(out, e)
}
