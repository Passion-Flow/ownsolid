package datasets

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/mongodatasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

const dmMongoImportFields = `"ID","SOURCE_ID","STATE","ATTEMPT_COUNT","SOURCE_SHA256","SOURCE_BYTES","SOURCE_ROWS","FIRST_ID","LAST_ID","DRAFT_ID","ERROR_CODE","UPDATED_AT","OBJECT_ID","ATTEMPT_ID",CASE WHEN "LEASE_EXPIRES_AT">SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) THEN 1 ELSE 0 END,"INTENT","REQUEST","CREATION_KEY","SOURCE_GRANT","RETAINED_OBJECTS"`

func mongoFields(q transaction.Executor) string {
	if q.Dialect() == transaction.Dameng {
		return dmMongoImportFields
	}
	if q.Dialect() == transaction.PostgreSQL {
		return mongoImportFields
	}
	return ""
}
func mongoSQL(q transaction.Executor, pg, dm string, pgArgs, dmArgs []any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, pgArgs...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, dmArgs...)
	default:
		return transaction.SQL(q.Dialect(), "")
	}
}
func mongoLimit(p config.MongoDBConfiguration) (string, error) {
	if p.MaximumImportsPerDataset < 1 || p.MaximumImportsPerDataset > 1000 {
		return "", failure("MONGODB_CONFIGURATION_INVALID")
	}
	return strconv.Itoa(p.MaximumImportsPerDataset), nil
}
func mongoTimestamp(t time.Time) bool {
	_, offset := t.Zone()
	return offset == 0 && t.Year() >= 1970 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0
}
func mongoUnknown(e error) bool {
	if e == nil {
		return false
	}
	if errors.Is(e, &transaction.Error{Kind: transaction.CommitUnknown}) || e.Error() == string(transaction.CommitUnknown) {
		return true
	}
	if joined, ok := e.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if mongoUnknown(child) {
				return true
			}
		}
	}
	if wrapped, ok := e.(interface{ Unwrap() error }); ok {
		return mongoUnknown(wrapped.Unwrap())
	}
	return false
}
func mongoOutcome(e error) error {
	if mongoUnknown(e) {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	for _, code := range []string{"MONGODB_SLOT_RELEASE_UNCERTAIN", "MONGODB_CLOSE_FAILED"} {
		if mongoHasCode(e, code) {
			return failure(code)
		}
	}
	return datasetRuntimeError(e)
}
func mongoHasCode(e error, code string) bool {
	if e == nil {
		return false
	}
	if e.Error() == code {
		return true
	}
	if joined, ok := e.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if mongoHasCode(child, code) {
				return true
			}
		}
	}
	if wrapped, ok := e.(interface{ Unwrap() error }); ok {
		return mongoHasCode(wrapped.Unwrap(), code)
	}
	return false
}

type mongoGrantSnapshot struct {
	Version          int                  `json:"version"`
	Source           config.MongoDBSource `json:"source"`
	CredentialSHA256 string               `json:"credential_sha256"`
	Epoch            int64                `json:"epoch"`
	Membership       *string              `json:"membership"`
}

// The current selected transaction has already locked identity and Workspace.
// No secret contents leave CaptureSource; JSON contains only private provenance.
func (s *Service) mongoGrant(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, source config.MongoDBSource, captured *mongodatasets.SnapshotSource) ([]byte, error) {
	current, e := s.mongoSource(w, source.ID)
	if e != nil {
		return nil, e
	}
	if !reflect.DeepEqual(current, source) {
		return nil, failure("MONGODB_SOURCE_CHANGED")
	}

	fresh, e := mongodatasets.CaptureSource(current, s.Identity.Config.MongoDB)
	if e != nil {
		return nil, e
	}
	if captured != nil && captured.Fingerprint() != fresh.Fingerprint() {
		return nil, failure("MONGODB_SOURCE_CHANGED")
	}
	var membership *string
	if q.Dialect() == transaction.Dameng && !isAdmin(a) {
		var value string
		e = q.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT "AUTHORITY_ID" FROM "WORKSPACE_MEMBERSHIPS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "USER_ID"=?`, w.TenantID, w.ID, a.Me.User.ID)).Scan(&value)
		if e != nil {
			return nil, e
		}
		membership = &value
	}
	if q.Dialect() == transaction.PostgreSQL {
		membership, e = delegatedAuthority(ctx, q, a, w)
		if e != nil {
			return nil, e
		}
		// The strict JSON owner rejects null. Empty is the fixed platform-admin
		// sentinel; delegated identities retain their actual membership UUID.
		if membership == nil {
			value := ""
			membership = &value
		}
	}
	return json.Marshal(mongoGrantSnapshot{1, current, fresh.Fingerprint(), a.Epoch, membership})
}
func (s *Service) mongoRecheckReceipt(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, r MongoDBImport) error {
	source, e := s.mongoSource(w, r.SourceID)
	if e != nil {
		return e
	}
	raw, e := s.mongoGrant(ctx, q, a, w, source, nil)
	if e != nil {
		return e
	}
	var saved mongoGrantSnapshot
	if wire.DecodeStrict(r.sourceGrant, &saved) == nil && saved.Version == 1 {
		if !wire.ValidDigest(saved.CredentialSHA256) {
			return failure("DATASET_DATA_INVALID")
		}
		var current mongoGrantSnapshot
		if wire.DecodeStrict(raw, &current) != nil || !reflect.DeepEqual(current, saved) {
			return failure("MONGODB_SOURCE_CHANGED")
		}
		if q.Dialect() == transaction.Dameng && !mongoTimestamp(r.UpdatedAt) {
			return failure("DATASET_DATA_INVALID")
		}
	} else {
		var saved config.MongoDBSource
		if wire.DecodeStrict(r.sourceGrant, &saved) != nil || !reflect.DeepEqual(saved, source) {
			return failure("MONGODB_SOURCE_CHANGED")
		}
	}
	return nil
}

func (s *Service) mongoCapture(source config.MongoDBSource) (*mongodatasets.SnapshotSource, error) {
	repo, e := s.repository()
	if e != nil {
		return nil, e
	}
	if repo.Dialect() == transaction.Dameng {
		return mongodatasets.CaptureSource(source, s.Identity.Config.MongoDB)
	}
	return nil, nil
}
func (s *Service) mongoRead(ctx context.Context, source config.MongoDBSource, captured *mongodatasets.SnapshotSource, mapping mongodatasets.Mapping, preflight bool) (mongodatasets.Result, error) {
	if captured != nil {
		return captured.Read(ctx, mapping, s.policy().Limits, preflight)
	}
	return mongodatasets.Read(ctx, source, s.Identity.Config.MongoDB, mapping, s.policy().Limits, preflight)
}
func (s *Service) mongoSourceBinding(ctx context.Context, a identity.Auth, workspace, dataset string, source config.MongoDBSource, captured *mongodatasets.SnapshotSource) ([]byte, error) {
	var out []byte
	e := s.mongoTransaction(ctx, a, workspace, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		d, e := lockedDataset(ctx, q, w, dataset)
		if e != nil {
			return e
		}
		if d.RetiredAt != nil {
			return failure("DATASET_RETIRED")
		}
		out, e = s.mongoGrant(ctx, q, a, w, source, captured)
		return e
	})
	if e != nil {
		return nil, mongoOutcome(e)
	}
	return out, nil
}
func mongoLockedReceipt(ctx context.Context, q transaction.Executor, w workspaces.Workspace, dataset, actor, id string) (MongoDBImport, error) {
	return scanMongoImport(q.QueryRow(ctx, mongoSQL(q, "SELECT "+mongoFields(q)+" FROM mongodb_dataset_imports WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND requested_by=$4 AND id=$5 FOR UPDATE",
		`SELECT `+mongoFields(q)+` FROM "MONGODB_DATASET_IMPORTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_ID"=? AND "REQUESTED_BY"=? AND "ID"=? FOR UPDATE`,
		[]any{w.TenantID, w.ID, dataset, actor, id}, []any{w.TenantID, w.ID, dataset, actor, id})))
}
func (s *Service) mongoAttachmentWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, dataset string, expected MongoDBImport) (MongoDBImport, error) {
	d, e := lockedDataset(ctx, q, w, dataset)
	if e != nil {
		return MongoDBImport{}, e
	}
	if d.RetiredAt != nil {
		return MongoDBImport{}, failure("DATASET_RETIRED")
	}
	r, e := mongoLockedReceipt(ctx, q, w, dataset, a.Me.User.ID, expected.ID)
	if e != nil {
		return MongoDBImport{}, e
	}
	if e = s.mongoRecheckReceipt(ctx, q, a, w, r); e != nil {
		return MongoDBImport{}, e
	}
	if r.attemptID != expected.attemptID || r.objectID != expected.objectID || !r.busy || r.State != "exported" {
		return MongoDBImport{}, failure("DATASET_ATTEMPT_STALE")
	}
	if r.SHA256 == nil || r.Bytes == nil || expected.SHA256 == nil || expected.Bytes == nil || *r.SHA256 != *expected.SHA256 || *r.Bytes != *expected.Bytes {
		return MongoDBImport{}, failure("DATASET_SOURCE_CHANGED")
	}
	return r, nil
}
func (s *Service) mongoCheckAttachment(ctx context.Context, a identity.Auth, workspace, dataset string, r MongoDBImport) error {
	return s.mongoTransaction(ctx, a, workspace, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		_, e := s.mongoAttachmentWithin(ctx, q, a, w, dataset, r)
		return e
	})
}
func (s *Service) mongoBindDraft(ctx context.Context, a identity.Auth, workspace, dataset string, r MongoDBImport, draft string) error {
	return s.mongoTransaction(ctx, a, workspace, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.mongoAttachmentWithin(ctx, q, a, w, dataset, r)
		if e != nil {
			return e
		}
		if current.DraftID != nil && *current.DraftID != draft {
			return failure("DATASET_SOURCE_CHANGED")
		}
		var digest, creator, key string
		var size int64
		e = q.QueryRow(ctx, mongoSQL(q, "SELECT source_sha256,expected_bytes,created_by::text,creation_key::text FROM dataset_drafts WHERE tenant_id=$1 AND workspace_id=$2 AND dataset_id=$3 AND id=$4 FOR UPDATE",
			`SELECT "SOURCE_SHA256","EXPECTED_BYTES","CREATED_BY","CREATION_KEY" FROM "DATASET_DRAFTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_ID"=? AND "ID"=? FOR UPDATE`,
			[]any{w.TenantID, w.ID, dataset, draft}, []any{w.TenantID, w.ID, dataset, draft})).Scan(&digest, &size, &creator, &key)
		if e != nil {
			return e
		}
		if digest != *r.SHA256 || size != *r.Bytes || creator != a.Me.User.ID || key != r.ID {
			return failure("DATASET_SOURCE_CHANGED")
		}
		n, e := q.Exec(ctx, mongoSQL(q, "UPDATE mongodb_dataset_imports SET draft_id=$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND dataset_id=$4 AND requested_by=$5 AND id=$6 AND attempt_id=$7 AND state='exported' AND lease_expires_at>clock_timestamp() AND (draft_id IS NULL OR draft_id=$1)",
			`UPDATE "MONGODB_DATASET_IMPORTS" SET "DRAFT_ID"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_ID"=? AND "REQUESTED_BY"=? AND "ID"=? AND "ATTEMPT_ID"=? AND "STATE"='exported' AND "LEASE_EXPIRES_AT">SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) AND ("DRAFT_ID" IS NULL OR "DRAFT_ID"=?)`,
			[]any{draft, w.TenantID, w.ID, dataset, a.Me.User.ID, r.ID, r.attemptID}, []any{draft, w.TenantID, w.ID, dataset, a.Me.User.ID, r.ID, r.attemptID, draft}))
		if e != nil {
			return e
		}
		if n != 1 {
			return failure("DATASET_ATTEMPT_STALE")
		}
		return nil
	})
}
func (s *Service) mongoRecordFailure(ctx context.Context, a identity.Auth, workspace, dataset string, r MongoDBImport, code string) error {
	return s.mongoTransaction(ctx, a, workspace, true, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedDataset(ctx, q, w, dataset); e != nil {
			return e
		}
		n, e := q.Exec(ctx, mongoSQL(q, "UPDATE mongodb_dataset_imports SET state='failed',lease_expires_at=NULL,error_code=$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND dataset_id=$4 AND requested_by=$5 AND id=$6 AND attempt_id=$7 AND state<>'imported'",
			`UPDATE "MONGODB_DATASET_IMPORTS" SET "STATE"='failed',"LEASE_EXPIRES_AT"=NULL,"ERROR_CODE"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "DATASET_ID"=? AND "REQUESTED_BY"=? AND "ID"=? AND "ATTEMPT_ID"=? AND "STATE"<>'imported'`,
			[]any{code, w.TenantID, w.ID, dataset, a.Me.User.ID, r.ID, r.attemptID}, []any{code, w.TenantID, w.ID, dataset, a.Me.User.ID, r.ID, r.attemptID}))
		if e != nil {
			return e
		}
		if n != 1 {
			return failure("DATASET_ATTEMPT_STALE")
		}
		return nil
	})
}

var mongoObjectIDPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

func mongoReceiptValid(r MongoDBImport) bool {
	if !wire.ValidID(r.ID) || !wire.ValidID(r.CreationKey) || !wire.ValidID(r.attemptID) || !wire.ValidID(r.objectID) || len(r.intent) != 32 || r.AttemptCount < 1 || r.AttemptCount > 20 || len(r.retained) != r.AttemptCount || r.retained[len(r.retained)-1] != r.objectID || r.SourceID != r.Request.SourceID || !validReason(r.Request.Reason) || len(r.sourceGrant) == 0 || len(r.sourceGrant) > 65536 {
		return false
	}
	if r.DraftID != nil && !wire.ValidID(*r.DraftID) {
		return false
	}
	proof := r.SHA256 != nil && r.Bytes != nil && r.Rows != nil && r.FirstID != nil && r.LastID != nil
	absent := r.SHA256 == nil && r.Bytes == nil && r.Rows == nil && r.FirstID == nil && r.LastID == nil
	if !proof && !absent {
		return false
	}
	if proof && (!wire.ValidDigest(*r.SHA256) || *r.Bytes <= 0 || *r.Rows <= 0 || !mongoObjectIDPattern.MatchString(*r.FirstID) || !mongoObjectIDPattern.MatchString(*r.LastID) || *r.FirstID > *r.LastID) {
		return false
	}
	if r.DraftID != nil && !proof {
		return false
	}
	switch r.State {
	case "reading":
		return absent && r.DraftID == nil && r.ErrorCode == nil
	case "exported":
		return proof && r.ErrorCode == nil
	case "imported":
		return proof && r.DraftID != nil && r.ErrorCode == nil && !r.busy
	case "failed":
		return r.ErrorCode != nil && !r.busy
	default:
		return false
	}
}
