//go:build repository_integration

package nodes_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

func TestActualNodeEnrollmentCommitAcknowledgementLoss(t *testing.T) {
	t.Run("enroll", func(t *testing.T) {
		for _, fault := range []struct {
			name      string
			mode      commitfault.Mode
			committed bool
		}{
			{"normal_acknowledged_commit", commitfault.Pass, true},
			{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
			{"actual_server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
		} {
			t.Run(fault.name, func(t *testing.T) {
				x := newNodeReadActual(t)
				x.s = nodeRecoveryProduction(t, x.owner)
				pending := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic wire enrollment "+uuid.NewString(), wire.ReadOnlyProfile)
				handshake := nodeEnrollmentInput(wire.ReadOnlyProfile)
				handshake.ExecutionProfile = "" // Actual legacy implicit read_only path.
				before, request := x.audits(t), uuid.NewString()
				store, proxy := nodeReadFaultStore(t, x)
				owner := *x.owner
				owner.Storage, owner.DB = store, nil
				service := &nodes.Service{Identity: &owner, Workspaces: &workspaces.Service{Identity: &owner}}
				if fault.mode != commitfault.Pass {
					if e := proxy.Arm(fault.mode); e != nil {
						t.Fatal(e)
					}
				}
				value, e := service.Enroll(x.ctx, wire.Enrollment{JoinToken: pending.Token, Handshake: handshake}, request)
				if fault.mode == commitfault.Pass {
					if e != nil {
						t.Fatal("actual acknowledged enrollment", e)
					}
					nodeEnrollmentStored(t, x, pending, value, handshake, request)
				} else {
					nodeReadWant(t, e, string(transaction.CommitUnknown))
					if !reflect.DeepEqual(value, wire.Enrolled{}) {
						t.Fatal("unknown enrollment COMMIT exposed captured credential/DTO")
					}
					select {
					case observed := <-proxy.Outcomes():
						if observed.Mode != fault.mode || !observed.Suppressed || observed.Forwarded != fault.committed || observed.ServerOK != fault.committed {
							t.Fatal("real enrollment COMMIT observation mismatch")
						}
					case <-x.ctx.Done():
						t.Fatal("actual enrollment wire observation absent")
					}
				}
				if proxy.Commits() != 1 {
					t.Fatal("enrollment automatically resent a real COMMIT")
				}
				var count, audits int
				var nodeID *string
				var consumed bool
				if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM managed_nodes WHERE tenant_id=$1 AND workspace_id=$2 AND name=$3`,
					`SELECT count(*) FROM managed_nodes WHERE tenant_id=? AND workspace_id=? AND name=?`, x.f.Tenant, x.f.Workspace, pending.NodeName)).Scan(&count); e != nil {
					t.Fatal(e)
				}
				if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT node_id::text,consumed_at IS NOT NULL FROM node_join_tokens WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3`,
					`SELECT node_id,consumed_at IS NOT NULL FROM node_join_tokens WHERE id=? AND tenant_id=? AND workspace_id=?`, pending.ID, x.f.Tenant, x.f.Workspace)).Scan(&nodeID, &consumed); e != nil {
					t.Fatal(e)
				}
				if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND workspace_id=$2 AND audience='system' AND actor_user_id IS NULL AND action='node.enrolled' AND outcome='success' AND request_id=$3`,
					`SELECT count(*) FROM audit_events WHERE tenant_id=? AND workspace_id=? AND audience='system' AND actor_user_id IS NULL AND action='node.enrolled' AND outcome='success' AND request_id=?`, x.f.Tenant, x.f.Workspace, request)).Scan(&audits); e != nil {
					t.Fatal(e)
				}
				want := 0
				if fault.committed {
					want = 1
				}
				if count != want || audits != want || x.audits(t) != before+want || consumed != fault.committed || (nodeID != nil) != fault.committed {
					t.Fatal("independent enrollment node/token/audit COMMIT readback mismatch")
				}
				page, e := x.s.Tokens(x.ctx, x.admin, url.Values{"limit": {"100"}})
				if e != nil {
					t.Fatal("actual explicit enrollment history refresh", e)
				}
				raw, _ := json.Marshal(page)
				if bytes.Contains(raw, []byte(pending.Token)) || bytes.Contains(raw, []byte("credential")) || bytes.Contains(raw, []byte("token_hash")) {
					t.Fatal("authoritative history exposed enrollment inputs/credentials")
				}
				if fault.mode == commitfault.Pass {
					var hash []byte
					if e = x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT credential_hash FROM managed_nodes WHERE id=$1`,
						`SELECT credential_hash FROM managed_nodes WHERE id=?`, value.NodeID)).Scan(&hash); e != nil || !bytes.Equal(hash, security.Digest("node-credential:v1:"+value.Credential)) {
						t.Fatal("acknowledged wire credential mismatch", e)
					}
				} else if !fault.committed {
					// This explicit second action follows independent confirmed absence;
					// the uncertain forwarding store is never resent or reused.
					recovered, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: pending.Token, Handshake: handshake}, uuid.NewString())
					if e != nil || !security.ValidToken(recovered.Credential) {
						t.Fatal("explicit enrollment after authoritative absence failed", e)
					}
				} else {
					node, e := x.s.Get(x.ctx, x.admin, "", *nodeID)
					if e != nil || node.Status != "active" {
						t.Fatal("committed lost-credential node absent from authoritative inventory", e)
					}
					nodeEnrollmentRefused(t, x, pending.Token, handshake, "TOKEN_UNUSABLE")
					removed, e := x.s.Update(x.ctx, x.admin, node.ID, node.Name, "removed", node.Version, "Explicit orphan removal after uncertain enrollment inspection", uuid.NewString())
					if e != nil || removed.Status != "removed" {
						t.Fatal("actual selected Admin orphan removal failed", e)
					}
					fresh := nodeEnrollmentMint(t, x, x.f.Workspace, node.Name, wire.ReadOnlyProfile)
					if e := nodeEnrollmentAttempt(t, x, x.ctx, fresh.Token); e != nil {
						t.Fatal("actual selected fresh enrollment recovery failed", e)
					}
				}
				if proxy.Commits() != 1 {
					t.Fatal("inspection/manual recovery retried uncertain forwarding COMMIT")
				}
			})
		}
	})
}
