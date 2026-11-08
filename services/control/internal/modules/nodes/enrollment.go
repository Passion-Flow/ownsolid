package nodes

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func enrollmentError(e error) error {
	if e == nil {
		return nil
	}
	switch e.Error() {
	case "TOKEN_UNUSABLE", "AGENT_PROFILE_REJECTED", "RESOURCE_LIMIT_REACHED", "NODE_NAME_EXISTS", "AGENT_CERTIFICATE_REQUEST_INVALID", "AGENT_IDENTITY_UNAVAILABLE":
		return fail(e.Error())
	default:
		return nodeRepositoryError(e)
	}
}

// The scope comes from the stored token. A shared Workspace fence precedes the
// exclusive installation and separate shared tenant read, matching Mint/Revoke.
func enrollmentScope(ctx context.Context, q transaction.Executor, hash []byte) (string, string, error) {
	var tenant, workspace string
	e := q.QueryRow(ctx, nodeSQL(q,
		`SELECT j.tenant_id::text,j.workspace_id::text FROM node_join_tokens j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete AND j.token_hash=$1 AND j.revoked_at IS NULL AND j.consumed_at IS NULL AND j.expires_at>clock_timestamp()`,
		`SELECT j.tenant_id,j.workspace_id FROM node_join_tokens j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND j.token_hash=? AND j.revoked_at IS NULL AND j.consumed_at IS NULL AND j.expires_at>UTC_TIMESTAMP(6)`, `SELECT j.tenant_id,j.workspace_id FROM node_join_tokens j JOIN installation_state i ON i.tenant_id=j.tenant_id WHERE i.id=1 AND i.bootstrap_complete=1 AND j.token_hash=? AND j.revoked_at IS NULL AND j.consumed_at IS NULL AND j.expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))`, hash)).Scan(&tenant, &workspace)
	if errors.Is(e, sql.ErrNoRows) {
		return "", "", fail("TOKEN_UNUSABLE")
	}
	if e != nil {
		return "", "", e
	}
	var status string
	e = q.QueryRow(ctx, nodeSQL(q,
		`SELECT w.status FROM workspaces w WHERE w.tenant_id=$1 AND w.id=$2 FOR SHARE OF w`,
		`SELECT w.status FROM workspaces w WHERE w.tenant_id=? AND w.id=? FOR SHARE`, `SELECT w.status FROM workspaces w WHERE w.tenant_id=? AND w.id=? FOR UPDATE`, tenant, workspace)).Scan(&status)
	if errors.Is(e, sql.ErrNoRows) {
		return "", "", fail("TOKEN_UNUSABLE")
	}
	if e != nil {
		return "", "", e
	}
	if status != "active" {
		return "", "", fail("ACCESS_DENIED")
	}
	var installed string
	var complete bool
	e = q.QueryRow(ctx, nodeSQL(q,
		`SELECT COALESCE(tenant_id::text,''),bootstrap_complete FROM installation_state WHERE id=1 FOR SHARE`,
		`SELECT COALESCE(tenant_id,''),bootstrap_complete FROM installation_state WHERE id=1 FOR UPDATE`, `SELECT COALESCE(tenant_id,''),bootstrap_complete FROM installation_state WHERE id=1 FOR UPDATE`)).Scan(&installed, &complete)
	if errors.Is(e, sql.ErrNoRows) || e == nil && (!complete || installed != tenant) {
		return "", "", fail("TOKEN_UNUSABLE")
	}
	if e != nil {
		return "", "", e
	}
	e = q.QueryRow(ctx, nodeSQL(q, `SELECT status FROM tenants WHERE id=$1 FOR SHARE`,
		`SELECT status FROM tenants WHERE id=? FOR SHARE`, `SELECT status FROM tenants WHERE id=? FOR UPDATE`, tenant)).Scan(&status)
	if errors.Is(e, sql.ErrNoRows) {
		return "", "", fail("TOKEN_UNUSABLE")
	}
	if e != nil {
		return "", "", e
	}
	if status != "active" {
		return "", "", fail("ACCESS_DENIED")
	}
	return tenant, workspace, nil
}

func (s *Service) Enroll(ctx context.Context, input wire.Enrollment, request string) (wire.Enrolled, error) {
	if ctx == nil || !nodeID(request) || input.Component != nil || input.Inference != nil {
		return wire.Enrolled{}, fail("VALIDATION_FAILED")
	}
	if !security.ValidToken(input.JoinToken) {
		return wire.Enrolled{}, fail("TOKEN_UNUSABLE")
	}
	if e := input.Handshake.Validate(); e != nil {
		return wire.Enrolled{}, e
	}
	facts, e := json.Marshal(input.Facts)
	if e != nil {
		return wire.Enrolled{}, fail("VALIDATION_FAILED")
	}
	capabilities, e := json.Marshal(input.Capabilities)
	if e != nil {
		return wire.Enrolled{}, fail("VALIDATION_FAILED")
	}
	store, e := s.repository()
	if e != nil {
		return wire.Enrolled{}, e
	}
	var certificateRequest *x509.CertificateRequest
	if s.Certificates != nil {
		if certificateRequest, e = wire.ParseCertificateRequest(input.CertificateRequest); e != nil {
			return wire.Enrolled{}, fail(e.Error())
		}
	}
	ctx, cancel := context.WithTimeout(ctx, config.Seconds(s.Identity.Config.Policy().HTTP.RequestSeconds))
	defer cancel()
	var out wire.Enrolled
	hash := digest("node-join", input.JoinToken)
	e = store.Within(ctx, false, func(ctx context.Context, q transaction.Executor) error {
		tenant, workspace, e := enrollmentScope(ctx, q, hash)
		if e != nil {
			return e
		}
		var tokenID, name, profile string
		e = q.QueryRow(ctx, nodeSQL(q,
			`SELECT id::text,node_name,execution_profile FROM node_join_tokens WHERE token_hash=$1 AND tenant_id=$2 AND workspace_id=$3 AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at>clock_timestamp() FOR UPDATE`,
			`SELECT id,node_name,execution_profile FROM node_join_tokens WHERE token_hash=? AND tenant_id=? AND workspace_id=? AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at>UTC_TIMESTAMP(6) FOR UPDATE`, `SELECT id,node_name,execution_profile FROM node_join_tokens WHERE token_hash=? AND tenant_id=? AND workspace_id=? AND revoked_at IS NULL AND consumed_at IS NULL AND expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) FOR UPDATE`, hash, tenant, workspace)).Scan(&tokenID, &name, &profile)
		if errors.Is(e, sql.ErrNoRows) {
			return fail("TOKEN_UNUSABLE")
		}
		if e != nil {
			return e
		}
		if input.Profile() != profile || (wire.IsManagementProfile(profile) || wire.IsInferenceProfile(profile)) &&
			(input.Facts.Architecture != "amd64" || input.Facts.OSID != "ubuntu" || input.Facts.OSVersion != "24.04") {
			return fail("AGENT_PROFILE_REJECTED")
		}
		count, e := liveNodesWithin(ctx, q, tenant)
		if e != nil {
			return e
		}
		if count >= s.Identity.Config.Policy().Nodes.MaximumLiveNodes {
			return fail("RESOURCE_LIMIT_REACHED")
		}
		id, credential := uuid.NewString(), security.Token()
		insert := nodeSQL(q,
			`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES($1,$2,$3,$4,'active',$5,$6,$7,clock_timestamp(),$8,$9)`,
			`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES(?,?,?,?,'active',?,?,?,UTC_TIMESTAMP(6),?,?)`, `INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities) VALUES(?,?,?,?,'active',?,?,?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),?,?)`,
			id, tenant, workspace, name, digest("node-credential", credential), input.ProtocolVersion, nodeJSON(q, facts), profile, nodeJSON(q, capabilities))
		var certificate string
		if certificateRequest != nil {
			pin, e := wire.KeyDigest(certificateRequest.PublicKey)
			if e != nil {
				return fail(e.Error())
			}
			var notAfter time.Time
			if certificate, notAfter, e = s.Certificates.Issue(certificateRequest, wire.LocalScope(), id); e != nil {
				return fail(e.Error())
			}
			insert = transaction.SQL(transaction.PostgreSQL,
				`INSERT INTO managed_nodes(id,tenant_id,workspace_id,name,status,credential_hash,protocol_version,facts,last_heartbeat_at,execution_profile,capabilities,certificate_key_sha256,certificate_not_after) VALUES($1,$2,$3,$4,'active',$5,$6,$7,clock_timestamp(),$8,$9,$10,$11)`,
				id, tenant, workspace, name, digest("node-credential", credential), input.ProtocolVersion, nodeJSON(q, facts), profile, nodeJSON(q, capabilities), pin, notAfter)
		}
		_, e = q.Exec(ctx, insert)
		var safe *transaction.Error
		if errors.As(e, &safe) && safe.Kind == transaction.UniqueConflict {
			return fail("NODE_NAME_EXISTS")
		}
		if e != nil {
			return e
		}
		changed, e := q.Exec(ctx, nodeSQL(q,
			`UPDATE node_join_tokens SET consumed_at=clock_timestamp(),node_id=$1 WHERE tenant_id=$2 AND workspace_id=$3 AND id=$4 AND token_hash=$5 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp()`,
			`UPDATE node_join_tokens SET consumed_at=UTC_TIMESTAMP(6),node_id=? WHERE tenant_id=? AND workspace_id=? AND id=? AND token_hash=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6)`, `UPDATE node_join_tokens SET consumed_at=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),node_id=? WHERE tenant_id=? AND workspace_id=? AND id=? AND token_hash=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6))`,
			id, tenant, workspace, tokenID, hash))
		if e != nil {
			return e
		}
		if changed != 1 {
			return fail("TOKEN_UNUSABLE")
		}
		_, e = q.Exec(ctx, nodeSQL(q,
			`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES($1,clock_timestamp(),NULL,$2,$3,'system','node.enrolled','success','node',$4,NULL,$5)`,
			`INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,UTC_TIMESTAMP(6),NULL,?,?,'system','node.enrolled','success','node',?,NULL,?)`, `INSERT INTO audit_events(id,occurred_at,actor_user_id,tenant_id,workspace_id,audience,action,outcome,target_type,target_id,reason,request_id) VALUES(?,SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)),NULL,?,?,'system','node.enrolled','success','node',?,NULL,?)`, uuid.NewString(), tenant, workspace, id, request))
		if e != nil {
			return e
		}
		// Read in this transaction: enrollment is rare, and a second one would hold two
		// connections while the token row is locked.
		pacing, e := readPacingWithin(ctx, q, "")
		if e != nil {
			return e
		}
		out = wire.Enrolled{NodeID: id, SiteID: wire.DefaultSiteID, CellID: wire.DefaultCellID, Credential: credential, ProtocolVersion: input.ProtocolVersion, HeartbeatSeconds: s.effectivePacing(pacing.Pacing).HeartbeatSeconds, Certificate: certificate}
		return nil
	})
	if e != nil {
		return wire.Enrolled{}, enrollmentError(e)
	}
	s.noteEnrolled(out.NodeID)
	return out, nil
}

// liveNodesWithin reads how many live nodes a tenant has and serializes enrollment against
// the limit. On PostgreSQL a trigger keeps the count, so the cost of enrolling does not
// grow with the fleet (ADR 0020); the row is created by the first read. The other chains
// lack the table until CMP-201 and CMP-202 and count the nodes.
func liveNodesWithin(ctx context.Context, q transaction.Executor, tenant string) (int, error) {
	var count int
	e := q.QueryRow(ctx, nodeSQL(q,
		`INSERT INTO tenant_node_counts(tenant_id,live_nodes) VALUES($1,0) ON CONFLICT(site_id,cell_id,tenant_id) DO UPDATE SET live_nodes=tenant_node_counts.live_nodes RETURNING live_nodes`,
		`SELECT count(*) FROM managed_nodes WHERE tenant_id=? AND status<>'removed'`, `SELECT count(*) FROM managed_nodes WHERE tenant_id=? AND status<>'removed'`, tenant)).Scan(&count)
	return count, e
}
