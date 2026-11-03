package artifactstores

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/objectstore"
)

const MaximumObjectPageSize = 100

func (s *Service) Create(ctx context.Context, a identity.Auth, input CreateRequest, key, request string) (Object, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Object{}, admissionError
	}
	defer finish()
	if !id(key) || !id(request) || !workspaces.ValidReason(input.Reason) || input.ExpectedBytes < 1 || input.ExpectedBytes > s.Configuration.MaximumObjectBytes || !objectstore.ValidDigest(input.SHA256) || input.Purpose != "model_bundle" && input.Purpose != "dataset_source" {
		return Object{}, fail("VALIDATION_FAILED")
	}
	input.Name = strings.TrimSpace(input.Name)
	if !utf8.ValidString(input.Name) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 200 || strings.ContainsFunc(input.Name, unicode.IsControl) {
		return Object{}, fail("VALIDATION_FAILED")
	}
	if e := s.admitNewBusiness(ctx, a, input.WorkspaceID); e != nil {
		return Object{}, e
	}
	raw, _ := json.Marshal(struct {
		Input CreateRequest
		Actor string
	}{input, a.Me.User.ID})
	hash := sha256.Sum256(raw)
	var out Object
	e := s.within(ctx, a, input.WorkspaceID, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		if !writeRole(a, w) {
			return fail("ACCESS_DENIED")
		}
		previous, e := scanObject(q.QueryRow(ctx, statement(q, "SELECT "+pgObjectFields+" FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND created_by=$3 AND creation_key=$4 FOR UPDATE", "SELECT "+myObjectFields+" FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND created_by=? AND creation_key=? FOR UPDATE", `SELECT `+dmObjectFields+` FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "CREATED_BY"=? AND "CREATION_KEY"=? FOR UPDATE`, w.TenantID, w.ID, a.Me.User.ID, key)))
		if e == nil {
			var original []byte
			if e = q.QueryRow(ctx, statement(q, "SELECT fingerprint FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3", "SELECT fingerprint FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND id=?", `SELECT "FINGERPRINT" FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "ID"=?`, w.TenantID, w.ID, previous.ID)).Scan(&original); e != nil {
				return e
			}
			if subtle.ConstantTimeCompare(original, hash[:]) != 1 {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			if previous.StoreID != s.Stage.Namespace() {
				return fail("OBJECT_STORE_CHANGED")
			}
			out = previous.Object
			return nil
		}
		if e.Error() != "RESOURCE_NOT_FOUND" {
			return e
		}
		var membership *string
		if !admin(a) {
			var m string
			if e = q.QueryRow(ctx, statement(q, "SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", `SELECT "AUTHORITY_ID" FROM "WORKSPACE_MEMBERSHIPS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "USER_ID"=?`, w.TenantID, w.ID, a.Me.User.ID)).Scan(&m); e != nil {
				return e
			}
			membership = &m
		}
		object, operation := uuid.NewString(), uuid.NewString()
		args := []any{object, w.TenantID, w.ID, a.Me.User.ID, a.Me.Audience, a.Epoch, membership, key, hash[:], input.Name, input.Purpose, input.ExpectedBytes, input.SHA256, s.Configuration.PartBytes, s.Stage.Namespace(), operation, jsonParameter(q, actorBytes(a)), s.Configuration.TransferSeconds, s.Configuration.UploadSeconds, s.Configuration.ObjectLifetimeSeconds, a.Epoch, membership}
		if _, e = q.Exec(ctx, statement(q, `INSERT INTO artifact_objects(id,tenant_id,workspace_id,created_by,requested_audience,requested_epoch,membership_authority,creation_key,fingerprint,name,purpose,expected_bytes,sha256,part_bytes,store_id,state,operation_id,operation_kind,operation_phase,operation_authority,operation_expires_at,upload_expires_at,expires_at,execution_epoch,execution_membership) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'creating',$16,'create','prepared',$17,clock_timestamp()+make_interval(secs=>$18),clock_timestamp()+make_interval(secs=>$19),clock_timestamp()+make_interval(secs=>$20),$21,$22)`, `INSERT INTO artifact_objects(id,tenant_id,workspace_id,created_by,requested_audience,requested_epoch,membership_authority,creation_key,fingerprint,name,purpose,expected_bytes,sha256,part_bytes,store_id,state,operation_id,operation_kind,operation_phase,operation_authority,operation_expires_at,upload_expires_at,expires_at,execution_epoch,execution_membership) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'creating',?,'create','prepared',CAST(? AS JSON),TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6)),?,?)`, `INSERT INTO "ARTIFACT_OBJECTS"("ID","TENANT_ID","WORKSPACE_ID","CREATED_BY","REQUESTED_AUDIENCE","REQUESTED_EPOCH","MEMBERSHIP_AUTHORITY","CREATION_KEY","FINGERPRINT","NAME","PURPOSE","EXPECTED_BYTES","SHA256","PART_BYTES","STORE_ID","STATE","OPERATION_ID","OPERATION_KIND","OPERATION_PHASE","OPERATION_AUTHORITY","OPERATION_EXPIRES_AT","UPLOAD_EXPIRES_AT","EXPIRES_AT","EXECUTION_EPOCH","EXECUTION_MEMBERSHIP") VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,'creating',?,'create','prepared',?,(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND')),(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND')),(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND')),?,?)`, args...)); e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object.created", object, input.Reason, request); e != nil {
			return e
		}
		value, e := s.objectWithin(ctx, q, w, object)
		out = value.Object
		return e
	})
	if e != nil {
		return Object{}, e
	}
	return out, nil
}
func (s *Service) Get(ctx context.Context, a identity.Auth, workspace, object, grant string) (Object, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Object{}, admissionError
	}
	defer finish()
	var out Object
	issuer, e := s.grantIssuer(ctx, a, workspace, object, grant)
	if e != nil {
		return out, e
	}
	e = s.within(ctx, a, workspace, issuer, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.authorize(ctx, q, a, w, o, "inspect", grant); e != nil {
			return e
		}
		out = o.Object
		return nil
	})
	if e != nil {
		return Object{}, e
	}
	return out, nil
}
func (s *Service) List(ctx context.Context, a identity.Auth, workspace, after string, limit int) ([]Object, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return nil, admissionError
	}
	defer finish()
	if s == nil || s.Identity == nil {
		return nil, fail("OBJECT_IDENTITY_REQUIRED")
	}
	if limit == 0 {
		limit = s.Identity.Config.Policy().UI.PageSize
	}
	if after != "" && !id(after) || limit < 1 || limit > MaximumObjectPageSize {
		return nil, fail("VALIDATION_FAILED")
	}
	out := []Object{}
	e := s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		owner := admin(a) || w.Role != nil && *w.Role == "owner"
		rows, e := q.Query(ctx, statement(q, "SELECT "+pgObjectFields+" FROM artifact_objects WHERE tenant_id=$1 AND workspace_id=$2 AND ($3::boolean OR created_by=$4) AND ($5::text='' OR id>NULLIF($5::text,'')::uuid) ORDER BY id LIMIT $6", "SELECT "+myObjectFields+" FROM artifact_objects WHERE tenant_id=? AND workspace_id=? AND (? OR created_by=?) AND (?='' OR id>?) ORDER BY id LIMIT ?", `SELECT `+dmObjectFields+` FROM "ARTIFACT_OBJECTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND (?=1 OR "CREATED_BY"=?) AND (? IS NULL OR "ID">?) ORDER BY "ID" LIMIT `+strconv.Itoa(limit), listArgs(q, w, owner, a.Me.User.ID, after, limit)...))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			o, e := scanObject(rows)
			if e != nil {
				return e
			}
			if o.StoreID != s.Stage.Namespace() {
				return fail("OBJECT_STORE_CHANGED")
			}
			out = append(out, o.Object)
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func listArgs(q transaction.Executor, w workspaces.Workspace, owner bool, user, after string, limit int) []any {
	if q.Dialect() == transaction.Dameng {
		return []any{w.TenantID, w.ID, owner, user, damengCursor(after), damengCursor(after)}
	}
	if positionalArguments(q) {
		return []any{w.TenantID, w.ID, owner, user, after, after, limit}
	}
	return []any{w.TenantID, w.ID, owner, user, after, limit}
}
