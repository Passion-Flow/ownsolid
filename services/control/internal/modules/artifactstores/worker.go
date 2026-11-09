package artifactstores

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	"github.com/google/uuid"
)

// ProcessNext performs one durable prepared effect. Sent/unknown operations are
// never automatically reissued after process interruption; Reconcile owns them.
func (s *Service) ProcessNext(ctx context.Context) (worked bool, err error) {
	defer func() {
		if err != nil {
			worked = false
		}
	}()
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return false, admissionError
	}
	defer finish()
	r, e := s.repository()
	if e != nil {
		return false, e
	}
	bounded, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	var candidate storedObject
	selected := false
	defer func() {
		if selected && err != nil {
			err = workerFailureResult(err, s.reduceFailureBounded(candidate, err))
		}
	}()
	e = r.Within(bounded, true, func(ctx context.Context, q transaction.Executor) error {
		var e error
		candidate, e = scanObject(q.QueryRow(ctx, statement(q, "SELECT "+pgObjectFields+" FROM artifact_objects WHERE tenant_id=$1 AND store_id=$2 AND operation_phase='prepared' AND operation_kind IN ('create','complete','delete','abort','ingest') AND error_code IS NULL ORDER BY created_at,id LIMIT 1", "SELECT "+myObjectFields+" FROM artifact_objects WHERE tenant_id=? AND store_id=? AND operation_phase='prepared' AND operation_kind IN ('create','complete','delete','abort','ingest') AND error_code IS NULL ORDER BY created_at,id LIMIT 1", `SELECT `+dmObjectFields+` FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "STORE_ID"=? AND "OPERATION_PHASE"='prepared' AND "OPERATION_KIND" IN ('create','complete','delete','abort','ingest') AND "ERROR_CODE" IS NULL ORDER BY "CREATED_AT","ID" LIMIT 1`, s.Stage.Tenant(), s.Stage.Namespace())))
		return e
	})
	cancel()
	if e != nil {
		if e.Error() == "RESOURCE_NOT_FOUND" {
			return false, nil
		}
		return false, safe(e)
	}
	selected = true
	a, e := actorFrom(candidate.OperationAuthority)
	if e != nil {
		return true, e
	}
	if e = s.admitAcceptedWork(ctx, a, candidate.WorkspaceID); e != nil {
		return true, e
	}
	// Refresh a prepared operation's execution window only after current actor and
	// object fences. Prepared means no remote request has yet been committed sent.
	e = s.within(ctx, a, candidate.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, candidate.ID)
		if e != nil {
			return e
		}
		if current.OperationID == nil || candidate.OperationID == nil || *current.OperationID != *candidate.OperationID || current.OperationPhase == nil || *current.OperationPhase != "prepared" {
			return fail("OBJECT_OPERATION_STALE")
		}
		pending := current.OperationKind != nil && (*current.OperationKind == "create" || *current.OperationKind == "complete")
		if current.OperationKind != nil && *current.OperationKind == "ingest" {
			if !writeRole(a, w) {
				return fail("ACCESS_DENIED")
			}
			// This only claims the exact accepted job. Current exact grant authority is
			// rechecked by processIngestion before any source or target IO.
		} else if e = s.writeObject(ctx, q, a, w, current, pending); e != nil {
			return e
		}
		kind := *current.OperationKind
		if kind == "create" || kind == "complete" {
			if e = liveWithin(ctx, q, current, true); e != nil {
				return e
			}
		}
		_, e = q.Exec(ctx, statement(q, "UPDATE artifact_objects SET operation_phase='sent',operation_expires_at=clock_timestamp()+make_interval(secs=>$5),updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 AND operation_id=$4", "UPDATE artifact_objects SET operation_phase='sent',operation_expires_at=TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=? AND operation_id=?", `UPDATE "ARTIFACT_OBJECTS" SET "OPERATION_PHASE"='sent',"OPERATION_EXPIRES_AT"=(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND')),"UPDATED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? AND "OPERATION_ID"=?`, workerSentArgs(q, current, s.Configuration.TransferSeconds)...))
		return e
	})
	if e != nil {
		return true, e
	}
	run, stop := context.WithTimeout(ctx, time.Duration(s.Configuration.TransferSeconds)*time.Second)
	defer stop()
	kind := *candidate.OperationKind
	var upload, etag *string
	state := ""
	switch kind {
	case "ingest":
		e = s.processIngestion(run, a, candidate)
		return true, e
	case "create":
		v, e := s.Transport.Create(run, candidate.ref(), metadata(candidate))
		if e != nil {
			return true, safe(e)
		}
		upload = &v.ID
		state = "uploading"
	case "complete":
		var candidateParts []objectstore.Part
		e = s.within(run, a, candidate.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
			current, e := s.objectWithin(ctx, q, w, candidate.ID)
			if e != nil {
				return e
			}
			if e = s.sameOperation(ctx, q, a, w, current, candidate, true); e != nil {
				return e
			}
			parts, e := s.partsWithin(ctx, q, current, true)
			if e != nil {
				return e
			}
			candidateParts = parts
			return nil
		})
		if e != nil {
			return true, e
		}
		if candidate.UploadID == nil {
			return true, fail("OBJECT_UPLOAD_INVALID")
		}
		_, e = s.Transport.Complete(run, candidate.ref(), *candidate.UploadID, candidateParts)
		if e != nil {
			return true, safe(e)
		}
		i, e := s.verifyWhole(run, candidate)
		if e != nil {
			return true, e
		}
		etag = &i.ETag
		upload = candidate.UploadID
		state = "ready"
	case "delete":
		if candidate.ETag == nil {
			return true, fail("OBJECT_NOT_READY")
		}
		if e = s.Transport.Delete(run, candidate.ref(), *candidate.ETag); e != nil {
			return true, safe(e)
		}
		if _, e = s.Transport.Head(run, candidate.ref()); e == nil || e.Error() != "OBJECT_NOT_FOUND" {
			if e == nil {
				e = fail("OBJECT_REMOTE_OUTCOME_UNKNOWN")
			}
			return true, safe(e)
		}
		state = "deleted"
		upload = candidate.UploadID
		etag = candidate.ETag
	case "abort":
		if candidate.UploadID == nil {
			err := fail("OBJECT_RECONCILE_REQUIRED")
			return true, err
		}
		if e = s.Transport.Abort(run, candidate.ref(), *candidate.UploadID); e != nil {
			return true, safe(e)
		}
		if _, e = s.Transport.Parts(run, candidate.ref(), *candidate.UploadID); e == nil || e.Error() != "OBJECT_UPLOAD_NOT_FOUND" {
			if e == nil {
				e = fail("OBJECT_REMOTE_OUTCOME_UNKNOWN")
			}
			return true, safe(e)
		}
		if _, e = s.Transport.Head(run, candidate.ref()); e == nil || e.Error() != "OBJECT_NOT_FOUND" {
			err := fail("OBJECT_RECONCILE_REQUIRED")
			return true, err
		}

		state = "aborted"
		upload = candidate.UploadID
	default:
		return true, fail("OBJECT_OPERATION_STALE")
	}
	if e = s.admitAcceptedWork(run, a, candidate.WorkspaceID); e != nil {
		return true, e
	}
	e = s.within(run, a, candidate.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		current, e := s.objectWithin(ctx, q, w, candidate.ID)
		if e != nil {
			return e
		}
		checkExpiry := kind == "create" || kind == "complete"
		if checkExpiry {
			if e = s.sameOperation(ctx, q, a, w, current, candidate, true); e != nil {
				return e
			}
		} else {
			if e = s.writeObject(ctx, q, a, w, current, false); e != nil {
				return e
			}
			if current.OperationID == nil || candidate.OperationID == nil || *current.OperationID != *candidate.OperationID {
				return fail("OBJECT_OPERATION_STALE")
			}
		}
		if e = clearOperation(ctx, q, current, state, upload, etag); e != nil {
			return e
		}
		return audit(ctx, q, a, w, "object."+state, current.ID, "Reconcile accepted exact object operation", uuid.NewString())
	})
	if e != nil {
		return true, e
	}
	return true, nil
}
func workerSentArgs(q transaction.Executor, o storedObject, seconds int) []any {
	if positionalArguments(q) {
		return []any{seconds, o.Tenant, o.WorkspaceID, o.ID, *o.OperationID}
	}
	return []any{o.Tenant, o.WorkspaceID, o.ID, *o.OperationID, seconds}
}
func (s *Service) reduceFailureBounded(o storedObject, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	return s.reduceFailure(ctx, o, cause)
}
func (s *Service) verifyWhole(ctx context.Context, o storedObject) (objectstore.Info, error) {
	return s.verifyWholeTo(ctx, o, "", io.Discard)
}

func (s *Service) verifyWholeTo(ctx context.Context, o storedObject, expectedETag string, destination io.Writer) (info objectstore.Info, err error) {
	i, e := s.Transport.Head(ctx, o.ref())
	if e != nil {
		return info, safe(e)
	}
	if !infoMatches(o, i) {
		return info, fail("OBJECT_DIGEST_MISMATCH")
	}
	if expectedETag != "" && i.ETag != expectedETag {
		return info, fail("OBJECT_CHANGED")
	}
	d, e := s.Transport.Open(ctx, o.ref(), i.ETag)
	if e != nil {
		return info, safe(e)
	}
	closed := false
	defer func() {
		if !closed {
			d.Body.Close()
		}
	}()
	if !infoMatches(o, d.Info) || d.Info.ETag != i.ETag {
		return info, fail("OBJECT_CHANGED")
	}
	h := sha256.New()
	count, e := io.Copy(io.MultiWriter(h, destination), io.LimitReader(d.Body, o.ExpectedBytes+1))
	if e != nil {
		return info, safe(e)
	}
	if count != o.ExpectedBytes || hex.EncodeToString(h.Sum(nil)) != o.SHA256 {
		return info, fail("OBJECT_DIGEST_MISMATCH")
	}
	// Open retains a transfer reservation until the body closes. Release it
	// before any further remote observation, including on single-slot profiles.
	e = d.Body.Close()
	closed = true
	if e != nil {
		return info, safe(e)
	}
	after, e := s.Transport.Head(ctx, o.ref())
	if e != nil {
		return info, safe(e)
	}
	if !infoMatches(o, after) || after.ETag != i.ETag {
		return info, fail("OBJECT_CHANGED")
	}
	if e = s.Transport.PrivateObject(ctx, o.ref()); e != nil {
		return info, safe(e)
	}
	return after, nil
}
