//go:build repository_integration

package nodes_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/nodes"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

type nodeEnrollmentObservedStore struct {
	transaction.Store
	attempt chan struct{}
	once    sync.Once
}
type nodeEnrollmentObservedExecutor struct {
	transaction.Executor
	owner *nodeEnrollmentObservedStore
}
type nodeEnrollmentObservedRow struct {
	ctx   context.Context
	q     transaction.Executor
	sql   transaction.Statement
	owner *nodeEnrollmentObservedStore
}

func (s *nodeEnrollmentObservedStore) Within(ctx context.Context, read bool, fn func(context.Context, transaction.Executor) error) error {
	return s.Store.Within(ctx, read, func(ctx context.Context, q transaction.Executor) error {
		return fn(ctx, nodeEnrollmentObservedExecutor{q, s})
	})
}
func (q nodeEnrollmentObservedExecutor) QueryRow(ctx context.Context, sql transaction.Statement) transaction.Row {
	return nodeEnrollmentObservedRow{ctx, q.Executor, sql, q.owner}
}
func (r nodeEnrollmentObservedRow) Scan(dest ...any) error {
	// The first single-field projection is the real Workspace status fence.
	// Only timing is observed; the unchanged statement/result uses the real driver.
	if len(dest) == 1 {
		r.owner.once.Do(func() { close(r.owner.attempt) })
	}
	return r.q.QueryRow(r.ctx, r.sql).Scan(dest...)
}

func TestActualNodeEnrollmentConcurrency(t *testing.T) {
	for _, kind := range []string{"parallel_distinct_token_capacity", "parallel_same_token", "enrollment_versus_revocation", "workspace_disabled_after_wait"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			handshake := nodeEnrollmentInput(wire.ReadOnlyProfile)
			one := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic concurrent enrollment one", wire.ReadOnlyProfile)
			switch kind {
			case "parallel_distinct_token_capacity", "parallel_same_token":
				tokens := []nodes.MintedToken{one, one}
				code := "TOKEN_UNUSABLE"
				if kind == "parallel_distinct_token_capacity" {
					workspace := uuid.NewString()
					x.exec(t, `INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Synthetic parallel capacity Workspace','active')`,
						`INSERT INTO workspaces(id,tenant_id,name,status) VALUES(?,?,'Synthetic parallel capacity Workspace','active')`, workspace, x.f.Tenant)
					tokens[1] = nodeEnrollmentMint(t, x, workspace, "Synthetic concurrent enrollment two", wire.ReadOnlyProfile)
					policy := config.DefaultRuntimePolicy()
					policy.Nodes.MaximumLiveNodes = 10 // Nine explicit existing rows leave exactly one slot.
					x.owner.Config.Runtime = &policy
					code = "RESOURCE_LIMIT_REACHED"
				}
				type result struct {
					value wire.Enrolled
					err   error
				}
				start, done := make(chan struct{}), make(chan result, 2)
				before := x.audits(t)
				for _, token := range tokens {
					go func(token nodes.MintedToken) {
						<-start
						v, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: token.Token, Handshake: handshake}, uuid.NewString())
						done <- result{v, e}
					}(token)
				}
				close(start)
				wins, losses := 0, 0
				for n := 0; n < 2; n++ {
					select {
					case r := <-done:
						if r.err == nil {
							wins++
							if r.value.NodeID == "" || r.value.Credential == "" {
								t.Fatal("actual enrollment winner missing credential")
							}
						} else {
							losses++
							nodeReadWant(t, r.err, code)
							if !reflect.DeepEqual(r.value, wire.Enrolled{}) {
								t.Fatal("actual losing enrollment exposed credential")
							}
						}
					case <-x.ctx.Done():
						t.Fatal("actual parallel enrollment did not complete")
					}
				}
				var count, consumed int
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM managed_nodes WHERE tenant_id=$1 AND status<>'removed'`,
					`SELECT count(*) FROM managed_nodes WHERE tenant_id=? AND status<>'removed'`, x.f.Tenant)).Scan(&count); e != nil {
					t.Fatal(e)
				}
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT count(*) FROM node_join_tokens WHERE tenant_id=$1 AND consumed_at IS NOT NULL`,
					`SELECT count(*) FROM node_join_tokens WHERE tenant_id=? AND consumed_at IS NOT NULL`, x.f.Tenant)).Scan(&consumed); e != nil {
					t.Fatal(e)
				}
				if wins != 1 || losses != 1 || count != 10 || consumed != 1 || x.audits(t) != before+1 {
					t.Fatal("actual capacity/token/system audit concurrency invariant failed")
				}
			case "enrollment_versus_revocation":
				type result struct {
					enrolled  wire.Enrolled
					revoked   nodes.JoinToken
					err       error
					operation string
				}
				start, done := make(chan struct{}), make(chan result, 2)
				before := x.audits(t)
				go func() {
					<-start
					v, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: one.Token, Handshake: handshake}, uuid.NewString())
					done <- result{enrolled: v, err: e, operation: "enroll"}
				}()
				go func() {
					<-start
					v, e := x.s.RevokeToken(x.ctx, x.admin, one.ID, "Synthetic actual parallel revoke", uuid.NewString())
					done <- result{revoked: v, err: e, operation: "revoke"}
				}()
				close(start)
				wins, losses := 0, 0
				for n := 0; n < 2; n++ {
					select {
					case r := <-done:
						if r.err == nil {
							wins++
						} else {
							losses++
							code := "TOKEN_UNUSABLE"
							if r.operation == "revoke" {
								code = "NODE_JOIN_STATE_CONFLICT"
							}
							nodeReadWant(t, r.err, code)
							if !reflect.DeepEqual(r.enrolled, wire.Enrolled{}) || !reflect.DeepEqual(r.revoked, nodes.JoinToken{}) {
								t.Fatal("actual losing terminal transition exposed private output")
							}
						}
					case <-x.ctx.Done():
						t.Fatal("actual enrollment/revocation race did not complete")
					}
				}
				var consumed, revoked bool
				if e := x.f.Store.QueryRow(x.ctx, x.f.Statement(`SELECT consumed_at IS NOT NULL,revoked_at IS NOT NULL FROM node_join_tokens WHERE id=$1`,
					`SELECT consumed_at IS NOT NULL,revoked_at IS NOT NULL FROM node_join_tokens WHERE id=?`, one.ID)).Scan(&consumed, &revoked); e != nil {
					t.Fatal(e)
				}
				if wins != 1 || losses != 1 || consumed == revoked || x.audits(t) != before+1 {
					t.Fatal("actual token had multiple terminal winners/audits")
				}
			case "workspace_disabled_after_wait":
				before, audits := nodeReadStoredDigest(t, x), x.audits(t)
				stop := nodeReadHoldWorkspace(t, x, func(ctx context.Context, q transaction.Executor) error {
					_, e := q.Exec(ctx, x.f.Statement(`UPDATE workspaces SET status='disabled',version=version+1 WHERE id=$1`,
						`UPDATE workspaces SET status='disabled',version=version+1 WHERE id=?`, x.f.Workspace))
					return e
				})
				observed := &nodeEnrollmentObservedStore{Store: x.f.Store, attempt: make(chan struct{})}
				x.owner.Storage, x.owner.DB = observed, nil
				x.s.DB, x.s.Workspaces.DB = nil, nil
				type result struct {
					value wire.Enrolled
					err   error
				}
				done := make(chan result, 1)
				go func() {
					v, e := x.s.Enroll(x.ctx, wire.Enrollment{JoinToken: one.Token, Handshake: handshake}, uuid.NewString())
					done <- result{v, e}
				}()
				select {
				case <-observed.attempt:
				case r := <-done:
					t.Fatal("enrollment did not reach actual Workspace fence", r.err)
				case <-x.ctx.Done():
					t.Fatal("Workspace admission timed out")
				}
				select {
				case r := <-done:
					t.Fatal("enrollment escaped real held Workspace fence", r.err)
				case <-time.After(100 * time.Millisecond):
				}
				stop()
				select {
				case r := <-done:
					nodeReadWant(t, r.err, "ACCESS_DENIED")
					if !reflect.DeepEqual(r.value, wire.Enrolled{}) || nodeReadStoredDigest(t, x) != before || x.audits(t) != audits {
						t.Fatal("waiting enrollment consumed token/exposed credential after Workspace disable")
					}
				case <-x.ctx.Done():
					t.Fatal("waiting enrollment did not recover after lock release")
				}
			}
		})
	}
}

func TestActualNodeEnrollmentProfiles(t *testing.T) {
	for _, kind := range []string{"token_profile", "management_os", "management_version", "management_architecture", "protocol", "capability_order"} {
		t.Run(kind, func(t *testing.T) {
			x := newNodeReadActual(t)
			token := nodeEnrollmentMint(t, x, x.f.Workspace, "Synthetic profile-refused enrollment", wire.SystemdMaintenanceProfile)
			handshake := nodeEnrollmentInput(wire.SystemdMaintenanceProfile)
			code := "AGENT_PROFILE_REJECTED"
			switch kind {
			case "token_profile":
				handshake = nodeEnrollmentInput(wire.ReadOnlyProfile)
			case "management_os":
				handshake.Facts.OSID = "debian"
			case "management_version":
				handshake.Facts.OSVersion = "22.04"
			case "management_architecture":
				handshake.Facts.Architecture = "arm64"
			case "protocol":
				handshake.ProtocolVersion = "unsupported"
				code = "AGENT_PROTOCOL_UNSUPPORTED"
			case "capability_order":
				handshake.Capabilities[0], handshake.Capabilities[1] = handshake.Capabilities[1], handshake.Capabilities[0]
				code = "AGENT_PROTOCOL_UNSUPPORTED"
			}
			nodeEnrollmentRefused(t, x, token.Token, handshake, code)
		})
	}
}
