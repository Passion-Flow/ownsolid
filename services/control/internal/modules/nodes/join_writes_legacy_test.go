package nodes_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
)

func TestM20JoinEnrollmentRevocationInteraction(t *testing.T) {
	for _, kind := range []string{"revocation_before_enrollment", "enrollment_before_revocation", "parallel_single_terminal_transition"} {
		t.Run(kind, func(t *testing.T) {
			s, actor := setup(t)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			pending, e := s.Mint(ctx, actor, actor.Me.Workspace.ID, "Synthetic enrollment-revocation interaction", uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			type result struct {
				operation string
				enrolled  wire.Enrolled
				revoked   nodes.JoinToken
				err       error
			}
			enroll := func() result {
				v, e := s.Enroll(ctx, wire.Enrollment{JoinToken: pending.Token, Handshake: handshake()}, uuid.NewString())
				return result{operation: "enroll", enrolled: v, err: e}
			}
			revoke := func() result {
				v, e := s.RevokeToken(ctx, actor, pending.ID, "Synthetic concurrent enrollment recovery", uuid.NewString())
				return result{operation: "revoke", revoked: v, err: e}
			}
			var results []result
			switch kind {
			case "revocation_before_enrollment":
				results = append(results, revoke(), enroll())
			case "enrollment_before_revocation":
				results = append(results, enroll(), revoke())
			default:
				start := make(chan struct{})
				done := make(chan result, 2)
				go func() { <-start; done <- enroll() }()
				go func() { <-start; done <- revoke() }()
				close(start)
				for n := 0; n < 2; n++ {
					select {
					case r := <-done:
						results = append(results, r)
					case <-ctx.Done():
						t.Fatal("real enrollment/revoke race did not complete")
					}
				}
			}
			winners, enrolled := 0, 0
			nodeID := ""
			for _, r := range results {
				if r.err == nil {
					winners++
					if r.operation == "enroll" {
						enrolled++
						nodeID = r.enrolled.NodeID
						if !security.ValidToken(r.enrolled.Credential) || nodeID == "" {
							t.Fatal("real enrollment winner missing credential/node")
						}
					} else if r.revoked.ID != pending.ID || r.revoked.Status != "revoked" {
						t.Fatal("real revoke winner missing transition")
					}
				} else if r.operation == "enroll" {
					expected(t, r.err, "TOKEN_UNUSABLE")
					if !reflect.DeepEqual(r.enrolled, wire.Enrolled{}) {
						t.Fatal("losing enrollment exposed credential/DTO")
					}
				} else {
					expected(t, r.err, "NODE_JOIN_STATE_CONFLICT")
					if !reflect.DeepEqual(r.revoked, nodes.JoinToken{}) {
						t.Fatal("losing revoke exposed private DTO")
					}
				}
			}
			if winners != 1 || kind == "revocation_before_enrollment" && enrolled != 0 || kind == "enrollment_before_revocation" && enrolled != 1 {
				t.Fatal("real terminal transition has wrong winner")
			}
			var consumed, revoked bool
			var storedNode string
			if e := s.DB.QueryRow(ctx, `SELECT consumed_at IS NOT NULL,revoked_at IS NOT NULL,COALESCE(node_id::text,'') FROM node_join_tokens WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3`, pending.ID, pending.TenantID, pending.WorkspaceID).Scan(&consumed, &revoked, &storedNode); e != nil {
				t.Fatal(e)
			}
			var nodeCount, enrollAudits, revokeAudits int
			if e := s.DB.QueryRow(ctx, `SELECT count(*) FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2`, pending.TenantID, pending.WorkspaceID).Scan(&nodeCount); e != nil {
				t.Fatal(e)
			}
			if e := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE action='node.enrolled' AND audience='system' AND actor_user_id IS NULL),count(*) FILTER(WHERE action='node.join_token_revoked' AND audience='admin' AND actor_user_id=$3) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND outcome='success'`, pending.TenantID, pending.WorkspaceID, actor.Me.User.ID).Scan(&enrollAudits, &revokeAudits); e != nil {
				t.Fatal(e)
			}
			if consumed != (enrolled == 1) || revoked != (enrolled == 0) || storedNode != nodeID || nodeCount != enrolled || enrollAudits != enrolled || revokeAudits != 1-enrolled {
				t.Fatal("real node/token/scoped audit terminal state disagrees")
			}
			t.Logf("actual_original_PG=true interaction=%s terminal_winners=1 enrolled=%d scoped_audits=1", kind, enrolled)
		})
	}
}
