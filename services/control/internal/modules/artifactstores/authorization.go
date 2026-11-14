package artifactstores

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// This candidate lookup reveals no grant/issuer projection to callers. It only
// determines the additional immutable user lock before actor/Workspace locks.
func (s *Service) grantIssuer(ctx context.Context, a identity.Auth, workspace, object, grant string) (string, error) {
	if grant == "" {
		return "", nil
	}
	if !id(workspace) || !id(object) || !id(grant) || ctx == nil {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	r, e := s.repository()
	if e != nil {
		return "", e
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var user string
	e = r.Within(ctx, true, func(ctx context.Context, q transaction.Executor) error {
		return q.QueryRow(ctx, statement(q, "SELECT g.created_by::text FROM artifact_object_grants g JOIN installation_state i ON i.tenant_id=g.tenant_id WHERE i.id=1 AND g.workspace_id=$1 AND g.object_id=$2 AND g.id=$3 AND g.recipient_user_id=$4", "SELECT g.created_by FROM artifact_object_grants g JOIN installation_state i ON i.tenant_id=g.tenant_id WHERE i.id=1 AND g.workspace_id=? AND g.object_id=? AND g.id=? AND g.recipient_user_id=?", `SELECT "G"."CREATED_BY" FROM "ARTIFACT_OBJECT_GRANTS" "G" JOIN "INSTALLATION_STATE" "I" ON "I"."TENANT_ID"="G"."TENANT_ID" WHERE "I"."ID"=1 AND "G"."WORKSPACE_ID"=? AND "G"."OBJECT_ID"=? AND "G"."ID"=? AND "G"."RECIPIENT_USER_ID"=?`, workspace, object, grant, a.Me.User.ID)).Scan(&user)
	})
	if errors.Is(e, sql.ErrNoRows) {
		return "", fail("RESOURCE_NOT_FOUND")
	}
	if e != nil {
		return "", safe(e)
	}
	return user, nil
}
func (s *Service) authorize(ctx context.Context, q transaction.Executor, a identity.Auth, w workspaces.Workspace, o storedObject, action, grant string) error {
	if grant == "" {
		if admin(a) || w.Role != nil && *w.Role == "owner" {
			return nil
		}
		if o.Creator == a.Me.User.ID {
			return s.creatorCurrentWithin(ctx, q, a, w, o)
		}
		return fail("RESOURCE_NOT_FOUND")
	}
	var current bool
	e := q.QueryRow(ctx, statement(q, `SELECT EXISTS(SELECT 1 FROM artifact_object_grants g JOIN users issuer ON issuer.id=g.created_by JOIN users recipient ON recipient.id=g.recipient_user_id JOIN workspace_memberships rm ON rm.tenant_id=g.tenant_id AND rm.workspace_id=g.workspace_id AND rm.user_id=recipient.id LEFT JOIN workspace_memberships im ON im.tenant_id=g.tenant_id AND im.workspace_id=g.workspace_id AND im.user_id=issuer.id WHERE g.tenant_id=$1 AND g.workspace_id=$2 AND g.object_id=$3 AND g.id=$4 AND g.recipient_user_id=$5 AND (g.action=$6 OR ($6='inspect' AND g.action IN ('read','ingest_model','ingest_dataset'))) AND g.not_before<=clock_timestamp() AND g.expires_at>clock_timestamp() AND g.revoked_at IS NULL AND recipient.status='active' AND recipient.auth_epoch=g.recipient_epoch AND rm.authority_id=g.recipient_membership AND issuer.status='active' AND issuer.auth_epoch=g.creator_epoch AND ((g.creator_membership IS NULL AND issuer.is_platform_admin) OR (im.authority_id=g.creator_membership AND im.role IN ('owner','member'))))`, `SELECT EXISTS(SELECT 1 FROM artifact_object_grants g JOIN users issuer ON issuer.id=g.created_by JOIN users recipient ON recipient.id=g.recipient_user_id JOIN workspace_memberships rm ON rm.tenant_id=g.tenant_id AND rm.workspace_id=g.workspace_id AND rm.user_id=recipient.id LEFT JOIN workspace_memberships im ON im.tenant_id=g.tenant_id AND im.workspace_id=g.workspace_id AND im.user_id=issuer.id WHERE g.tenant_id=? AND g.workspace_id=? AND g.object_id=? AND g.id=? AND g.recipient_user_id=? AND (g.action=? OR (?='inspect' AND g.action IN ('read','ingest_model','ingest_dataset'))) AND g.not_before<=UTC_TIMESTAMP(6) AND g.expires_at>UTC_TIMESTAMP(6) AND g.revoked_at IS NULL AND recipient.status='active' AND recipient.auth_epoch=g.recipient_epoch AND rm.authority_id=g.recipient_membership AND issuer.status='active' AND issuer.auth_epoch=g.creator_epoch AND ((g.creator_membership IS NULL AND issuer.is_platform_admin=1) OR (im.authority_id=g.creator_membership AND im.role IN ('owner','member'))))`, `SELECT CASE WHEN EXISTS(SELECT 1 FROM "ARTIFACT_OBJECT_GRANTS" "G" JOIN "USERS" "ISSUER" ON "ISSUER"."ID"="G"."CREATED_BY" JOIN "USERS" "RECIPIENT" ON "RECIPIENT"."ID"="G"."RECIPIENT_USER_ID" JOIN "WORKSPACE_MEMBERSHIPS" "RM" ON "RM"."TENANT_ID"="G"."TENANT_ID" AND "RM"."WORKSPACE_ID"="G"."WORKSPACE_ID" AND "RM"."USER_ID"="RECIPIENT"."ID" LEFT JOIN "WORKSPACE_MEMBERSHIPS" "IM" ON "IM"."TENANT_ID"="G"."TENANT_ID" AND "IM"."WORKSPACE_ID"="G"."WORKSPACE_ID" AND "IM"."USER_ID"="ISSUER"."ID" WHERE "G"."TENANT_ID"=? AND "G"."WORKSPACE_ID"=? AND "G"."OBJECT_ID"=? AND "G"."ID"=? AND "G"."RECIPIENT_USER_ID"=? AND ("G"."ACTION"=? OR (?='inspect' AND "G"."ACTION" IN ('read','ingest_model','ingest_dataset'))) AND "G"."NOT_BEFORE"<=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) AND "G"."EXPIRES_AT">SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) AND "G"."REVOKED_AT" IS NULL AND "RECIPIENT"."STATUS"='active' AND "RECIPIENT"."AUTH_EPOCH"="G"."RECIPIENT_EPOCH" AND "RM"."AUTHORITY_ID"="G"."RECIPIENT_MEMBERSHIP" AND "ISSUER"."STATUS"='active' AND "ISSUER"."AUTH_EPOCH"="G"."CREATOR_EPOCH" AND (("G"."CREATOR_MEMBERSHIP" IS NULL AND "ISSUER"."IS_PLATFORM_ADMIN"=1) OR ("IM"."AUTHORITY_ID"="G"."CREATOR_MEMBERSHIP" AND "IM"."ROLE" IN ('owner','member')))) THEN 1 ELSE 0 END FROM DUAL`, grantAuthorityArgs(q, w, o.ID, grant, a.Me.User.ID, action)...)).Scan(&current)
	if e != nil {
		return e
	}
	if !current {
		return fail("RESOURCE_NOT_FOUND")
	}
	return nil
}
func (s *Service) CreateGrant(ctx context.Context, a identity.Auth, workspace, object string, input GrantRequest, key, request string) (Grant, error) {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return Grant{}, admissionError
	}
	defer finish()
	if !id(input.Recipient) || !id(key) || !id(request) || input.Seconds < 1 || input.Seconds > s.Configuration.MaximumGrantSeconds || !workspaces.ValidReason(input.Reason) || input.Action != "read" && input.Action != "ingest_model" && input.Action != "ingest_dataset" {
		return Grant{}, fail("VALIDATION_FAILED")
	}
	if e := s.admitNewBusiness(ctx, a, workspace); e != nil {
		return Grant{}, e
	}
	raw, _ := json.Marshal(struct {
		Input GrantRequest
		Actor string
	}{input, a.Me.User.ID})
	fingerprint := sha256.Sum256(raw)
	var out Grant
	e := s.within(ctx, a, workspace, input.Recipient, func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.writeObject(ctx, q, a, w, o, false); e != nil {
			return e
		}
		if e = liveWithin(ctx, q, o, false); e != nil {
			return e
		}
		if o.State != "ready" {
			return fail("OBJECT_NOT_READY")
		}
		var existing string
		var original []byte
		e = q.QueryRow(ctx, statement(q, "SELECT id::text,fingerprint FROM artifact_object_grants WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND created_by=$4 AND creation_key=$5 FOR UPDATE", "SELECT id,fingerprint FROM artifact_object_grants WHERE tenant_id=? AND workspace_id=? AND object_id=? AND created_by=? AND creation_key=? FOR UPDATE", `SELECT "ID","FINGERPRINT" FROM "ARTIFACT_OBJECT_GRANTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "CREATED_BY"=? AND "CREATION_KEY"=? FOR UPDATE`, w.TenantID, w.ID, o.ID, a.Me.User.ID, key)).Scan(&existing, &original)
		if e == nil {
			if subtle.ConstantTimeCompare(original, fingerprint[:]) != 1 {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out, e = scanGrant(q.QueryRow(ctx, statement(q, "SELECT id::text,object_id::text,recipient_user_id::text,action,not_before,expires_at,revoked_at FROM artifact_object_grants WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4", "SELECT id,object_id,recipient_user_id,action,not_before,expires_at,revoked_at FROM artifact_object_grants WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=?", `SELECT "ID","OBJECT_ID","RECIPIENT_USER_ID","ACTION","NOT_BEFORE","EXPIRES_AT","REVOKED_AT" FROM "ARTIFACT_OBJECT_GRANTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=?`, w.TenantID, w.ID, o.ID, existing)))
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}

		var recipientEpoch int64
		var recipientMembership string
		if e = q.QueryRow(ctx, statement(q, "SELECT u.auth_epoch,m.authority_id::text FROM users u JOIN workspace_memberships m ON m.user_id=u.id WHERE u.id=$1 AND u.status='active' AND m.tenant_id=$2 AND m.workspace_id=$3", "SELECT u.auth_epoch,m.authority_id FROM users u JOIN workspace_memberships m ON m.user_id=u.id WHERE u.id=? AND u.status='active' AND m.tenant_id=? AND m.workspace_id=?", `SELECT "U"."AUTH_EPOCH","M"."AUTHORITY_ID" FROM "USERS" "U" JOIN "WORKSPACE_MEMBERSHIPS" "M" ON "M"."USER_ID"="U"."ID" WHERE "U"."ID"=? AND "U"."STATUS"='active' AND "M"."TENANT_ID"=? AND "M"."WORKSPACE_ID"=?`, input.Recipient, w.TenantID, w.ID)).Scan(&recipientEpoch, &recipientMembership); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return fail("RESOURCE_NOT_FOUND")
			}
			return e
		}
		var creatorMembership *string
		if !admin(a) {
			var m string
			if e = q.QueryRow(ctx, statement(q, "SELECT authority_id::text FROM workspace_memberships WHERE tenant_id=$1 AND workspace_id=$2 AND user_id=$3", "SELECT authority_id FROM workspace_memberships WHERE tenant_id=? AND workspace_id=? AND user_id=?", `SELECT "AUTHORITY_ID" FROM "WORKSPACE_MEMBERSHIPS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "USER_ID"=?`, w.TenantID, w.ID, a.Me.User.ID)).Scan(&m); e != nil {
				return e
			}
			creatorMembership = &m
		}
		grant := uuid.NewString()
		if _, e = q.Exec(ctx, statement(q, `INSERT INTO artifact_object_grants(id,tenant_id,workspace_id,object_id,recipient_user_id,created_by,creator_epoch,creator_membership,recipient_epoch,recipient_membership,action,not_before,expires_at,creation_key,fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,clock_timestamp(),LEAST($12,clock_timestamp()+make_interval(secs=>$13)),$14,$15)`, `INSERT INTO artifact_object_grants(id,tenant_id,workspace_id,object_id,recipient_user_id,created_by,creator_epoch,creator_membership,recipient_epoch,recipient_membership,action,not_before,expires_at,creation_key,fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?,?,UTC_TIMESTAMP(6),LEAST(?,TIMESTAMPADD(SECOND,?,UTC_TIMESTAMP(6))),?,?)`, `INSERT INTO "ARTIFACT_OBJECT_GRANTS"("ID","TENANT_ID","WORKSPACE_ID","OBJECT_ID","RECIPIENT_USER_ID","CREATED_BY","CREATOR_EPOCH","CREATOR_MEMBERSHIP","RECIPIENT_EPOCH","RECIPIENT_MEMBERSHIP","ACTION","NOT_BEFORE","EXPIRES_AT","CREATION_KEY","FINGERPRINT") VALUES(?,?,?,?,?,?,?,?,?,?,?,SYS_EXTRACT_UTC(SYSTIMESTAMP(6)),LEAST(?,(SYS_EXTRACT_UTC(SYSTIMESTAMP(6))+NUMTODSINTERVAL(?,'SECOND'))),?,?)`, grant, w.TenantID, w.ID, o.ID, input.Recipient, a.Me.User.ID, a.Epoch, creatorMembership, recipientEpoch, recipientMembership, input.Action, o.ExpiresAt, input.Seconds, key, fingerprint[:])); e != nil {
			return e
		}
		if e = audit(ctx, q, a, w, "object.grant_created", o.ID, input.Reason, request); e != nil {
			return e
		}
		out, e = scanGrant(q.QueryRow(ctx, statement(q, "SELECT id::text,object_id::text,recipient_user_id::text,action,not_before,expires_at,revoked_at FROM artifact_object_grants WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4", "SELECT id,object_id,recipient_user_id,action,not_before,expires_at,revoked_at FROM artifact_object_grants WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=?", `SELECT "ID","OBJECT_ID","RECIPIENT_USER_ID","ACTION","NOT_BEFORE","EXPIRES_AT","REVOKED_AT" FROM "ARTIFACT_OBJECT_GRANTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=?`, w.TenantID, w.ID, o.ID, grant)))
		return e
	})
	if e != nil {
		return Grant{}, e
	}
	return out, nil
}
func scanGrant(row transaction.Row) (Grant, error) {
	var g Grant
	e := row.Scan(&g.ID, &g.ObjectID, &g.Recipient, &g.Action, &g.NotBefore, &g.ExpiresAt, &g.RevokedAt)
	if e != nil {
		return Grant{}, e
	}
	g.NotBefore, g.ExpiresAt = g.NotBefore.UTC(), g.ExpiresAt.UTC()
	if g.RevokedAt != nil {
		t := g.RevokedAt.UTC()
		g.RevokedAt = &t
	}
	return g, nil
}
func (s *Service) RevokeGrant(ctx context.Context, a identity.Auth, workspace, object, grant, reason, request string) error {
	ctx, finish, admissionError := s.begin(ctx)
	if admissionError != nil {
		return admissionError
	}
	defer finish()
	if !id(grant) || !id(request) || !workspaces.ValidReason(reason) {
		return fail("VALIDATION_FAILED")
	}
	return s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.writeObject(ctx, q, a, w, o, false); e != nil {
			return e
		}
		g, e := scanGrant(q.QueryRow(ctx, statement(q, "SELECT id::text,object_id::text,recipient_user_id::text,action,not_before,expires_at,revoked_at FROM artifact_object_grants WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4 FOR UPDATE", "SELECT id,object_id,recipient_user_id,action,not_before,expires_at,revoked_at FROM artifact_object_grants WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=? FOR UPDATE", `SELECT "ID","OBJECT_ID","RECIPIENT_USER_ID","ACTION","NOT_BEFORE","EXPIRES_AT","REVOKED_AT" FROM "ARTIFACT_OBJECT_GRANTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=? FOR UPDATE`, w.TenantID, w.ID, o.ID, grant)))
		if e != nil {
			return e
		}
		if g.RevokedAt != nil {
			return nil
		}
		if _, e = q.Exec(ctx, statement(q, "UPDATE artifact_object_grants SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND id=$4", "UPDATE artifact_object_grants SET revoked_at=UTC_TIMESTAMP(6) WHERE tenant_id=? AND workspace_id=? AND object_id=? AND id=?", `UPDATE "ARTIFACT_OBJECT_GRANTS" SET "REVOKED_AT"=SYS_EXTRACT_UTC(SYSTIMESTAMP(6)) WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND "ID"=?`, w.TenantID, w.ID, o.ID, grant)); e != nil {
			return e
		}
		return audit(ctx, q, a, w, "object.grant_revoked", o.ID, reason, request)
	})
}

func (s *Service) ListGrants(ctx context.Context, a identity.Auth, workspace, object, after string, limit int) ([]Grant, error) {
	ctx, done, e := s.begin(ctx)
	if e != nil {
		return nil, e
	}
	defer done()
	if after != "" && !id(after) || limit < 1 || limit > MaximumObjectPageSize {
		return nil, fail("VALIDATION_FAILED")
	}
	out := []Grant{}
	e = s.within(ctx, a, workspace, "", func(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
		o, e := s.objectWithin(ctx, q, w, object)
		if e != nil {
			return e
		}
		if e = s.writeObject(ctx, q, a, w, o, false); e != nil {
			return e
		}
		rows, e := q.Query(ctx, statement(q, "SELECT id::text,object_id::text,recipient_user_id::text,action,not_before,expires_at,revoked_at FROM artifact_object_grants WHERE tenant_id=$1 AND workspace_id=$2 AND object_id=$3 AND ($4::text='' OR id>NULLIF($4::text,'')::uuid) ORDER BY id LIMIT $5", "SELECT id,object_id,recipient_user_id,action,not_before,expires_at,revoked_at FROM artifact_object_grants WHERE tenant_id=? AND workspace_id=? AND object_id=? AND (?='' OR id>?) ORDER BY id LIMIT ?", `SELECT "ID","OBJECT_ID","RECIPIENT_USER_ID","ACTION","NOT_BEFORE","EXPIRES_AT","REVOKED_AT" FROM "ARTIFACT_OBJECT_GRANTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "OBJECT_ID"=? AND (? IS NULL OR "ID">?) ORDER BY "ID" LIMIT `+strconv.Itoa(limit), grantListArgs(q, w, object, after, limit)...))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			g, e := scanGrant(rows)
			if e != nil {
				return e
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	if e != nil {
		return nil, e
	}
	return out, nil
}
func grantListArgs(q transaction.Executor, w workspaces.Workspace, object, after string, limit int) []any {
	if q.Dialect() == transaction.Dameng {
		return []any{w.TenantID, w.ID, object, damengCursor(after), damengCursor(after)}
	}
	if positionalArguments(q) {
		return []any{w.TenantID, w.ID, object, after, after, limit}
	}
	return []any{w.TenantID, w.ID, object, after, limit}
}

func grantAuthorityArgs(q transaction.Executor, w workspaces.Workspace, object, grant, user, action string) []any {
	if positionalArguments(q) {
		return []any{w.TenantID, w.ID, object, grant, user, action, action}
	}
	return []any{w.TenantID, w.ID, object, grant, user, action}
}
