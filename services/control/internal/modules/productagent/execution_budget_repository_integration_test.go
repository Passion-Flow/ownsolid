//go:build repository_integration

package productagent

import (
	"context"
	_ "embed"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/repositoryfixture"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/execution-budget.sql
var executionBudgetSchema string

type budgetRepositoryFixture struct {
	t           *testing.T
	f           repositoryfixture.Fixture
	ctx         context.Context
	policy      atomic.Pointer[ExecutionBudgetPolicy]
	actors      [3]identity.Auth
	gates       [2]*ExecutionBudgets
	replicaPool *pgxpool.Pool
}

func newBudgetRepositoryFixture(t *testing.T) *budgetRepositoryFixture {
	t.Helper()
	f := repositoryfixture.New(t)
	if f.PostgreSQL == nil {
		t.Skip("Durable assistant reservations require PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	t.Cleanup(cancel)
	g := &budgetRepositoryFixture{t: t, f: f, ctx: ctx}
	if _, err := f.PostgreSQL.Exec(ctx, executionBudgetSchema); err != nil {
		t.Fatal(err)
	}
	owner := &identity.Service{Storage: f.Store}
	for i, audience := range []string{"admin", "portal", "portal"} {
		user := f.User
		if i == 2 {
			user = uuid.NewString()
			email := user + "@example.invalid"
			g.exec(`INSERT INTO users(id,email_canonical,email_display,display_name,locale,status,is_platform_admin) VALUES($1,$2,$2,'Budget second user','en','active',false)`, user, email)
		}
		if i != 1 {
			g.exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`, user, f.Tenant, f.Workspace, uuid.NewString())
		}
		var issued identity.IssuedSession
		if err := f.Store.Within(ctx, false, func(ctx context.Context, tx transaction.Executor) error {
			var err error
			issued, err = owner.IssueSessionWithin(ctx, tx, user, 1, audience, "", uuid.NewString(), nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		actor, err := owner.Authenticate(ctx, issued.Token, audience)
		if err != nil || actor.Me.Workspace == nil || actor.Me.Workspace.ID != f.Workspace {
			t.Fatalf("budget fixture authentication: %v", err)
		}
		g.actors[i] = actor
	}
	config := f.PostgreSQL.Config()
	config.ConnConfig.RuntimeParams["application_name"] = "assistant-budget-" + uuid.NewString()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	g.replicaPool = pool
	replica, err := transaction.FromPostgreSQL(pool)
	if err != nil {
		t.Fatal(err)
	}
	g.gates[0] = g.gate(f.Store)
	g.gates[1] = g.gate(replica)
	g.reset(t)
	return g
}

func (g *budgetRepositoryFixture) gate(store transaction.Store) *ExecutionBudgets {
	g.t.Helper()
	owner := &identity.Service{Storage: store}
	apps := &applications.Service{Identity: owner, Workspaces: &workspaces.Service{Identity: owner}}
	commands, err := New(apps, func(context.Context) (bool, error) { return true, nil })
	if err != nil {
		g.t.Fatal(err)
	}
	gate, err := NewExecutionBudgets(commands, func(context.Context, transaction.Executor, BudgetSubject) (ExecutionBudgetPolicy, error) {
		return *g.policy.Load(), nil
	})
	if err != nil {
		g.t.Fatal(err)
	}
	return gate
}

func (g *budgetRepositoryFixture) exec(query string, args ...any) {
	g.t.Helper()
	if _, err := g.f.PostgreSQL.Exec(g.ctx, query, args...); err != nil {
		g.t.Fatal(err)
	}
}

func (g *budgetRepositoryFixture) reset(t *testing.T) {
	g.t = t
	g.t.Helper()
	g.exec(`TRUNCATE product_agent_steps,product_agent_runs,product_agent_budget_reservations,product_agent_budget_users,product_agent_budget_installations`)
	start := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	g.policy.Store(&ExecutionBudgetPolicy{
		UserAccess: BudgetAccessEnabled, WindowStart: start, WindowEnd: start.Add(3 * time.Hour),
		Installation: BudgetCeiling{100, 1000}, User: BudgetCeiling{100, 1000},
	})
}

func (g *budgetRepositoryFixture) counters(user string, want BudgetCeiling, reservations int) {
	g.t.Helper()
	var got BudgetCeiling
	p := g.policy.Load()
	query := `SELECT executions,tokens FROM product_agent_budget_installations WHERE window_start=$1 AND window_end=$2`
	args := []any{p.WindowStart, p.WindowEnd}
	if user != "" {
		query = `SELECT executions,tokens FROM product_agent_budget_users WHERE user_id=$3 AND window_start=$1 AND window_end=$2`
		args = append(args, user)
	}
	if err := g.f.PostgreSQL.QueryRow(g.ctx, query, args...).Scan(&got.Executions, &got.Tokens); err != nil || got != want {
		g.t.Fatalf("durable counters %+v, want %+v: %v", got, want, err)
	}
	var count int
	if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*) FROM product_agent_budget_reservations`).Scan(&count); err != nil || count != reservations {
		g.t.Fatalf("durable reservation count %d, want %d: %v", count, reservations, err)
	}
}

func budgetTestExecution(tokens int64) BudgetExecution {
	return BudgetExecution{ID: uuid.NewString(), ExecutionSHA256: strings.Repeat("a", 64), MaximumTokens: tokens}
}

type budgetTestOutcome struct {
	reservation BudgetReservation
	err         error
}

func concurrentBudgetCalls(n int, call func(int) (BudgetReservation, error)) []budgetTestOutcome {
	results := make([]budgetTestOutcome, n)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range n {
		workers.Go(func() {
			<-start
			results[i].reservation, results[i].err = call(i)
		})
	}
	close(start)
	workers.Wait()
	return results
}

func TestDurableExecutionBudgets(t *testing.T) {
	g := newBudgetRepositoryFixture(t)
	for _, tc := range []struct {
		name       string
		ceiling    BudgetCeiling
		actors     []identity.Auth
		userScoped bool
		admitted   int64
	}{
		{"user ceiling across entrances", BudgetCeiling{3, 1000}, g.actors[:2], true, 3},
		{"user token ceiling across entrances", BudgetCeiling{100, 6}, g.actors[:2], true, 3},
		{"installation ceiling across users", BudgetCeiling{4, 1000}, g.actors[1:], false, 4},
		{"installation token ceiling across users", BudgetCeiling{100, 8}, g.actors[1:], false, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g.reset(t)
			p := *g.policy.Load()
			if tc.userScoped {
				p.User = tc.ceiling
			} else {
				p.Installation = tc.ceiling
			}
			g.policy.Store(&p)
			results := concurrentBudgetCalls(16, func(i int) (BudgetReservation, error) {
				return g.gates[i%2].Reserve(g.ctx, tc.actors[i%len(tc.actors)], budgetTestExecution(2))
			})
			admitted := int64(0)
			for _, result := range results {
				if result.err == nil && result.reservation.State == BudgetNew {
					admitted++
				} else if result.err != ErrBudgetExceeded || result.reservation != (BudgetReservation{}) {
					t.Fatalf("concurrent admission: %+v, %v", result.reservation, result.err)
				}
			}
			if admitted != tc.admitted {
				t.Fatalf("admitted %d, want %d", admitted, tc.admitted)
			}
			used := BudgetCeiling{admitted, 2 * admitted}
			g.counters("", used, int(admitted))
			if tc.userScoped {
				g.counters(g.f.User, used, int(admitted))
			}
		})
	}

	t.Run("duplicate binding and settlement races", func(t *testing.T) {
		g.reset(t)
		execution := budgetTestExecution(7)
		newCount := 0
		for _, result := range concurrentBudgetCalls(12, func(i int) (BudgetReservation, error) {
			return g.gates[i%2].Reserve(g.ctx, g.actors[1], execution)
		}) {
			if result.err != nil || result.reservation.ID != execution.ID {
				t.Fatalf("duplicate admission: %+v, %v", result.reservation, result.err)
			}
			switch result.reservation.State {
			case BudgetNew:
				newCount++
			case BudgetHeld:
			default:
				t.Fatalf("unexpected duplicate state: %s", result.reservation.State)
			}
		}
		if newCount != 1 {
			t.Fatalf("duplicate identity admitted %d executions", newCount)
		}
		g.counters("", BudgetCeiling{1, 7}, 1)
		changed := execution
		changed.ExecutionSHA256 = strings.Repeat("b", 64)
		g.refusal(t, g.gates[0], g.actors[1], changed, "IDEMPOTENCY_CONFLICT")
		changed = execution
		changed.MaximumTokens++
		g.refusal(t, g.gates[0], g.actors[1], changed, "IDEMPOTENCY_CONFLICT")
		g.refusal(t, g.gates[1], g.actors[0], execution, "RESOURCE_NOT_FOUND")
		g.refusal(t, g.gates[1], g.actors[2], execution, "RESOURCE_NOT_FOUND")
		if result, err := g.gates[0].Settle(g.ctx, g.actors[1], BudgetSettlement{ID: execution.ID, ActualTokens: 8, ReceiptSHA256: strings.Repeat("c", 64)}); result != (BudgetReservation{}) || err == nil || err.Error() != "VALIDATION_FAILED" {
			t.Fatalf("usage above the reserved maximum: %+v, %v", result, err)
		}
		settlement := BudgetSettlement{ID: execution.ID, ActualTokens: 2, ReceiptSHA256: strings.Repeat("c", 64)}
		for _, result := range concurrentBudgetCalls(12, func(i int) (BudgetReservation, error) {
			return g.gates[i%2].Settle(g.ctx, g.actors[1], settlement)
		}) {
			if result.err != nil || result.reservation != (BudgetReservation{execution.ID, BudgetSettled}) {
				t.Fatalf("settlement race: %+v, %v", result.reservation, result.err)
			}
		}
		g.counters("", BudgetCeiling{1, 2}, 1)
		g.counters(g.f.User, BudgetCeiling{1, 2}, 1)
		for _, changed := range []BudgetSettlement{
			{ID: execution.ID, ActualTokens: 3, ReceiptSHA256: settlement.ReceiptSHA256},
			{ID: execution.ID, ActualTokens: 2, ReceiptSHA256: strings.Repeat("d", 64)},
		} {
			if result, err := g.gates[0].Settle(g.ctx, g.actors[1], changed); err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" || result != (BudgetReservation{}) {
				t.Fatalf("changed settlement: %+v, %v", result, err)
			}
		}
		if result, err := g.gates[1].Reserve(g.ctx, g.actors[1], execution); err != nil || result.State != BudgetSettled {
			t.Fatalf("completed reservation replay: %+v, %v", result, err)
		}
		g.counters("", BudgetCeiling{1, 2}, 1)
	})

	t.Run("policy unavailable disabled and recovered", func(t *testing.T) {
		g.reset(t)
		gate := *g.gates[0]
		for _, source := range []ExecutionBudgetPolicySource{
			nil,
			func(context.Context, transaction.Executor, BudgetSubject) (ExecutionBudgetPolicy, error) {
				return ExecutionBudgetPolicy{}, errors.New("private policy diagnostic")
			},
			func(context.Context, transaction.Executor, BudgetSubject) (ExecutionBudgetPolicy, error) {
				return ExecutionBudgetPolicy{}, nil
			},
		} {
			gate.policy = source
			g.refusal(t, &gate, g.actors[1], budgetTestExecution(1), "SERVICE_NOT_READY")
		}
		gate.policy = g.gates[0].policy
		commands := *gate.commands
		commands.executionPolicy = func(context.Context) (bool, error) { return false, nil }
		gate.commands = &commands
		g.refusal(t, &gate, g.actors[1], budgetTestExecution(1), "ACCESS_DENIED")
		commands.executionPolicy = func(context.Context) (bool, error) { return true, errors.New("private installation diagnostic") }
		g.refusal(t, &gate, g.actors[1], budgetTestExecution(1), "SERVICE_NOT_READY")
		gate.commands = g.gates[0].commands
		p := *g.policy.Load()
		p.UserAccess = BudgetAccessDisabled
		g.policy.Store(&p)
		g.refusal(t, &gate, g.actors[1], budgetTestExecution(1), "ACCESS_DENIED")
		p.UserAccess = BudgetAccessEnabled
		p.WindowEnd = p.WindowStart
		g.policy.Store(&p)
		g.refusal(t, &gate, g.actors[1], budgetTestExecution(1), "SERVICE_NOT_READY")
		p.WindowEnd = p.WindowStart.Add(-time.Hour)
		p.WindowStart = p.WindowEnd.Add(-time.Hour)
		g.policy.Store(&p)
		g.refusal(t, &gate, g.actors[1], budgetTestExecution(1), "SERVICE_NOT_READY")
		p.WindowStart = time.Now().UTC().Truncate(time.Hour).Add(24 * time.Hour)
		p.WindowEnd = p.WindowStart.Add(time.Hour)
		g.policy.Store(&p)
		g.refusal(t, &gate, g.actors[1], budgetTestExecution(1), "SERVICE_NOT_READY")
		var count int
		if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT (SELECT count(*) FROM product_agent_budget_reservations)+(SELECT count(*) FROM product_agent_budget_installations)+(SELECT count(*) FROM product_agent_budget_users)`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("refused policy changed durable state: %d, %v", count, err)
		}
		g.reset(t)
		if result, err := gate.Reserve(g.ctx, g.actors[1], budgetTestExecution(0)); err != nil || result.State != BudgetNew {
			t.Fatalf("policy recovery: %+v, %v", result, err)
		}
		g.counters("", BudgetCeiling{1, 0}, 1)
	})

	t.Run("user capacity spans authorized workspaces", func(t *testing.T) {
		g.reset(t)
		p := *g.policy.Load()
		p.User = BudgetCeiling{1, 10}
		g.policy.Store(&p)
		workspace := uuid.NewString()
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Budget second workspace','active')`, workspace, g.f.Tenant)
		g.exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`, g.f.User, g.f.Tenant, workspace, uuid.NewString())
		if result, err := g.gates[0].Reserve(g.ctx, g.actors[1], budgetTestExecution(1)); err != nil || result.State != BudgetNew {
			t.Fatalf("first workspace admission: %+v, %v", result, err)
		}
		other := g.actors[1]
		other.Me.Workspace = &identity.Workspace{ID: workspace, TenantID: g.f.Tenant}
		g.refusal(t, g.gates[1], other, budgetTestExecution(1), "RATE_LIMITED")
		g.counters(g.f.User, BudgetCeiling{1, 1}, 1)
	})

	t.Run("rollback and unknown commit preserve authority", func(t *testing.T) {
		g.reset(t)
		execution := budgetTestExecution(7)
		rollback := g.gate(&budgetOutcomeStore{Store: g.f.Store, rollback: true})
		g.refusal(t, rollback, g.actors[1], execution, "STORAGE_UNAVAILABLE")
		unknown := g.gate(&budgetOutcomeStore{Store: g.f.Store})
		g.refusal(t, unknown, g.actors[1], execution, "STORAGE_COMMIT_OUTCOME_UNKNOWN")
		g.counters("", BudgetCeiling{1, 7}, 1)
		// A new service and independent pool recover the hold, not execution authority.
		if result, err := g.gates[1].Reserve(g.ctx, g.actors[1], execution); err != nil || result.State != BudgetHeld {
			t.Fatalf("reopened unknown admission: %+v, %v", result, err)
		}
		settlement := BudgetSettlement{ID: execution.ID, ActualTokens: 2, ReceiptSHA256: strings.Repeat("e", 64)}
		if result, err := unknown.Settle(g.ctx, g.actors[1], settlement); result != (BudgetReservation{}) || err == nil || err.Error() != "STORAGE_COMMIT_OUTCOME_UNKNOWN" {
			t.Fatalf("unknown settlement: %+v, %v", result, err)
		}
		if result, err := g.gates[1].Settle(g.ctx, g.actors[1], settlement); err != nil || result.State != BudgetSettled {
			t.Fatalf("reopened settlement: %+v, %v", result, err)
		}
		g.counters("", BudgetCeiling{1, 2}, 1)
	})

	t.Run("finite integer limits", func(t *testing.T) {
		g.reset(t)
		p := *g.policy.Load()
		p.User = BudgetCeiling{}
		g.policy.Store(&p)
		g.refusal(t, g.gates[0], g.actors[1], budgetTestExecution(0), "RATE_LIMITED")
		p.Installation = BudgetCeiling{math.MaxInt64, math.MaxInt64}
		p.User = p.Installation
		g.policy.Store(&p)
		execution := budgetTestExecution(math.MaxInt64)
		if result, err := g.gates[0].Reserve(g.ctx, g.actors[1], execution); err != nil || result.State != BudgetNew {
			t.Fatalf("maximum finite token reservation: %+v, %v", result, err)
		}
		g.refusal(t, g.gates[1], g.actors[1], budgetTestExecution(1), "RATE_LIMITED")
		g.counters("", BudgetCeiling{1, math.MaxInt64}, 1)
		if result, err := g.gates[0].Settle(g.ctx, g.actors[1], BudgetSettlement{ID: execution.ID, ReceiptSHA256: strings.Repeat("e", 64)}); err != nil || result.State != BudgetSettled {
			t.Fatalf("maximum unused token refund: %+v, %v", result, err)
		}
		g.exec(`UPDATE product_agent_budget_installations SET executions=$1,tokens=0`, int64(math.MaxInt64-1))
		g.exec(`UPDATE product_agent_budget_users SET executions=$1,tokens=0`, int64(math.MaxInt64-1))
		if result, err := g.gates[1].Reserve(g.ctx, g.actors[1], budgetTestExecution(0)); err != nil || result.State != BudgetNew {
			t.Fatalf("maximum finite execution charge: %+v, %v", result, err)
		}
		g.refusal(t, g.gates[0], g.actors[1], budgetTestExecution(0), "RATE_LIMITED")
		g.counters("", BudgetCeiling{math.MaxInt64, 0}, 2)
	})

	t.Run("settlement retains the original policy window", func(t *testing.T) {
		g.reset(t)
		old := *g.policy.Load()
		execution := budgetTestExecution(7)
		if result, err := g.gates[0].Reserve(g.ctx, g.actors[1], execution); err != nil || result.State != BudgetNew {
			t.Fatalf("original admission: %+v, %v", result, err)
		}
		next := old
		next.WindowStart = old.WindowStart.Add(-time.Hour)
		next.WindowEnd = old.WindowEnd.Add(time.Hour)
		g.policy.Store(&next)
		if result, err := g.gates[1].Reserve(g.ctx, g.actors[1], execution); err != nil || result.State != BudgetHeld {
			t.Fatalf("window change replay: %+v, %v", result, err)
		}
		if result, err := g.gates[1].Reserve(g.ctx, g.actors[1], budgetTestExecution(5)); err != nil || result.State != BudgetNew {
			t.Fatalf("next window admission: %+v, %v", result, err)
		}
		settlement := BudgetSettlement{ID: execution.ID, ActualTokens: 2, ReceiptSHA256: strings.Repeat("f", 64)}
		// Settlement is permitted even when new execution is explicitly disabled.
		next.UserAccess = BudgetAccessDisabled
		g.policy.Store(&next)
		if result, err := g.gates[0].Settle(g.ctx, g.actors[1], settlement); err != nil || result.State != BudgetSettled {
			t.Fatalf("original window settlement: %+v, %v", result, err)
		}
		g.counters("", BudgetCeiling{1, 5}, 2)
		g.policy.Store(&old)
		g.counters("", BudgetCeiling{1, 2}, 2)
		g.counters(g.f.User, BudgetCeiling{1, 2}, 2)
	})

	t.Run("cancellation while waiting for shared capacity", func(t *testing.T) {
		g.reset(t)
		if _, err := g.gates[0].Reserve(g.ctx, g.actors[1], budgetTestExecution(1)); err != nil {
			t.Fatal(err)
		}
		blocker, err := g.f.PostgreSQL.Begin(g.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer blocker.Rollback(g.ctx)
		var blockerID uint32
		if err := blocker.QueryRow(g.ctx, `SELECT pg_backend_pid()`).Scan(&blockerID); err != nil {
			t.Fatal(err)
		}
		if _, err := blocker.Exec(g.ctx, `SELECT executions FROM product_agent_budget_installations FOR UPDATE`); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(g.ctx)
		defer cancel()
		finished := make(chan budgetTestOutcome, 1)
		go func() {
			r, err := g.gates[1].Reserve(ctx, g.actors[2], budgetTestExecution(3))
			finished <- budgetTestOutcome{r, err}
		}()
		poll, stop := context.WithTimeout(g.ctx, 2*time.Second)
		defer stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		blocked := false
		for !blocked && poll.Err() == nil {
			if err := g.f.PostgreSQL.QueryRow(poll, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND $2=ANY(pg_blocking_pids(pid)))`,
				g.replicaPool.Config().ConnConfig.RuntimeParams["application_name"], blockerID).Scan(&blocked); err != nil {
				break
			}
			if !blocked {
				select {
				case <-ticker.C:
				case <-poll.Done():
				}
			}
		}
		cancel()
		result := <-finished
		if !blocked || result.reservation != (BudgetReservation{}) || !errors.Is(result.err, context.Canceled) {
			t.Fatalf("locked cancellation: blocked=%v, result=%+v, error=%v", blocked, result.reservation, result.err)
		}
		if err := blocker.Rollback(g.ctx); err != nil {
			t.Fatal(err)
		}
		g.counters("", BudgetCeiling{1, 1}, 1)
	})

	t.Run("missing candidate schema fails closed then recovers", func(t *testing.T) {
		g.reset(t)
		execution := budgetTestExecution(3)
		g.exec(`ALTER TABLE product_agent_budget_reservations RENAME TO product_agent_budget_reservations_unavailable`)
		g.refusal(t, g.gates[0], g.actors[1], execution, "STORAGE_UNAVAILABLE")
		g.exec(`ALTER TABLE product_agent_budget_reservations_unavailable RENAME TO product_agent_budget_reservations`)
		if result, err := g.gates[1].Reserve(g.ctx, g.actors[1], execution); err != nil || result.State != BudgetNew {
			t.Fatalf("schema recovery: %+v, %v", result, err)
		}
		g.counters("", BudgetCeiling{1, 3}, 1)
	})

	t.Run("authority denial precedes policy and reservation", func(t *testing.T) {
		g.reset(t)
		var calls atomic.Int64
		gate := *g.gates[0]
		gate.policy = func(context.Context, transaction.Executor, BudgetSubject) (ExecutionBudgetPolicy, error) {
			calls.Add(1)
			return *g.policy.Load(), nil
		}
		foreignTenant, foreignWorkspace := uuid.NewString(), uuid.NewString()
		g.exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Budget foreign tenant','active')`, foreignTenant)
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Budget foreign workspace','active')`, foreignWorkspace, foreignTenant)
		foreign := g.actors[1]
		foreign.Me.Workspace = &identity.Workspace{ID: foreignWorkspace, TenantID: foreignTenant}
		g.refusal(t, &gate, foreign, budgetTestExecution(1), "RESOURCE_NOT_FOUND")
		g.exec(`DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, g.actors[2].Me.User.ID, g.f.Workspace)
		g.refusal(t, &gate, g.actors[2], budgetTestExecution(1), "RESOURCE_NOT_FOUND")
		g.exec(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1`, g.actors[1].SessionID)
		g.refusal(t, &gate, g.actors[1], budgetTestExecution(1), "AUTH_SESSION_EXPIRED")
		if calls.Load() != 0 {
			t.Fatal("unavailable authority reached the trusted budget policy")
		}
		var count int
		if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*) FROM product_agent_budget_reservations`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("denied authority left reservations: %d, %v", count, err)
		}
	})
}

func (g *budgetRepositoryFixture) refusal(t *testing.T, gate *ExecutionBudgets, actor identity.Auth, execution BudgetExecution, code string) {
	t.Helper()
	result, err := gate.Reserve(g.ctx, actor, execution)
	if result != (BudgetReservation{}) || err == nil || err.Error() != code {
		t.Fatalf("refusal: %+v, %v; want %s", result, err, code)
	}
}

// The callback executes on PostgreSQL. The wrapper injects a lost reply only
// after a real commit, or a failure inside the callback to force a real rollback.
type budgetOutcomeStore struct {
	transaction.Store
	rollback bool
}

func (s *budgetOutcomeStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	err := s.Store.Within(ctx, readOnly, func(ctx context.Context, tx transaction.Executor) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		if s.rollback {
			return &transaction.Error{Kind: transaction.Unavailable}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return &transaction.Error{Kind: transaction.CommitUnknown}
}
