package models

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Passion-Flow/ownsolid/api/agent/modelartifact"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type workerImport struct {
	Import
	TenantID, ActorID, Reason string
	Epoch                     int64
}

func workerImportRow(ctx context.Context, q transaction.Executor, id string, scope *workerImport) (workerImport, error) {
	pg := "SELECT " + importFields + ",tenant_id::text,requested_by::text,requested_epoch,reason FROM model_imports WHERE id=$1"
	my := "SELECT " + mysqlImportFields + ",tenant_id,requested_by,requested_epoch,reason FROM model_imports WHERE id=?"
	dm := "SELECT " + nativeImportFields + ",TENANT_ID,REQUESTED_BY,REQUESTED_EPOCH,REASON FROM MODEL_IMPORTS WHERE ID=?"
	args := []any{id}
	if scope != nil {
		pg += " AND tenant_id=$2 AND workspace_id=$3 FOR UPDATE"
		my += " AND tenant_id=? AND workspace_id=? FOR UPDATE"
		dm += " AND TENANT_ID=? AND WORKSPACE_ID=? FOR UPDATE"
		args = append(args, scope.TenantID, scope.WorkspaceID)
	}
	var v workerImport
	e := q.QueryRow(ctx, modelNativeSQL(q, dm, pg, my, args...)).Scan(&v.ID, &v.WorkspaceID, &v.Name, &v.CatalogDigest, &v.ExpectedBytes, &v.UploadedBytes, &v.State, &v.ErrorCode, &v.ModelID, &v.Attempts, &v.CreatedAt, &v.UpdatedAt, &v.TenantID, &v.ActorID, &v.Epoch, &v.Reason)
	if errors.Is(e, sql.ErrNoRows) {
		return workerImport{}, fail("MODEL_IMPORT_STATE_CONFLICT")
	}
	return v, e
}

func sameWorkerBinding(a, b workerImport) bool {
	return a.ID == b.ID && a.TenantID == b.TenantID && a.WorkspaceID == b.WorkspaceID && a.ActorID == b.ActorID && a.Epoch == b.Epoch && a.Name == b.Name && a.Reason == b.Reason && a.CatalogDigest == b.CatalogDigest && a.ExpectedBytes == b.ExpectedBytes && a.UploadedBytes == b.UploadedBytes
}

// A durable queued command is authorized by current actor/epoch and scope, not
// a fabricated browser session. Failure bookkeeping may observe inactive rows;
// its callback receives the actual publication-authorization result explicitly.
func (s *Service) workerWithin(ctx context.Context, session transaction.Store, id string, fn func(transaction.Executor, workerImport, bool) error) error {
	return session.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		preview, e := workerImportRow(ctx, q, id, nil)
		if e != nil {
			return e
		}
		for _, value := range []string{preview.ID, preview.TenantID, preview.WorkspaceID, preview.ActorID} {
			if !validID(value) || value == uuid.Nil.String() {
				return fail("MODEL_IMPORT_INCOMPLETE")
			}
		}
		if preview.Epoch < 0 {
			return fail("MODEL_ACTOR_UNAVAILABLE")
		}
		if e = s.Identity.LockSessionUsersWithin(ctx, q, preview.ActorID, "", "admin"); e != nil {
			return e
		}
		var epoch int64
		var actorStatus string
		var admin bool
		if e = q.QueryRow(ctx, modelNativeSQL(q, `SELECT AUTH_EPOCH,STATUS,IS_PLATFORM_ADMIN FROM USERS WHERE ID=?`, `SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=$1`, `SELECT auth_epoch,status,is_platform_admin FROM users WHERE id=?`, preview.ActorID)).Scan(&epoch, &actorStatus, &admin); e != nil {
			return e
		}
		var workspaceStatus string
		var tenantActive bool
		e = q.QueryRow(ctx, modelNativeSQL(q, `SELECT W.STATUS,CASE WHEN T.STATUS='active' THEN 1 ELSE 0 END FROM WORKSPACES W JOIN INSTALLATION_STATE I ON I.TENANT_ID=W.TENANT_ID JOIN TENANTS T ON T.ID=W.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND W.ID=? AND W.TENANT_ID=? FOR UPDATE`,
			`SELECT w.status,t.status='active' FROM workspaces w JOIN installation_state i ON i.tenant_id=w.tenant_id JOIN tenants t ON t.id=w.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND w.id=$1 AND w.tenant_id=$2 FOR NO KEY UPDATE OF w`,
			`SELECT w.status,(SELECT t.status='active' FROM tenants t WHERE t.id=w.tenant_id) FROM workspaces w WHERE w.id=? AND w.tenant_id=? AND EXISTS(SELECT 1 FROM installation_state i WHERE i.id=1 AND i.bootstrap_complete=1 AND i.tenant_id=w.tenant_id) FOR UPDATE`, preview.WorkspaceID, preview.TenantID)).Scan(&workspaceStatus, &tenantActive)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("MODEL_ACTOR_UNAVAILABLE")
		}
		if e != nil {
			return e
		}
		current, e := workerImportRow(ctx, q, id, &preview)
		if e != nil {
			return e
		}
		if !sameWorkerBinding(preview, current) {
			return fail("MODEL_IMPORT_STATE_CONFLICT")
		}
		return fn(q, current, actorStatus == "active" && admin && epoch == current.Epoch && tenantActive && workspaceStatus == "active")
	})
}

func workerAudit(ctx context.Context, q transaction.Executor, current workerImport, action, outcome, reason string) error {
	_, e := modelExecOne(ctx, q, modelNativeSQL(q, `INSERT INTO AUDIT_EVENTS(ID,OCCURRED_AT,ACTOR_USER_ID,TENANT_ID,WORKSPACE_ID,AUDIENCE,ACTION,OUTCOME,TARGET_TYPE,TARGET_ID,REASON,REQUEST_ID) VALUES(?,CURRENT_TIMESTAMP(6),?,?,?,'admin',?,?,'model_import',?,NULLIF(?,''),?)`,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,'admin',$5,$6,'model_import',$7,NULLIF($8,''),$9)`,
		`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,'admin',?,?,'model_import',?,NULLIF(?,''),?)`, uuid.NewString(), current.ActorID, current.TenantID, current.WorkspaceID, action, outcome, current.ID, reason, uuid.NewString()))
	return e
}

// The import row is already locked. An immutable object alone proves no SQL
// outcome; exact model/import/audit identity must agree before any replay.
func publicationReady(ctx context.Context, q transaction.Executor, current workerImport) (bool, error) {
	var count, matching int
	if e := q.QueryRow(ctx, modelNativeSQL(q, `SELECT COUNT(*),COUNT(CASE WHEN ACTOR_USER_ID=? THEN 1 END) FROM AUDIT_EVENTS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND TARGET_TYPE='model_import' AND TARGET_ID=? AND ACTION='model.import_ready' AND OUTCOME='success'`,
		`SELECT COUNT(*),COUNT(CASE WHEN actor_user_id=$1 THEN 1 END) FROM audit_events WHERE tenant_id=$2 AND workspace_id=$3 AND target_type='model_import' AND target_id=$4 AND action='model.import_ready' AND outcome='success'`,
		`SELECT COUNT(*),COUNT(CASE WHEN actor_user_id=? THEN 1 END) FROM audit_events WHERE tenant_id=? AND workspace_id=? AND target_type='model_import' AND target_id=? AND action='model.import_ready' AND outcome='success'`, current.ActorID, current.TenantID, current.WorkspaceID, current.ID)).Scan(&count, &matching); e != nil {
		return false, e
	}
	var v Version
	var creator string
	var raw []byte
	e := q.QueryRow(ctx, modelNativeSQL(q, `SELECT ID,WORKSPACE_ID,NAME,MODEL_KEY,CATALOG_DIGEST,MANIFEST,STATE,CREATED_BY,KIND FROM MODEL_VERSIONS WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
		`SELECT id::text,workspace_id::text,name,model_key,catalog_digest,manifest,state,created_by::text,kind FROM model_versions WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`,
		`SELECT id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind FROM model_versions WHERE tenant_id=? AND workspace_id=? AND id=?`, current.TenantID, current.WorkspaceID, current.ID)).Scan(&v.ID, &v.WorkspaceID, &v.Name, &v.ModelKey, &v.CatalogDigest, &raw, &v.State, &creator, &v.Kind)
	if errors.Is(e, sql.ErrNoRows) {
		if count != 0 || current.ModelID != nil || current.State == "ready" {
			return false, fail("MODEL_PUBLICATION_CONFLICT")
		}
		return false, nil
	}
	if e != nil {
		return false, e
	}
	manifest := resolveImportManifest(current.CatalogDigest)
	if manifest == nil || current.State != "ready" || current.ModelID == nil || *current.ModelID != current.ID || count != 1 || matching != 1 || creator != current.ActorID || v.Name != current.Name || v.Kind != "approved_base" || v.ModelKey != manifest.catalog.ModelKey || v.CatalogDigest != current.CatalogDigest || v.State != "ready" && v.State != "deleted" || !manifest.matchesStored(raw) {
		return false, fail("MODEL_PUBLICATION_CONFLICT")
	}
	return true, nil
}

func (s *Service) claimImport(ctx context.Context, session transaction.Store, id string) (workerImport, bool, bool, error) {
	var original, claimed workerImport
	ready, eligible := false, false
	e := s.workerWithin(ctx, session, id, func(q transaction.Executor, current workerImport, authorized bool) error {
		original = current
		var e error
		ready, e = publicationReady(ctx, q, current)
		if e != nil {
			return e
		}
		if ready {
			claimed = current
			return nil
		}
		if importCancellationPending(current.Import) || current.State != "queued" && current.State != "verifying" {
			return nil
		}
		if !authorized {
			return fail("MODEL_ACTOR_UNAVAILABLE")
		}
		manifest := resolveImportManifest(current.CatalogDigest)
		if manifest == nil {
			return fail("MODEL_IMPORT_INCOMPLETE")
		}
		if current.State == "verifying" {
			if current.Attempts < 1 || current.Attempts > config.ModelImportSchemaMaximumAttempts {
				return fail("MODEL_RECOVERY_REQUIRED")
			}
			// This session holds the import's lock, so the process that ran the attempt
			// has stopped: it crashed, was stopped, or ran out of time.
			abandoned, e := s.abandonAttempt(current, manifest)
			if e != nil {
				return e
			}
			if !abandoned {
				claimed = current
				eligible = true
				return nil
			}
		}
		if current.Attempts >= s.Identity.Config.Policy().Models.MaximumAttempts {
			return fail("MODEL_RECOVERY_REQUIRED")
		}
		if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `UPDATE MODEL_IMPORTS SET STATE='verifying',ATTEMPTS=ATTEMPTS+1,ERROR_CODE=NULL,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
			`UPDATE model_imports SET state='verifying',attempts=attempts+1,error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`,
			`UPDATE model_imports SET state='verifying',attempts=attempts+1,error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, current.TenantID, current.WorkspaceID, current.ID)); e != nil {
			return e
		}
		if e = workerAudit(ctx, q, current, "model.import_verification_started", "success", current.Reason); e != nil {
			return e
		}
		claimed, e = workerImportRow(ctx, q, id, &current)
		eligible = e == nil
		return e
	})
	if e != nil {
		return original, false, false, e
	}
	return claimed, eligible, ready, nil
}

// abandonAttempt reads the stage a stopped attempt left. A stage that is empty,
// finished or foreign is taken up by the same attempt: verification extracts into an
// empty one, publishes a finished one after checking its digests, and refuses a
// foreign one as MODEL_STAGE_INVALID without touching it. A part-written stage is
// never resumed: its files are removed and the attempt counts as spent, so the
// caller starts the next attempt in a stage of its own, and an import that keeps
// stopping control ends as MODEL_RECOVERY_REQUIRED after the maximum attempts.
func (s *Service) abandonAttempt(current workerImport, manifest *importManifest) (bool, error) {
	if !validID(current.ID) {
		return false, fail("MODEL_PATH_INVALID")
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return false, e
	}
	defer root.Close()
	stage := stagePath(current.ID, current.Attempts)
	condition, e := classifyStage(root, stage, manifest)
	if e != nil || condition != stagePartial {
		return false, e
	}
	if e = removeStage(root, stage, manifest); e != nil {
		return false, e
	}
	slog.Warn("model_import_attempt_abandoned", "import_id", current.ID, "attempt", current.Attempts)
	return true, nil
}

func (s *Service) verifyImportFiles(ctx context.Context, session transaction.Store, current workerImport) error {
	manifest := resolveImportManifest(current.CatalogDigest)
	if !validID(current.ID) || manifest == nil || !manifest.admitsArchiveSize(current.ExpectedBytes) || current.UploadedBytes != current.ExpectedBytes {
		return fail("MODEL_IMPORT_INCOMPLETE")
	}
	root, e := openStore(s.RootDirectory)
	if e != nil {
		return e
	}
	defer root.Close()
	if e = directory(root, "objects"); e != nil {
		return e
	}
	final := "objects/" + current.ID
	if _, e = root.Lstat(final); e == nil {
		if e = manifest.verifyDirectory(ctx, filepath.Join(s.RootDirectory, final)); e != nil {
			return fail("MODEL_PUBLICATION_CONFLICT")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return fail("MODEL_IO_FAILED")
	}
	if e = directory(root, "imports"); e != nil {
		return e
	}
	scope := "imports/" + current.ID
	if e = directory(root, scope); e != nil {
		return e
	}
	stage := stagePath(current.ID, current.Attempts)
	if e = directory(root, stage); e != nil {
		return e
	}
	file, e := root.Open(stage)
	if e != nil {
		return fail("MODEL_IO_FAILED")
	}
	entries, e := file.ReadDir(1)
	file.Close()
	if e != nil && !errors.Is(e, io.EOF) {
		return fail("MODEL_IO_FAILED")
	}
	if len(entries) > 0 {
		if e = manifest.verifyDirectory(ctx, filepath.Join(s.RootDirectory, stage)); e != nil {
			return fail("MODEL_STAGE_INVALID")
		}
	} else {
		rows, e := session.Query(ctx, modelNativeSQL(session, `SELECT BYTE_OFFSET,SIZE_BYTES,SHA256 FROM MODEL_IMPORT_CHUNKS WHERE IMPORT_ID=? ORDER BY BYTE_OFFSET`, `SELECT byte_offset,size_bytes,sha256 FROM model_import_chunks WHERE import_id=$1 ORDER BY byte_offset`, `SELECT byte_offset,size_bytes,sha256 FROM model_import_chunks WHERE import_id=? ORDER BY byte_offset`, current.ID))
		if e != nil {
			return e
		}
		chunks := []chunk{}
		offset := int64(0)
		for rows.Next() {
			var c chunk
			if e = rows.Scan(&c.offset, &c.size, &c.digest); e != nil {
				rows.Close()
				return e
			}
			if c.offset != offset || c.size != min(ChunkBytes, current.ExpectedBytes-offset) || c.size < 1 || len(chunks) >= MaximumImportChunkCount {
				rows.Close()
				return fail("MODEL_IMPORT_INCOMPLETE")
			}
			offset += c.size
			chunks = append(chunks, c)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		if offset != current.ExpectedBytes {
			return fail("MODEL_IMPORT_INCOMPLETE")
		}
		source := &chunkReader{ctx: ctx, root: root, id: current.ID, chunks: chunks}
		defer source.Close()
		if e = manifest.reviewArchive(source, current.ExpectedBytes); e != nil {
			return e
		}
		if e = manifest.extractArchive(ctx, source, filepath.Join(s.RootDirectory, stage)); e != nil {
			return e
		}
	}
	if e = modelartifact.PublishDirectory(root, stage, final); e != nil {
		return fail("MODEL_IO_FAILED")
	}
	if e = syncDirectory(root, "objects"); e != nil {
		return e
	}
	return syncDirectory(root, scope)
}

func (s *Service) publishWorker(ctx context.Context, session transaction.Store, reference workerImport) error {
	return s.workerWithin(ctx, session, reference.ID, func(q transaction.Executor, current workerImport, authorized bool) error {
		if !sameWorkerBinding(reference, current) || reference.Attempts != current.Attempts {
			return fail("MODEL_IMPORT_STATE_CONFLICT")
		}
		ready, e := publicationReady(ctx, q, current)
		if e != nil || ready {
			return e
		}
		if !authorized {
			return fail("MODEL_ACTOR_UNAVAILABLE")
		}
		if current.State != "verifying" || importCancellationPending(current.Import) {
			return fail("MODEL_IMPORT_STATE_CONFLICT")
		}
		manifest := resolveImportManifest(current.CatalogDigest)
		if manifest == nil {
			return fail("MODEL_IMPORT_INCOMPLETE")
		}
		if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `INSERT INTO MODEL_VERSIONS(ID,TENANT_ID,WORKSPACE_ID,NAME,MODEL_KEY,CATALOG_DIGEST,MANIFEST,STATE,CREATED_BY,KIND) VALUES(?,?,?,?,?,?,?,'ready',?,'approved_base')`,
			`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,'ready',$8,'approved_base')`,
			`INSERT INTO model_versions(id,tenant_id,workspace_id,name,model_key,catalog_digest,manifest,state,created_by,kind) VALUES(?,?,?,?,?,?,CAST(? AS JSON),'ready',?,'approved_base')`, current.ID, current.TenantID, current.WorkspaceID, current.Name, manifest.catalog.ModelKey, current.CatalogDigest, manifest.canonical, current.ActorID)); e != nil {
			return e
		}
		if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `UPDATE MODEL_IMPORTS SET STATE='ready',MODEL_ID=?,ERROR_CODE=NULL,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
			`UPDATE model_imports SET state='ready',model_id=$1,error_code=NULL,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`,
			`UPDATE model_imports SET state='ready',model_id=?,error_code=NULL,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, current.ID, current.TenantID, current.WorkspaceID, current.ID)); e != nil {
			return e
		}
		return workerAudit(ctx, q, current, "model.import_ready", "success", current.Reason)
	})
}

func workerUncertain(ctx context.Context, e error) bool {
	if ctx.Err() != nil || errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
		return true
	}
	var safe *transaction.Error
	return errors.As(e, &safe) && (safe.Kind == transaction.CommitUnknown || safe.Kind == transaction.Cancelled || safe.Kind == transaction.Timeout)
}

func (s *Service) recordWorkerFailure(ctx context.Context, session transaction.Store, reference workerImport, cause error) error {
	return s.workerWithin(ctx, session, reference.ID, func(q transaction.Executor, current workerImport, _ bool) error {
		if !sameWorkerBinding(reference, current) || reference.Attempts != current.Attempts {
			return fail("MODEL_IMPORT_STATE_CONFLICT")
		}
		ready, e := publicationReady(ctx, q, current)
		if e != nil || ready {
			return e
		}
		if current.State != "queued" && current.State != "verifying" || importCancellationPending(current.Import) {
			return fail("MODEL_IMPORT_STATE_CONFLICT")
		}
		code := safeImportError(cause)
		if _, e = modelExecOne(ctx, q, modelNativeSQL(q, `UPDATE MODEL_IMPORTS SET STATE='failed',ERROR_CODE=?,UPDATED_AT=CURRENT_TIMESTAMP(6) WHERE TENANT_ID=? AND WORKSPACE_ID=? AND ID=?`,
			`UPDATE model_imports SET state='failed',error_code=$1,updated_at=clock_timestamp() WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4`,
			`UPDATE model_imports SET state='failed',error_code=?,updated_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND id=?`, code, current.TenantID, current.WorkspaceID, current.ID)); e != nil {
			return e
		}
		return workerAudit(ctx, q, current, "model.import_failed", "failure", code)
	})
}

func normalizedWorkerError(ctx context.Context, dialect transaction.Dialect, e error) error {
	if e == nil {
		return nil
	}
	var safe *transaction.Error
	// Native SQL failure invalidates the pinned session. Preserve its typed
	// outcome ahead of ambient cancellation; CommitUnknown has this priority
	// for every engine. Existing non-native cancellation semantics stay intact.
	if errors.As(e, &safe) && (dialect == transaction.Dameng || safe.Kind == transaction.CommitUnknown) {
		return safe
	}
	if ctx.Err() != nil {
		e = ctx.Err()
	}
	if errors.Is(e, context.Canceled) {
		return &transaction.Error{Kind: transaction.Cancelled}
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return &transaction.Error{Kind: transaction.Timeout}
	}
	if errors.As(e, &safe) {
		return safe
	}
	return fail(safeImportError(e))
}

func (s *Service) processNext(ctx context.Context) (bool, error) {
	if ctx == nil {
		return false, fail("MODEL_STORAGE_UNAVAILABLE")
	}
	store, e := s.repository()
	if e != nil {
		return false, e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().Models.ImportSeconds))
	defer cancel()
	var id string
	e = store.QueryRow(ctx, modelNativeSQL(store, `SELECT M.ID FROM MODEL_IMPORTS M JOIN INSTALLATION_STATE I ON I.TENANT_ID=M.TENANT_ID WHERE I.ID=1 AND I.BOOTSTRAP_COMPLETE=1 AND M.STATE IN ('queued','verifying') AND (M.ERROR_CODE IS NULL OR M.ERROR_CODE<>?) ORDER BY M.CREATED_AT,M.ID LIMIT 1`,
		`SELECT m.id::text FROM model_imports m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND m.state IN ('queued','verifying') AND (m.error_code IS NULL OR m.error_code<>$1) ORDER BY m.created_at,m.id LIMIT 1`,
		`SELECT m.id FROM model_imports m JOIN installation_state i ON i.tenant_id=m.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND m.state IN ('queued','verifying') AND (m.error_code IS NULL OR m.error_code<>?) ORDER BY m.created_at,m.id LIMIT 1`, modelCancellationPending)).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, normalizedWorkerError(ctx, store.Dialect(), e)
	}
	owned := false
	e = withImportFileLock(ctx, s.RootDirectory, id, func() error {
		return s.withImportSession(ctx, id, func(ctx context.Context, session transaction.Store) error {
			owned = true
			return processWorkerSession(ctx, session, id, workerSessionOperations{
				claim: s.claimImport, verify: s.verifyImportFiles,
				publish: s.publishWorker, recordFailure: s.recordWorkerFailure,
				verifyReady: func(ctx context.Context, current workerImport) error {
					manifest := resolveImportManifest(current.CatalogDigest)
					if manifest == nil {
						return fail("MODEL_PUBLICATION_CONFLICT")
					}
					return manifest.verifyDirectory(ctx, filepath.Join(s.RootDirectory, "objects", current.ID))
				},
			})
		})
	})
	if e != nil && e.Error() == "MODEL_IMPORT_BUSY" {
		return false, nil
	}
	return owned, normalizedWorkerError(ctx, store.Dialect(), e)
}

// Explicit operations keep the complete owned-session callback independently
// fault-testable without substituting a successful artifact or database engine.
// Production supplies the actual owner methods at the sole call site above.
type workerSessionOperations struct {
	claim         func(context.Context, transaction.Store, string) (workerImport, bool, bool, error)
	verify        func(context.Context, transaction.Store, workerImport) error
	publish       func(context.Context, transaction.Store, workerImport) error
	recordFailure func(context.Context, transaction.Store, workerImport, error) error
	verifyReady   func(context.Context, workerImport) error
}

func processWorkerSession(ctx context.Context, session transaction.Store, id string, operations workerSessionOperations) error {
	current, eligible, ready, e := operations.claim(ctx, session, id)
	if e == nil && ready {
		return operations.verifyReady(ctx, current)
	}
	if e == nil && !eligible {
		return nil
	}
	if e == nil {
		e = operations.verify(ctx, session, current)
		if e == nil {
			e = operations.publish(ctx, session, current)
		}
	}
	if e != nil && current.ID != "" && workerFailureCanBeRecorded(ctx, session, e) {
		if failed := operations.recordFailure(ctx, session, current, e); failed != nil {
			return failed
		}
	}
	return e
}
func workerFailureCanBeRecorded(ctx context.Context, session transaction.Store, cause error) bool {
	if cause == nil || workerUncertain(ctx, cause) {
		return false
	}
	if session.Dialect() == transaction.Dameng {
		var databaseError *transaction.Error
		// Native statement errors poison the pinned physical session. Even a benign
		// unhandled NoRows is left for a fresh authorized reconciliation, rather
		// than guessing whether another transaction on that session is safe.
		if errors.As(cause, &databaseError) {
			return false
		}
		// Only an explicitly classified file/business failure may use the session
		// again. Redacted/untyped storage or repository errors cannot prove health.
		code := safeImportError(cause)
		return code != "MODEL_IMPORT_FAILED" && code == cause.Error()
	}
	return true
}
