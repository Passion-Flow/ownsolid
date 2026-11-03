package artifactstores

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

func fail(code string) error { return errors.New(code) }
func id(v string) bool       { u, e := uuid.Parse(v); return e == nil && u != uuid.Nil && u.String() == v }
func statement(q transaction.Executor, pg, my, dm string, args ...any) transaction.Statement {
	switch q.Dialect() {
	case transaction.PostgreSQL:
		return transaction.SQL(transaction.PostgreSQL, pg, args...)
	case transaction.MySQL:
		return transaction.SQL(transaction.MySQL, my, args...)
	case transaction.Dameng:
		return transaction.SQL(transaction.Dameng, dm, args...)
	default:
		// An unknown store must fail its statement validation, never select PG.
		return transaction.SQL(q.Dialect(), "", args...)
	}
}
func safe(e error) error {
	if e == nil {
		return nil
	}
	for _, kind := range []transaction.ErrorKind{transaction.CommitUnknown, transaction.CommitRolledBack} {
		if containsOutcome(e, kind, 0) {
			return &transaction.Error{Kind: kind}
		}
	}
	if transaction.IsCancellationOnly(e) {
		kind := transaction.Timeout
		if errors.Is(e, context.Canceled) {
			kind = transaction.Cancelled
		}
		return &transaction.Error{Kind: kind}
	}
	c := e.Error()
	if len(c) > 100 {
		return fail("STORAGE_UNAVAILABLE")
	}
	for _, r := range c {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			return fail("STORAGE_UNAVAILABLE")
		}
	}
	for _, p := range []string{"OBJECT_", "MODEL_", "DATASET_", "AUTH_", "STORAGE_"} {
		if strings.HasPrefix(c, p) {
			return fail(c)
		}
	}
	switch c {
	case "LICENSE_CONFIGURATION_REQUIRED", "LICENSE_STORED_STATE_INVALID", "LICENSE_NEW_BUSINESS_UNAVAILABLE":
		return fail(c)
	case "ACCESS_DENIED", "RESOURCE_NOT_FOUND", "VALIDATION_FAILED", "IDEMPOTENCY_CONFLICT", "RESOURCE_LIMIT_REACHED", "VERSION_CONFLICT":
		return fail(c)
	}
	return fail("STORAGE_UNAVAILABLE")
}
func (s *Service) repository() (transaction.Store, error) {
	if s == nil || s.Identity == nil || s.Workspaces == nil || s.Workspaces.Identity != s.Identity {
		return nil, fail("OBJECT_IDENTITY_REQUIRED")
	}
	if s.Workspaces.DB != nil && s.Workspaces.DB != s.Identity.DB {
		return nil, fail("OBJECT_STORAGE_MISMATCH")
	}
	if !s.Configuration.Enabled || s.Configuration.Validate() != nil || s.Transport == nil || s.Stage == nil {
		return nil, fail("OBJECT_STORAGE_UNAVAILABLE")
	}
	if s.Stage.Identity() != s.Transport.Identity() || s.Stage.Identity() != objectstore.ConfigurationIdentity(s.Configuration) {
		return nil, fail("OBJECT_STORE_CHANGED")
	}
	r, e := s.Identity.Repository()
	if e != nil {
		return nil, e
	}
	if r.Dialect() != transaction.PostgreSQL && r.Dialect() != transaction.MySQL && r.Dialect() != transaction.Dameng {
		return nil, fail("OBJECT_STORAGE_UNAVAILABLE")
	}
	return r, nil
}
func admin(a identity.Auth) bool { return a.Me.Audience == "admin" && a.Me.User.IsPlatformAdmin }
func writeRole(a identity.Auth, w workspaces.Workspace) bool {
	return admin(a) || w.Role != nil && (*w.Role == "owner" || *w.Role == "member")
}
func (s *Service) within(ctx context.Context, a identity.Auth, workspace, extraUser string, fn func(context.Context, transaction.Executor, workspaces.Workspace) error) error {
	if ctx == nil || !id(workspace) || a.Me.Audience != "admin" && a.Me.Audience != "portal" {
		return fail("ACCESS_DENIED")
	}
	r, e := s.repository()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	e = r.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		if extraUser != "" {
			if !id(extraUser) {
				return fail("VALIDATION_FAILED")
			}
			if e := s.Identity.LockSessionUsersWithin(ctx, q, a.Me.User.ID, extraUser, a.Me.Audience); e != nil {
				return e
			}
		}
		if e := s.Identity.RecheckOrderedWithin(ctx, q, a); e != nil {
			return e
		}
		w, e := s.Workspaces.RecheckScopeWithin(ctx, q, a, workspace)
		if e != nil {
			return e
		}
		if w.Status != "active" || w.TenantID != s.Stage.Tenant() {
			return fail("ACCESS_DENIED")
		}
		var tenant string
		if e = q.QueryRow(ctx, statement(q, "SELECT tenant_id::text FROM installation_state WHERE id=1 AND bootstrap_complete FOR SHARE", "SELECT tenant_id FROM installation_state WHERE id=1 AND bootstrap_complete=1 FOR SHARE", `SELECT "TENANT_ID" FROM "INSTALLATION_STATE" WHERE "ID"=1 AND "BOOTSTRAP_COMPLETE"=1 FOR UPDATE`)).Scan(&tenant); e != nil {
			return e
		}
		if tenant != w.TenantID {
			return fail("RESOURCE_NOT_FOUND")
		}
		return fn(ctx, q, w)
	})
	return safe(e)
}

const pgObjectFields = `id::text,workspace_id::text,name,purpose,expected_bytes,sha256,part_bytes,state,revision,created_at,updated_at,upload_expires_at,expires_at,error_code,tenant_id::text,created_by::text,requested_audience,store_id::text,requested_epoch,membership_authority::text,remote_upload_id,remote_etag,operation_id::text,operation_kind,operation_phase,operation_expires_at,operation_authority,execution_epoch,execution_membership::text`
const myObjectFields = `id,workspace_id,name,purpose,expected_bytes,sha256,part_bytes,state,revision,created_at,updated_at,upload_expires_at,expires_at,error_code,tenant_id,created_by,requested_audience,store_id,requested_epoch,membership_authority,remote_upload_id,remote_etag,operation_id,operation_kind,operation_phase,operation_expires_at,operation_authority,execution_epoch,execution_membership`

const dmObjectFields = `"ID","WORKSPACE_ID","NAME","PURPOSE","EXPECTED_BYTES","SHA256","PART_BYTES","STATE","REVISION","CREATED_AT","UPDATED_AT","UPLOAD_EXPIRES_AT","EXPIRES_AT","ERROR_CODE","TENANT_ID","CREATED_BY","REQUESTED_AUDIENCE","STORE_ID","REQUESTED_EPOCH","MEMBERSHIP_AUTHORITY","REMOTE_UPLOAD_ID","REMOTE_ETAG","OPERATION_ID","OPERATION_KIND","OPERATION_PHASE","OPERATION_EXPIRES_AT","OPERATION_AUTHORITY","EXECUTION_EPOCH","EXECUTION_MEMBERSHIP"`

func scanObject(row transaction.Row) (storedObject, error) {
	var o storedObject
	e := row.Scan(&o.ID, &o.WorkspaceID, &o.Name, &o.Purpose, &o.ExpectedBytes, &o.SHA256, &o.PartBytes, &o.State, &o.Revision, &o.CreatedAt, &o.UpdatedAt, &o.UploadExpiresAt, &o.ExpiresAt, &o.ErrorCode, &o.Tenant, &o.Creator, &o.Audience, &o.StoreID, &o.Epoch, &o.Membership, &o.UploadID, &o.ETag, &o.OperationID, &o.OperationKind, &o.OperationPhase, &o.OperationExpires, &o.OperationAuthority, &o.ExecutionEpoch, &o.ExecutionMembership)
	if errors.Is(e, sql.ErrNoRows) {
		return storedObject{}, fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return storedObject{}, e
	}
	o.CreatedAt, o.UpdatedAt, o.UploadExpiresAt, o.ExpiresAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC(), o.UploadExpiresAt.UTC(), o.ExpiresAt.UTC()
	return o, nil
}
func (s *Service) objectWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, object string) (storedObject, error) {
	if !id(object) {
		return storedObject{}, fail("RESOURCE_NOT_FOUND")
	}
	o, e := scanObject(q.QueryRow(ctx, statement(q, "SELECT "+pgObjectFields+" FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3 FOR UPDATE", "SELECT "+myObjectFields+" FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND id=? FOR UPDATE", `SELECT `+dmObjectFields+` FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=? FOR UPDATE`, w.TenantID, w.ID, object)))
	if e != nil {
		return storedObject{}, e
	}
	if o.StoreID != s.Stage.Namespace() {
		return storedObject{}, fail("OBJECT_STORE_CHANGED")
	}
	return o, nil
}
func (s *Service) writeObject(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, o storedObject, pending bool) error {
	if !writeRole(a, w) || !admin(a) && o.Creator != a.Me.User.ID && !(w.Role != nil && *w.Role == "owner") {
		return fail("ACCESS_DENIED")
	}
	if pending && !admin(a) && o.Creator == a.Me.User.ID {
		return s.creatorCurrentWithin(ctx, q, a, w, o)
	}
	return nil
}
func (s *Service) creatorCurrentWithin(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, o storedObject) error {
	if o.ExecutionEpoch != a.Epoch {
		return fail("OBJECT_ACTOR_CHANGED")
	}
	var membership string
	if e := q.QueryRow(ctx, statement(q, "SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", `SELECT "AUTHORITY_ID" FROM "WORKSPACE_MEMBERSHIPS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "USER_ID"=?`, w.TenantID, w.ID, a.Me.User.ID)).Scan(&membership); e != nil {
		return e
	}
	if o.ExecutionMembership == nil || *o.ExecutionMembership != membership {
		return fail("OBJECT_ACTOR_CHANGED")
	}

	return nil
}
func audit(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, action, target, reason, request string) error {
	if !id(request) || !workspaces.ValidReason(reason) {
		return fail("VALIDATION_FAILED")
	}
	_, e := q.Exec(ctx, statement(q, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),$2,$3,$4,$5,$6,'success','artifact_object',$7,$8,$9)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),?,?,?,?,?,'success','artifact_object',?,?,?)`, `INSERT INTO "AUDIT_EVENTS"("ID","OCCURRED_AT","ACTOR_USER_ID","TENANT_ID","WORKSPACE_ID","AUDIENCE","ACTION","OUTCOME","TARGET_TYPE","TARGET_ID","REASON","REQUEST_ID") VALUES(?,SYS_EXTRACT_UTC(SYSTIMESTAMP(6)),?,?,?,?,?,'success','artifact_object',?,?,?)`, uuid.NewString(), a.Me.User.ID, w.TenantID, w.ID, a.Me.Audience, action, target, reason, request))
	return e
}

const (
	storedAuthorityVersion      = 1
	maximumStoredAuthorityBytes = 4096
)

func actorBytes(a identity.Auth) []byte {
	b, _ := json.Marshal(authority{FormatVersion: storedAuthorityVersion, Actor: a.Me.User.ID,
		Session: a.SessionID, Audience: a.Me.Audience, Epoch: a.Epoch,
		TokenHash: base64.StdEncoding.EncodeToString(a.TokenHash)})
	return b
}
func actorFrom(raw []byte) (identity.Auth, error) {
	var v authority
	if len(raw) == 0 || len(raw) > maximumStoredAuthorityBytes || release.StrictJSON(raw, &v) != nil || v.FormatVersion != storedAuthorityVersion || !id(v.Actor) || !id(v.Session) || v.Epoch < 1 || v.Epoch > identity.MaximumAuthVersion || v.Audience != "admin" && v.Audience != "portal" {
		return identity.Auth{}, fail("OBJECT_AUTHORITY_INVALID")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 6 {
		return identity.Auth{}, fail("OBJECT_AUTHORITY_INVALID")
	}
	for _, name := range []string{"format_version", "actor_id", "session_id", "audience", "epoch", "token_hash"} {
		if _, ok := fields[name]; !ok {
			return identity.Auth{}, fail("OBJECT_AUTHORITY_INVALID")
		}
	}
	hash, err := base64.StdEncoding.DecodeString(v.TokenHash)
	if err != nil || len(hash) != sha256.Size || base64.StdEncoding.EncodeToString(hash) != v.TokenHash {
		return identity.Auth{}, fail("OBJECT_AUTHORITY_INVALID")
	}
	var a identity.Auth
	a.Me.User.ID = v.Actor
	a.Me.Audience = v.Audience
	a.Me.User.IsPlatformAdmin = v.Audience == "admin"
	a.SessionID = v.Session
	a.Epoch = v.Epoch
	a.TokenHash = hash
	return a, nil
}
func liveWithin(ctx context.Context, q transaction.Executor, o storedObject, upload bool) error {
	var live bool
	column := "expires_at"
	if upload {
		column = "upload_expires_at"
	}
	e := q.QueryRow(ctx, statement(q, "SELECT "+column+">clock_timestamp() FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT "+column+">UTC_TIMESTAMP(6) FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND id=?", `SELECT CASE WHEN `+damengColumn(column)+`>SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) THEN 1 ELSE 0 END FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, o.Tenant, o.WorkspaceID, o.ID)).Scan(&live)
	if e != nil {
		return e
	}
	if !live {
		return fail("OBJECT_EXPIRED")
	}
	return nil
}

// Bound malformed unwrap graphs and preserve uncertain commit precedence.
func containsOutcome(e error, kind transaction.ErrorKind, depth int) bool {
	if e == nil || depth >= 64 {
		return false
	}
	if e.Error() == string(kind) {
		return true
	}
	if value, ok := e.(*transaction.Error); ok && value.Kind == kind {
		return true
	}
	if joined, ok := e.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if containsOutcome(child, kind, depth+1) {
				return true
			}
		}
	}
	if wrapped, ok := e.(interface{ Unwrap() error }); ok {
		return containsOutcome(wrapped.Unwrap(), kind, depth+1)
	}
	return false
}
