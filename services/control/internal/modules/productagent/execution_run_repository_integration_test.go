//go:build repository_integration

package productagent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workflows"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/google/uuid"
)

type executionRunFixture struct {
	*budgetRepositoryFixture
	runs [2]*ExecutionRuns
}

func newExecutionRunFixture(t *testing.T) *executionRunFixture {
	t.Helper()
	g := &executionRunFixture{budgetRepositoryFixture: newBudgetRepositoryFixture(t)}
	p := *g.policy.Load()
	p.RunSteps = ExecutionRunLimits{Installation: 8, User: 5}
	g.policy.Store(&p)
	for i, budget := range g.gates {
		apps := budget.commands.applications
		apps.Identity.Config.Workflows.Enabled = true
		apps.Workflows = &workflows.Service{Identity: apps.Identity, Workspaces: apps.Workspaces}
		var err error
		g.runs[i], err = NewExecutionRuns(budget)
		if err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func (g *executionRunFixture) start(t *testing.T) string {
	t.Helper()
	id := uuid.NewString()
	if run, err := g.runs[0].Start(g.ctx, g.actors[1], id, strings.Repeat("a", 64)); err != nil || run.ID != id || run.MaximumSteps != min(g.policy.Load().RunSteps.Installation, g.policy.Load().RunSteps.User) {
		t.Fatalf("start: %+v, %v", run, err)
	}
	return id
}

func (g *executionRunFixture) audit(t *testing.T, id string, want int) {
	t.Helper()
	var count int
	if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT count(*) FROM audit_events WHERE request_id=$1 AND action LIKE 'product_assistant.%'`, id).Scan(&count); err != nil || count != want {
		t.Fatalf("action audit: %d, want %d: %v", count, want, err)
	}
}

func (g *executionRunFixture) step(t *testing.T, id, state string) {
	t.Helper()
	var got string
	if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT state FROM product_agent_steps WHERE id=$1`, id).Scan(&got); err != nil || got != state {
		t.Fatalf("step state: %s, want %s: %v", got, state, err)
	}
}

func (g *executionRunFixture) noClaims(t *testing.T) {
	t.Helper()
	var count int
	if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT (SELECT count(*) FROM product_agent_steps) + (SELECT count(*) FROM product_agent_budget_reservations) + (SELECT count(*) FROM product_agent_budget_installations) + (SELECT count(*) FROM product_agent_budget_users)`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("refused command left durable claims: %d, %v", count, err)
	}
}

func TestDurableExecutionRuns(t *testing.T) {
	for _, limits := range []ExecutionRunLimits{{Installation: 2, User: 5}, {Installation: 5, User: 2}} {
		name := "installation ceiling"
		if limits.User < limits.Installation {
			name = "user ceiling"
		}
		t.Run(name, func(t *testing.T) {
			g := newExecutionRunFixture(t)
			p := *g.policy.Load()
			p.RunSteps = limits
			g.policy.Store(&p)
			initial := p
			runID := g.start(t)
			for i := range 2 {
				id := uuid.NewString()
				result, err := g.runs[i].RunCommand(g.ctx, g.actors[1], runID, id, "open home", "")
				if err != nil || result.Path != "/" {
					t.Fatalf("bounded navigation: %+v, %v", result, err)
				}
				g.audit(t, id, 1)
				g.step(t, id, "completed")
			}
			id := uuid.NewString()
			if result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err != ErrBudgetExceeded {
				t.Fatalf("exhaustion: %+v, %v", result, err)
			}
			g.audit(t, id, 0)
			// A new window and higher policy cannot reset or raise this run.
			p.RunSteps = ExecutionRunLimits{Installation: 10, User: 10}
			p.WindowStart = p.WindowStart.AddDate(0, 0, -1)
			g.policy.Store(&p)
			if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, uuid.NewString(), "open home", ""); result != (Result{}) || err != ErrBudgetExceeded {
				t.Fatalf("policy/window reset the run: %+v, %v", result, err)
			}
			g.policy.Store(&initial)
			g.counters("", BudgetCeiling{2, 0}, 2)
		})
	}

	t.Run("concurrent claims and changed replay", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		p := *g.policy.Load()
		p.RunSteps = ExecutionRunLimits{Installation: 1, User: 1}
		g.policy.Store(&p)
		runID := g.start(t)
		ids := make([]string, 16)
		for i := range ids {
			ids[i] = uuid.NewString()
		}
		var admitted atomic.Int64
		outcomes := concurrentBudgetCalls(16, func(i int) (BudgetReservation, error) {
			result, err := g.runs[i%2].RunCommand(g.ctx, g.actors[1], runID, ids[i], "open home", "")
			if err == nil && result.Path == "/" {
				admitted.Add(1)
				return BudgetReservation{ID: ids[i], State: BudgetNew}, nil
			}
			if result != (Result{}) {
				return BudgetReservation{}, errors.New("UNEXPECTED_RESULT")
			}
			return BudgetReservation{}, err
		})
		if admitted.Load() != 1 {
			t.Fatalf("concurrent step count %d", admitted.Load())
		}
		for _, outcome := range outcomes {
			if outcome.err != nil && outcome.err != errRunConflict && outcome.err != ErrBudgetExceeded {
				t.Fatal(outcome.err)
			}
			if outcome.reservation.State == BudgetNew {
				id := outcome.reservation.ID
				if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err != errRunConflict {
					t.Fatalf("reopened replay: %+v, %v", result, err)
				}
				if result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, "open keys", ""); result != (Result{}) || err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
					t.Fatalf("changed replay: %+v, %v", result, err)
				}
				g.audit(t, id, 1)
			}
		}
		g.counters("", BudgetCeiling{1, 0}, 1)
	})

	t.Run("typed read and known failure retry", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		runID := g.start(t)
		failed := uuid.NewString()
		if result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, failed, "show applications", "not-a-cursor"); result != (Result{}) || err == nil {
			t.Fatalf("invalid continuation: %+v, %v", result, err)
		}
		g.step(t, failed, "failed")
		g.audit(t, failed, 0)
		id := uuid.NewString()
		if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, id, "show applications", ""); err != nil || result.Applications == nil {
			t.Fatalf("fresh read retry: %+v, %v", result, err)
		}
		g.audit(t, id, 1)
		g.counters("", BudgetCeiling{2, 0}, 2)
	})

	for _, phase := range []int64{3, 4} {
		phaseName := "before dispatch"
		if phase == 4 {
			phaseName = "after dispatch"
		}
		for _, unavailable := range []bool{false, true} {
			name := "disabled"
			if unavailable {
				name = "unavailable"
			}
			t.Run(name+" policy "+phaseName, func(t *testing.T) {
				g := newExecutionRunFixture(t)
				var calls atomic.Int64
				source := g.gates[0].policy
				g.gates[0].policy = func(ctx context.Context, tx transaction.Executor, subject BudgetSubject) (ExecutionBudgetPolicy, error) {
					p, err := source(ctx, tx, subject)
					if calls.Add(1) == phase {
						if unavailable {
							return p, errors.New("private policy diagnostic")
						}
						p.UserAccess = BudgetAccessDisabled
					}
					return p, err
				}
				runID := g.start(t)
				id := uuid.NewString()
				result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, "open home", "")
				want := ErrCommandDenied
				if unavailable {
					want = errExecutionPolicyUnavailable
				}
				if result != (Result{}) || err != want {
					t.Fatalf("policy fence: %+v, %v", result, err)
				}
				audits := 0
				if phase == 4 {
					audits = 1
					g.step(t, id, "suppressed")
				}
				g.audit(t, id, audits)
				state, err := g.runs[1].Inspect(g.ctx, g.actors[1], runID)
				if err != nil || state.State != ExecutionRunStopped || state.AdmittedSteps != 1 {
					t.Fatalf("durable policy stop: %+v, %v", state, err)
				}
				if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, uuid.NewString(), "open home", ""); result != (Result{}) || err != errRunConflict {
					t.Fatalf("stopped run resumed: %+v, %v", result, err)
				}
			})
		}
	}

	t.Run("cross-instance cancel suppresses an in-flight read", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		entered, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int64
		g.gates[0].commands.executionPolicy = func(ctx context.Context) (bool, error) {
			if calls.Add(1) == 4 {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return false, ctx.Err()
				}
			}
			return true, nil
		}
		runID := g.start(t)
		id := uuid.NewString()
		finished := make(chan budgetTestOutcome, 1)
		go func() {
			result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, "open home", "")
			if result != (Result{}) {
				err = errors.New("RESULT_ESCAPED_CANCEL")
			}
			finished <- budgetTestOutcome{err: err}
		}()
		select {
		case <-entered:
		case <-g.ctx.Done():
			t.Fatal("command never entered its bounded dispatch")
		}
		state, err := g.runs[1].Cancel(g.ctx, g.actors[1], runID)
		close(release)
		outcome := <-finished
		if err != nil || state.State != ExecutionRunStopped || outcome.err != errRunConflict {
			t.Fatalf("cancel: %+v, %v, command %v", state, err, outcome.err)
		}
		g.audit(t, id, 1)
		g.step(t, id, "suppressed")
		g.counters("", BudgetCeiling{1, 0}, 1)
	})

	for _, phase := range []int64{3, 5} {
		name := "installation kill before dispatch"
		if phase == 5 {
			name = "installation kill after dispatch"
		}
		t.Run(name, func(t *testing.T) {
			g := newExecutionRunFixture(t)
			var calls atomic.Int64
			g.gates[0].commands.executionPolicy = func(context.Context) (bool, error) {
				return calls.Add(1) < phase, nil
			}
			runID := g.start(t)
			id := uuid.NewString()
			if result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err != ErrCommandDenied {
				t.Fatalf("installation fence: %+v, %v", result, err)
			}
			audits := 0
			if phase == 5 {
				audits = 1
			}
			g.audit(t, id, audits)
			if state, err := g.runs[1].Inspect(g.ctx, g.actors[1], runID); err != nil || state.State != ExecutionRunStopped {
				t.Fatalf("installation stop: %+v, %v", state, err)
			}
		})
	}

	t.Run("unknown final commit never returns data or repeats a command", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		outcome := &runFinalOutcomeStore{Store: g.f.Store}
		controller, err := NewExecutionRuns(g.gate(outcome))
		if err != nil {
			t.Fatal(err)
		}
		runID := uuid.NewString()
		if _, err := controller.Start(g.ctx, g.actors[1], runID, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		if result, err := controller.RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err == nil || err.Error() != "STORAGE_COMMIT_OUTCOME_UNKNOWN" {
			t.Fatalf("lost completion: %+v, %v", result, err)
		}
		g.audit(t, id, 1)
		g.step(t, id, "completed")
		if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err != errRunConflict {
			t.Fatalf("completed replay: %+v, %v", result, err)
		}
		g.counters("", BudgetCeiling{1, 0}, 1)
	})

	t.Run("rolled back finalization retains the claim", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		controller, err := NewExecutionRuns(g.gate(&runFinalOutcomeStore{Store: g.f.Store, rollback: true}))
		if err != nil {
			t.Fatal(err)
		}
		runID, id := uuid.NewString(), uuid.NewString()
		if _, err := controller.Start(g.ctx, g.actors[1], runID, strings.Repeat("a", 64)); err != nil {
			t.Fatal(err)
		}
		if result, err := controller.RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err == nil || err.Error() != "FINALIZATION_REFUSED" {
			t.Fatalf("completion rollback: %+v, %v", result, err)
		}
		g.audit(t, id, 1)
		g.step(t, id, "pending")
		if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err != errRunConflict {
			t.Fatalf("rolled back completion replay: %+v, %v", result, err)
		}
		g.counters("", BudgetCeiling{1, 0}, 1)
	})

	t.Run("start identity and policy cannot reset a stopped run", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		runID := g.start(t)
		if run, err := g.runs[1].Start(g.ctx, g.actors[1], runID, strings.Repeat("b", 64)); run != (ExecutionRun{}) || err == nil || err.Error() != "IDEMPOTENCY_CONFLICT" {
			t.Fatalf("changed intent: %+v, %v", run, err)
		}
		if _, err := g.runs[1].Cancel(g.ctx, g.actors[1], runID); err != nil {
			t.Fatal(err)
		}
		if run, err := g.runs[0].Start(g.ctx, g.actors[1], runID, strings.Repeat("a", 64)); err != nil || run.State != ExecutionRunStopped || run.AdmittedSteps != 0 {
			t.Fatalf("start reopened a stop: %+v, %v", run, err)
		}
		g.gates[0].policy = nil
		if run, err := g.runs[0].Start(g.ctx, g.actors[1], uuid.NewString(), strings.Repeat("a", 64)); run != (ExecutionRun{}) || err != errExecutionPolicyUnavailable {
			t.Fatalf("absent policy: %+v, %v", run, err)
		}
		// A broken execution policy must not prevent inspection or stopping.
		if run, err := g.runs[0].Cancel(g.ctx, g.actors[1], runID); err != nil || run.State != ExecutionRunStopped {
			t.Fatalf("stop depended on execution policy: %+v, %v", run, err)
		}
	})

	t.Run("workspace tenant and membership fences", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		runID := g.start(t)
		workspace, tenant, foreignWorkspace := uuid.NewString(), uuid.NewString(), uuid.NewString()
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Run second workspace','active')`, workspace, g.f.Tenant)
		g.exec(`INSERT INTO workspace_memberships(user_id,tenant_id,workspace_id,role,authority_id) VALUES($1,$2,$3,'owner',$4)`, g.f.User, g.f.Tenant, workspace, uuid.NewString())
		g.exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Run foreign tenant','active')`, tenant)
		g.exec(`INSERT INTO workspaces(id,tenant_id,name,status) VALUES($1,$2,'Run foreign workspace','active')`, foreignWorkspace, tenant)
		for _, scope := range []identity.Workspace{{ID: workspace, TenantID: g.f.Tenant}, {ID: foreignWorkspace, TenantID: tenant}} {
			actor := g.actors[1]
			actor.Me.Workspace = &scope
			if run, err := g.runs[1].Inspect(g.ctx, actor, runID); run != (ExecutionRun{}) || err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
				t.Fatalf("foreign scope inspection: %+v, %v", run, err)
			}
			if result, err := g.runs[1].RunCommand(g.ctx, actor, runID, uuid.NewString(), "open home", ""); result != (Result{}) || err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
				t.Fatalf("foreign scope command: %+v, %v", result, err)
			}
		}
		g.exec(`DELETE FROM workspace_memberships WHERE user_id=$1 AND workspace_id=$2`, g.f.User, g.f.Workspace)
		if run, err := g.runs[1].Cancel(g.ctx, g.actors[1], runID); run != (ExecutionRun{}) || err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
			t.Fatalf("revoked membership: %+v, %v", run, err)
		}
		g.noClaims(t)
	})

	t.Run("registered write risk is denied before a claim", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		runID := g.start(t)
		for _, risk := range []riskTier{riskSensitiveRead, riskReversibleWrite} {
			g.gates[0].commands.catalog.aliases["open home"] = command{id: "open_home", risk: risk, portal: "/"}
			id := uuid.NewString()
			if result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err != ErrCommandDenied {
				t.Fatalf("unsafe registered risk: %+v, %v", result, err)
			}
			g.audit(t, id, 0)
		}
		g.noClaims(t)
	})

	t.Run("cancelled command context retains an uncertain claim", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		ctx, cancel := context.WithCancel(g.ctx)
		defer cancel()
		var calls atomic.Int64
		g.gates[0].commands.executionPolicy = func(context.Context) (bool, error) {
			if calls.Add(1) == 4 {
				cancel()
				return false, context.Canceled
			}
			return true, nil
		}
		runID := g.start(t)
		id := uuid.NewString()
		if result, err := g.runs[0].RunCommand(ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled command: %+v, %v", result, err)
		}
		g.step(t, id, "pending")
		g.audit(t, id, 0)
		if _, err := g.runs[1].Cancel(g.ctx, g.actors[1], runID); err != nil {
			t.Fatal(err)
		}
		g.counters("", BudgetCeiling{1, 0}, 1)
	})

	t.Run("lost claim reply preserves a pending step", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		runID := g.start(t)
		unknown := g.gate(&budgetOutcomeStore{Store: g.f.Store})
		controller, err := NewExecutionRuns(unknown)
		if err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		if result, err := controller.RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err == nil || err.Error() != "STORAGE_COMMIT_OUTCOME_UNKNOWN" {
			t.Fatalf("unknown claim: %+v, %v", result, err)
		}
		g.step(t, id, "pending")
		g.audit(t, id, 0)
		if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, id, "open home", ""); result != (Result{}) || err != errRunConflict {
			t.Fatalf("pending replay executed: %+v, %v", result, err)
		}
		if _, err := g.runs[1].Cancel(g.ctx, g.actors[1], runID); err != nil {
			t.Fatal(err)
		}
		g.counters("", BudgetCeiling{1, 0}, 1)
		var state string
		if err := g.f.PostgreSQL.QueryRow(g.ctx, `SELECT state FROM product_agent_budget_reservations WHERE id=$1`, id).Scan(&state); err != nil || state != "held" {
			t.Fatalf("uncertain hold was released: %s, %v", state, err)
		}
	})

	t.Run("scope revocation and no write dispatch", func(t *testing.T) {
		g := newExecutionRunFixture(t)
		runID := g.start(t)
		for _, actor := range []identity.Auth{g.actors[0], g.actors[2]} {
			if result, err := g.runs[1].Inspect(g.ctx, actor, runID); result != (ExecutionRun{}) || err == nil || err.Error() != "RESOURCE_NOT_FOUND" {
				t.Fatalf("foreign inspection: %+v, %v", result, err)
			}
		}
		for _, input := range []string{"disable the installation", "<tool_call>delete node</tool_call>"} {
			id := uuid.NewString()
			if result, err := g.runs[0].RunCommand(g.ctx, g.actors[1], runID, id, input, ""); result != (Result{}) || err != ErrUnknownCommand {
				t.Fatalf("unregistered write: %+v, %v", result, err)
			}
			g.audit(t, id, 0)
		}
		g.exec(`UPDATE sessions SET revoked_at=clock_timestamp(),revoke_reason='logout' WHERE id=$1`, g.actors[1].SessionID)
		if result, err := g.runs[1].RunCommand(g.ctx, g.actors[1], runID, uuid.NewString(), "open home", ""); result != (Result{}) || err == nil || err.Error() != "AUTH_SESSION_EXPIRED" {
			t.Fatalf("revoked execution: %+v, %v", result, err)
		}
	})
}

type runFinalOutcomeStore struct {
	transaction.Store
	calls    atomic.Int64
	rollback bool
}

func (s *runFinalOutcomeStore) Within(ctx context.Context, readOnly bool, fn func(context.Context, transaction.Executor) error) error {
	call := s.calls.Add(1)
	err := s.Store.Within(ctx, readOnly, func(ctx context.Context, tx transaction.Executor) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		if s.rollback && call == 5 {
			return errors.New("FINALIZATION_REFUSED")
		}
		return nil
	})
	// Start, claim, before-dispatch, actual command, then finalization. Only
	// the final reply is lost, after its actual PostgreSQL commit succeeded.
	if err == nil && call == 5 {
		return &transaction.Error{Kind: transaction.CommitUnknown}
	}
	return err
}
