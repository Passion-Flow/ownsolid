//go:build repository_integration

package nodes_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func nodeJoinSecondAdmin(t *testing.T, x nodeReadActual) identity.Auth {
	t.Helper()
	id := uuid.NewString()
	email := "synthetic-admin+" + id + "@example.invalid"
	x.exec(t, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$3,'Synthetic second join administrator','en','active',true)`, `INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES(?,?,?,'Synthetic second join administrator','en','active',true)`, id, email, email)
	return x.issue(t, id, "admin")
}
func TestActualNodeJoinConcurrency(t *testing.T) {
	for _, kind := range []string{"different_admins_capacity_one", "parallel_revoke_same_token", "mint_workspace_disabled_after_wait", "revoke_workspace_disabled_after_wait"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			switch kind {
			case "different_admins_capacity_one":
				peer := nodeJoinSecondAdmin(t, x)
				policy := config.DefaultRuntimePolicy()
				policy.Nodes.MaximumLiveJoinTokens = 1
				x.owner.Config.Runtime = &policy
				before := x.audits(t)
				start := make(chan struct{})
				type result struct {
					value nodes.MintedToken
					err   error
				}
				done := make(chan result, 2)
				for _, actor := range []identity.Auth{x.admin, peer} {
					go func(a identity.Auth) {
						<-start
						v, e := x.s.Mint(x.ctx, a, x.f.Workspace, "Synthetic parallel join "+uuid.NewString(), uuid.NewString())
						done <- result{v, e}
					}(actor)
				}
				close(start)
				success, refused := 0, 0
				for n := 0; n < 2; n++ {
					select {
					case r := <-done:
						if r.err == nil {
							success++
							if r.value.Token == "" || r.value.ID == "" {
								t.Fatal("parallel winner missing token")
							}
						} else {
							nodeReadWant(t, r.err, "RESOURCE_LIMIT_REACHED")
							refused++
							if !reflect.DeepEqual(r.value, nodes.MintedToken{}) {
								t.Fatal("capacity loser exposed token")
							}
						}
					case <-x.ctx.Done():
						t.Fatal("parallel actual capacity did not complete")
					}
				}
				var count int
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM node_join_tokens WHERE tenant_id=$1 AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>clock_timestamp()`, `SELECT count(*) FROM node_join_tokens WHERE tenant_id=? AND consumed_at IS NULL AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6)`, x.f.Tenant)).Scan(&count); e != nil {
					t.Fatal(e)
				}
				if success != 1 || refused != 1 || count != 1 || x.audits(t) != before+1 {
					t.Fatal("actual concurrent capacity/audit invariant failed")
				}
			case "parallel_revoke_same_token":
				pending := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
				peer := nodeJoinSecondAdmin(t, x)
				before := x.audits(t)
				start := make(chan struct{})
				type result struct {
					value nodes.JoinToken
					err   error
				}
				done := make(chan result, 2)
				for _, actor := range []identity.Auth{x.admin, peer} {
					go func(a identity.Auth) {
						<-start
						v, e := x.s.RevokeToken(x.ctx, a, pending.ID, "Synthetic parallel revoke", uuid.NewString())
						done <- result{v, e}
					}(actor)
				}
				close(start)
				success, refused := 0, 0
				for n := 0; n < 2; n++ {
					select {
					case r := <-done:
						if r.err == nil {
							success++
							if r.value.ID != pending.ID || r.value.Status != "revoked" {
								t.Fatal("parallel revoke winner missing durable state")
							}
						} else {
							nodeReadWant(t, r.err, "NODE_JOIN_STATE_CONFLICT")
							refused++
							if !reflect.DeepEqual(r.value, nodes.JoinToken{}) {
								t.Fatal("revoke loser exposed DTO")
							}
						}
					case <-x.ctx.Done():
						t.Fatal("parallel actual revocation did not complete")
					}
				}
				var revoked bool
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT revoked_at IS NOT NULL FROM node_join_tokens WHERE id=$1`, `SELECT revoked_at IS NOT NULL FROM node_join_tokens WHERE id=?`, pending.ID)).Scan(&revoked); e != nil {
					t.Fatal(e)
				}
				if success != 1 || refused != 1 || !revoked || x.audits(t) != before+1 {
					t.Fatal("actual parallel revoke duplicated mutation/audit")
				}
			default:
				pending := nodeJoinMint(t, x, x.f.Workspace, wire.ReadOnlyProfile)
				before := x.audits(t)
				stop := nodeReadHoldWorkspace(t, x, func(ctx context.Context, q transaction.Executor) error {
					_, e := q.Exec(ctx, x.f.Statement(`UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1`, `UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?`, x.f.Workspace))
					return e
				})
				// This existing explicit observer forwards the real SQL and Scan result;
				// only the entry signal is observed. The database owns the actual lock.
				observed := &nodeReadScopeStore{Store: x.f.Store, attempt: make(chan struct{})}
				x.owner.Storage, x.owner.DB = observed, nil
				x.s.DB, x.s.Workspaces.DB = nil, nil
				type result struct {
					minted  nodes.MintedToken
					revoked nodes.JoinToken
					err     error
				}
				done := make(chan result, 1)
				mint := strings.HasPrefix(kind, "mint_")
				go func() {
					if mint {
						v, e := x.s.Mint(x.ctx, x.admin, x.f.Workspace, "Synthetic availability wait", uuid.NewString())
						done <- result{minted: v, err: e}
					} else {
						v, e := x.s.RevokeToken(x.ctx, x.admin, pending.ID, "Synthetic availability wait revoke", uuid.NewString())
						done <- result{revoked: v, err: e}
					}
				}()
				select {
				case <-observed.attempt:
				case r := <-done:
					t.Fatal("join write did not reach actual Workspace fence", r.err)
				case <-x.ctx.Done():
					t.Fatal("join fence entry timeout")
				}
				select {
				case r := <-done:
					t.Fatal("join write escaped held real Workspace lock", r.err)
				case <-time.After(100 * time.Millisecond):
				}
				stop()
				select {
				case r := <-done:
					if mint {
						nodeReadWant(t, r.err, "ACCESS_DENIED")
						if !reflect.DeepEqual(r.minted, nodes.MintedToken{}) || x.audits(t) != before {
							t.Fatal("post-wait unavailable Workspace minted secret/audit")
						}
					} else if r.err != nil || r.revoked.ID != pending.ID || r.revoked.Status != "revoked" || x.audits(t) != before+1 {
						t.Fatal("Admin post-wait revocation recovery failed", r.err)
					}
				case <-x.ctx.Done():
					t.Fatal("join write did not complete after lock release")
				}
			}
		})
	}
}
