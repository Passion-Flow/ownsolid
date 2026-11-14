package models

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const modelCancellationPending = "MODEL_CANCELLATION_PENDING"
const modelImportLockIdentityPrefix = "model-import:v1:"
const mysqlImportLockPrefix = "projectone:mi:v1:"
const mysqlImportLockDigestBytes = 20

func importCancellationPending(value Import) bool {
	return value.ErrorCode != nil && *value.ErrorCode == modelCancellationPending
}

func importMutationError(e error) error {
	if e == nil {
		return nil
	}
	if errors.Is(e, context.Canceled) {
		return fail(string(transaction.Cancelled))
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return fail(string(transaction.Timeout))
	}
	switch e.Error() {
	case "MODEL_IMPORT_BUSY", "MODEL_IMPORT_STATE_CONFLICT", "MODEL_CLEANUP_CONFLICT", "MODEL_PATH_INVALID", "MODEL_IO_FAILED", "MODEL_STORAGE_UNAVAILABLE",
		"MODEL_ALREADY_EXISTS", "IDEMPOTENCY_CONFLICT", "MODEL_CHUNK_INVALID", "MODEL_CHUNK_CONFLICT", "MODEL_CHUNK_OFFSET_MISMATCH", "MODEL_IMPORT_INCOMPLETE":
		return fail(e.Error())
	default:
		return modelReadError(e)
	}
}

func scopedImport(ctx context.Context, q transaction.Executor, tenant, workspace, id string) (Import, error) {
	return scanImport(q.QueryRow(ctx, modelNativeSQL(q, "SELECT "+nativeImportFields+" FROM MODEL_IMPORTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=? FOR UPDATE",
		"SELECT "+importFields+" FROM model_imports WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE",
		"SELECT "+mysqlImportFields+" FROM model_imports WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE",
		tenant, workspace, id)))
}

func (s *Service) importMutationOn(ctx context.Context, store transaction.Store, a identity.Auth, workspace string, operation func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if !admin(a) {
		return fail("ACCESS_DENIED")
	}
	e := store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" {
			return fail("ACCESS_DENIED")
		}
		return operation(ctx, q, w)
	})
	return importMutationError(e)
}

func importAuditWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, id, reason, request string) error {
	_, e := modelExecOne(ctx, q, modelNativeSQL(q, `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,ACTOR_USER_ID,TENANT_ID,WORKSPACE_ID,AUDIENCE,ACTION,OUTCOME,TARGET_TYPE,TARGET_ID,REASON,REQUEST_ID) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,'admin',?,'success','model_import',?,NULLIF(?,''),?)`,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin',$5,'success','model_import',$6,NULLIF($7,''),$8)`,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin',?,'success','model_import',?,NULLIF(?,''),?)`,
		uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, action, id, reason, request))
	return e
}

// A callback is serialized across transactions. The surrounding file lock is
// independent of DB session loss; physical disposal is the last safety boundary.
func (s *Service) withImportSession(ctx context.Context, id string, operation func(context.Context, transaction.Store) error) error {
	store, e := s.repository()
	if e != nil {
		return e
	}
	owner, ok := store.(transaction.SessionStore)
	if !ok {
		return fail("MODEL_STORAGE_UNAVAILABLE")
	}
	return owner.WithSession(ctx, func(ctx context.Context, session transaction.Store) (result error) {
		if session.Dialect() == transaction.Dameng {
			return s.withNativeImportLock(ctx, session, id, operation)
		}
		var key any
		if session.Dialect() == transaction.MySQL {
			var database string
			if e := session.QueryRow(ctx, transaction.SQL(transaction.MySQL, `SELECT DATABASE()`)).Scan(&database); e != nil {
				return e
			}
			if database == "" {
				return fail("STORAGE_UNAVAILABLE")
			}
			digest := sha256.Sum256([]byte(database + "\x00" + id))
			key = mysqlImportLockPrefix + hex.EncodeToString(digest[:mysqlImportLockDigestBytes])
		} else {
			digest := sha256.Sum256([]byte(modelImportLockIdentityPrefix + id))
			key = int64(binary.LittleEndian.Uint64(digest[:8]))
		}
		var locked bool
		if e := session.QueryRow(ctx, modelSQL(session, `SELECT pg_try_advisory_lock($1)`, `SELECT GET_LOCK(?,0)`, key)).Scan(&locked); e != nil {
			return e
		}
		if !locked {
			return fail("MODEL_IMPORT_BUSY")
		}
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), config.Seconds(s.Identity.Config.Policy().Models.CleanupSeconds))
			defer cancel()
			var released bool
			e := session.QueryRow(cleanup, modelSQL(session, `SELECT pg_advisory_unlock($1)`, `SELECT RELEASE_LOCK(?)`, key)).Scan(&released)
			if result == nil && (e != nil || !released) {
				result = fail("STORAGE_UNAVAILABLE")
			}
		}()
		return operation(ctx, session)
	})
}

// DBMS_LOCK release_on_commit=FALSE preserves this import fence across each
// real transaction. Only the shared SessionStore may own/dispose its connection.
func (s *Service) withNativeImportLock(ctx context.Context, session transaction.Store, id string, operation func(context.Context, transaction.Store) error) (result error) {
	var schema string
	if e := session.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT SYS_CONTEXT('USERENV','CURRENT_SCHEMA') FROM DUAL`)).Scan(&schema); e != nil {
		return e
	}
	if schema == "" || !validID(id) {
		return fail("MODEL_STORAGE_UNAVAILABLE")
	}
	digest := sha256.Sum256([]byte(modelImportLockIdentityPrefix + schema + "\x00" + id))
	// Native DBMS_LOCK user IDs occupy the documented unsigned 30-bit namespace.
	// Hash collisions serialize unrelated imports; they never weaken a fence.
	key := int64(binary.BigEndian.Uint32(digest[:4]) & 0x3fffffff)
	var code int
	if e := session.QueryRow(ctx, transaction.SQL(transaction.Dameng, `SELECT DBMS_LOCK.REQUEST(?,6,0,FALSE) FROM DUAL`, key)).Scan(&code); e != nil {
		return e
	}
	if code == 1 {
		return fail("MODEL_IMPORT_BUSY")
	}
	if code != 0 {
		return fail("MODEL_STORAGE_UNAVAILABLE")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), config.Seconds(s.Identity.Config.Policy().Models.CleanupSeconds))
		defer cancel()
		var released int
		e := session.QueryRow(cleanup, transaction.SQL(transaction.Dameng, `SELECT DBMS_LOCK.RELEASE(?) FROM DUAL`, key)).Scan(&released)
		// Commit uncertainty is never replaced with a secondary cleanup failure.
		if result == nil && (e != nil || released != 0) {
			result = fail("MODEL_STORAGE_UNAVAILABLE")
		}
	}()
	return operation(ctx, session)
}
