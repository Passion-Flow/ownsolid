//go:build repository_integration

package nodes_test

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/components"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/commitfault"
)

// Construct the original product graph, including all cross-domain references.
// Hand-built observer services cannot establish production orphan recovery.
func nodeRecoveryProduction(t *testing.T, owner *identity.Service) *nodes.Service {
	t.Helper()
	h := transport.New(owner)
	if h.Nodes == nil || h.Components == nil || h.Deployments == nil || h.Training == nil || h.ModelPreparations == nil {
		t.Fatal("production recovery graph missing")
	}
	s := h.Nodes.Service
	if s.Identity != owner || s.Components != h.Components.Service || s.Inference != h.Deployments.Service || s.Training != h.Training || s.Preparations != h.ModelPreparations || h.Components.Service.Nodes != s || h.Deployments.Service.Nodes != s || h.Training.Nodes != s || h.ModelPreparations.Nodes != s || h.Training.Holdouts != h.ModelPreparations {
		t.Fatal("production recovery did not retain original domain owners")
	}
	if e := s.CheckRecoveryOwners(); e != nil {
		t.Fatal("production recovery graph refused", e)
	}
	return s
}

// Preserve the real PostgreSQL pool boundary required by the production
// preparation constructor while forwarding its actual COMMIT through the proxy.
func nodeRecoveryFaultOwner(t *testing.T, x nodeReadActual) (*identity.Service, nodeReadWireFault) {
	t.Helper()
	owner := *x.owner
	if x.f.Store.Dialect() != transaction.PostgreSQL {
		store, proxy := nodeReadFaultStore(t, x)
		owner.DB, owner.Storage = nil, store
		return &owner, proxy
	}
	c := x.f.PostgreSQL.Config()
	if c.ConnConfig.TLSConfig != nil || len(c.ConnConfig.Fallbacks) != 0 || c.ConnConfig.Host != "127.0.0.1" {
		t.Fatal("explicit original plaintext loopback PG fixture required")
	}
	proxy, e := commitfault.NewPlaintextPostgreSQL(c.ConnConfig.Host, c.ConnConfig.Port)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := proxy.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	c.ConnConfig.Port, c.MaxConns = proxy.Port(), 2
	pool, e := pgxpool.NewWithConfig(x.ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(pool.Close)
	if e := pool.Ping(x.ctx); e != nil {
		t.Fatal(e)
	}
	owner.DB, owner.Storage = pool, nil
	return &owner, proxy
}

func TestActualNodeRemovalCommitAcknowledgementRecovery(t *testing.T) {
	for _, fault := range []struct {
		name      string
		mode      commitfault.Mode
		committed bool
	}{
		{"acknowledged", commitfault.Pass, true},
		{"commit_not_forwarded", commitfault.DropBeforeCommit, false},
		{"server_ACK_suppressed", commitfault.DropCommitAcknowledgement, true},
	} {
		t.Run(fault.name, func(t *testing.T) {
			x := newNodeReadActual(t)
			x.s = nodeRecoveryProduction(t, x.owner)
			join := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic recoverable component identity", wire.SystemdMaintenanceProfile)
			h := nodeEnrollmentInput(wire.SystemdMaintenanceProfile)
			enrolled, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: join.Token, Handshake: h}, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			o := wire.ComponentObservation{Component: wire.NodeExporter, Version: wire.NodeExporterVersion, State: "not_installed", ObservedAt: h.Facts.ObservedAt}
			h.Component = &o
			if _, e = x.s.Heartbeat(x.ctx, enrolled.NodeID, enrolled.Credential, h); e != nil {
				t.Fatal("native selected component observer was not wired", e)
			}
			component := x.s.Components.(*components.Service)
			task, e := component.Request(x.ctx, x.admin, enrolled.NodeID, "start", uuid.NewString(), "Explicit pending component intent", uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			claim, e := x.s.Claim(x.ctx, enrolled.NodeID, enrolled.Credential, uuid.NewString())
			if e != nil || claim.Lease == nil || claim.Lease.TaskID != task.ID {
				t.Fatal("actual component claim", e)
			}
			n, e := x.s.Get(x.ctx, x.admin, "", enrolled.NodeID)
			if e != nil {
				t.Fatal(e)
			}

			owner, proxy := nodeRecoveryFaultOwner(t, x)
			mutator := nodeRecoveryProduction(t, owner)
			if fault.mode != commitfault.Pass {
				if e = proxy.Arm(fault.mode); e != nil {
					t.Fatal(e)
				}
			}
			request := uuid.NewString()
			out, e := mutator.Update(x.ctx, x.admin, n.ID, n.Name, "removed", n.Version, "Explicit inspected node retirement", request)
			if fault.mode == commitfault.Pass {
				if e != nil || out.Status != "removed" {
					t.Fatal("acknowledged retirement", e)
				}
			} else {
				nodeReadWant(t, e, string(transaction.CommitUnknown))
				if !reflect.DeepEqual(out, nodes.Node{}) {
					t.Fatal("unknown removal exposed a node")
				}
				select {
				case observation := <-proxy.Outcomes():
					if observation.Mode != fault.mode || !observation.Suppressed || observation.Forwarded != fault.committed || observation.ServerOK != fault.committed {
						t.Fatal("actual removal COMMIT fault mismatch")
					}
				case <-x.ctx.Done():
					t.Fatal("actual removal fault observation missing")
				}
			}
			if proxy.Commits() != 1 {
				t.Fatal("removal retried uncertain COMMIT")
			}
			current, e := x.s.Get(x.ctx, x.admin, "", n.ID)
			if e != nil {
				t.Fatal(e)
			}
			wantStatus, wantState, wantAudits := "active", "running", 0
			if fault.committed {
				wantStatus, wantState, wantAudits = "removed", "cancelled", 1
			}
			var revoked, leaseCleared bool
			var taskState string
			if e = x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT credential_hash IS NULL FROM managed_nodes WHERE id=$1", "SELECT credential_hash IS NULL FROM managed_nodes WHERE id=?", n.ID)).Scan(&revoked); e != nil {
				t.Fatal(e)
			}
			if e = x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT state,lease_token_hash IS NULL AND lease_expires_at IS NULL FROM node_tasks WHERE id=$1", "SELECT state,lease_token_hash IS NULL AND lease_expires_at IS NULL FROM node_tasks WHERE id=?", task.ID)).Scan(&taskState, &leaseCleared); e != nil {
				t.Fatal(e)
			}
			var audits int
			if e = x.f.Store.QueryRow(x.ctx, x.f.Statement("SELECT count(*) FROM audit_events WHERE action='node.updated' AND target_id=$1 AND request_id=$2", "SELECT count(*) FROM audit_events WHERE action='node.updated' AND target_id=? AND request_id=?", n.ID, request)).Scan(&audits); e != nil {
				t.Fatal(e)
			}
			if current.Status != wantStatus || revoked != fault.committed || leaseCleared != fault.committed || taskState != wantState || audits != wantAudits {
				t.Fatal("independent node/credential/task/audit readback disagreed with COMMIT")
			}
			if !fault.committed {
				// Explicit new action only after authoritative absence of removal.
				current, e = x.s.Update(x.ctx, x.admin, current.ID, current.Name, "removed", current.Version, "Explicit retirement after readback", uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
			}
			if value, e := x.s.Renew(x.ctx, enrolled.NodeID, enrolled.Credential, task.ID, claim.Lease.LeaseToken); e == nil || !reflect.DeepEqual(value, wire.RenewedLease{}) {
				t.Fatal("retired identity retained lease authority")
			}
			if value, e := x.s.Heartbeat(x.ctx, enrolled.NodeID, enrolled.Credential, h); e == nil || !reflect.DeepEqual(value, wire.Disposition{}) {
				t.Fatal("retired identity republished a component observation")
			}
			fresh := nodeEnrollmentMint(t, x, x.f.Workspace, n.Name, wire.SystemdMaintenanceProfile)
			replacement, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: fresh.Token, Handshake: nodeEnrollmentInput(wire.SystemdMaintenanceProfile)}, uuid.NewString())
			if e != nil || replacement.NodeID == n.ID || replacement.Credential == enrolled.Credential {
				t.Fatal("deliberate fresh enrollment reused retired authority", e)
			}
			if proxy.Commits() != 1 {
				t.Fatal("recovery reused uncertain mutation connection")
			}
		})
	}
}
