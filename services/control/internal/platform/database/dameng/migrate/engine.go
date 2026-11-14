package migrate

import "context"

type Mode string

const (
	Apply     Mode = "apply"
	Readiness Mode = "readiness"
	Reconcile Mode = "reconcile"
	Retry     Mode = "retry"
)

type Report struct {
	Scope           string `json:"scope"`
	TotalSteps      int    `json:"total_steps"`
	AppliedSteps    int    `json:"applied_steps"`
	DirtyStep       int    `json:"dirty_step"`
	FoundationReady bool   `json:"foundation_ready"`
	RuntimeReady    bool   `json:"runtime_ready"`
	PlatformReady   bool   `json:"platform_ready"`
	LockReleased    bool   `json:"lock_released"`
}
type journalEntry struct {
	Step                         int
	Scope, Source, Object, State string
	Started, Completed           bool
}
type snapshot struct {
	Journal bool
	Entries []journalEntry
	Present map[string]bool
}

// The sole production implementation uses the retained native connection. Test
// fixtures may implement this private port to exercise interrupted transitions.
type backend interface {
	inspect(context.Context) (snapshot, error)
	createJournal(context.Context) error
	start(context.Context, step) error
	execute(context.Context, step) error
	complete(context.Context, step) error
}

// journalProgress validates durable ordering independently from object readback.
// It grants no readiness and never fabricates Present flags for applied steps.
func journalProgress(c catalog, s snapshot) (Report, map[string]bool, error) {
	r := Report{Scope: Scope, TotalSteps: len(c.steps)}
	allowed := map[string]bool{}
	if len(s.Entries) > len(c.steps) || !s.Journal && len(s.Entries) > 0 {
		return r, allowed, fail("DAMENG_SCHEMA_JOURNAL_INVALID")
	}
	for i, e := range s.Entries {
		item := c.steps[i]
		if e.Step != i+1 || e.Scope != Scope || e.Source != item.SHA256 || e.Object != item.Object {
			return r, allowed, fail("DAMENG_SCHEMA_CHECKSUM_MISMATCH")
		}
		if !e.Started || e.State != "applied" && e.State != "applying" || (e.State == "applied") != e.Completed || r.DirtyStep != 0 {
			return r, allowed, fail("DAMENG_SCHEMA_JOURNAL_INVALID")
		}
		allowed[item.Object] = true
		if e.State == "applying" {
			r.DirtyStep = e.Step
		} else {
			r.AppliedSteps++
		}
	}
	return r, allowed, nil
}
func assess(c catalog, s snapshot) (Report, error) {
	r, allowed, e := journalProgress(c, s)
	if e != nil {
		return r, e
	}
	for _, item := range c.steps[:r.AppliedSteps] {
		if !s.Present[item.Object] {
			return r, fail("DAMENG_SCHEMA_OBJECT_MISSING")
		}
	}
	for name, present := range s.Present {
		if present && !allowed[name] {
			return r, fail("DAMENG_SCHEMA_UNJOURNALED_OBJECT")
		}
	}
	r.FoundationReady = s.Journal && r.AppliedSteps == len(c.steps) && r.DirtyStep == 0
	return r, nil
}

func run(ctx context.Context, c catalog, b backend, mode Mode) (Report, error) {
	r := Report{Scope: Scope, TotalSteps: len(c.steps)}
	if ctx == nil {
		return r, fail("DAMENG_SCHEMA_CONTEXT_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return r, fail("DAMENG_SCHEMA_DEADLINE_REQUIRED")
	}
	if e := ctx.Err(); e != nil {
		return r, e
	}
	if mode != Apply && mode != Readiness && mode != Reconcile && mode != Retry {
		return r, fail("DAMENG_SCHEMA_MODE_INVALID")
	}
	s, e := b.inspect(ctx)
	if e != nil {
		return r, e
	}
	r, e = assess(c, s)
	if e != nil {
		return r, e
	}
	if mode == Readiness {
		if !r.FoundationReady {
			return r, fail("DAMENG_SCHEMA_NOT_READY")
		}
		return r, nil
	}
	if !s.Journal {
		if mode != Apply {
			return r, fail("DAMENG_SCHEMA_NOT_INITIALIZED")
		}
		if e = ctx.Err(); e != nil {
			return r, e
		}
		if e = b.createJournal(ctx); e != nil {
			return r, e
		}
		s, e = b.inspect(ctx)
		if e != nil {
			return r, e
		}
		r, e = assess(c, s)
		if e != nil {
			return r, e
		}
		if !s.Journal {
			return r, fail("DAMENG_SCHEMA_JOURNAL_INVALID")
		}
	}
	if r.DirtyStep != 0 {
		if mode == Apply {
			return r, fail("DAMENG_SCHEMA_RECONCILIATION_REQUIRED")
		}
		item := c.steps[r.DirtyStep-1]
		if !s.Present[item.Object] {
			if mode != Retry {
				return r, fail("DAMENG_SCHEMA_EXPLICIT_RETRY_REQUIRED")
			}
			if e = ctx.Err(); e != nil {
				return r, e
			}
			if e = b.execute(ctx, item); e != nil {
				return r, e
			}
		}
		// A dirty operation is acknowledged only after another complete readback.
		s, e = b.inspect(ctx)
		if e != nil {
			return r, e
		}
		if _, e = assess(c, s); e != nil {
			return r, e
		}
		if !s.Present[item.Object] {
			return r, fail("DAMENG_SCHEMA_POSTCONDITION_FAILED")
		}
		if e = b.complete(ctx, item); e != nil {
			return r, e
		}
		// Recovery is one exact operation, never implicit continuation of a batch.
		s, e = b.inspect(ctx)
		if e != nil {
			return r, e
		}
		return assess(c, s)
	}
	if mode == Reconcile || mode == Retry {
		return r, nil
	}
	for i := r.AppliedSteps; i < len(c.steps); i++ {
		if e = ctx.Err(); e != nil {
			return r, e
		}
		item := c.steps[i]
		if e = b.start(ctx, item); e != nil {
			return r, e
		}
		r.DirtyStep = item.Number
		if e = ctx.Err(); e != nil {
			return r, e
		}
		if e = b.execute(ctx, item); e != nil {
			return r, e
		}
		s, e = b.inspect(ctx)
		if e != nil {
			return r, e
		}
		if _, e = assess(c, s); e != nil {
			return r, e
		}
		if !s.Present[item.Object] {
			return r, fail("DAMENG_SCHEMA_POSTCONDITION_FAILED")
		}
		if e = b.complete(ctx, item); e != nil {
			return r, e
		}
		s, e = b.inspect(ctx)
		if e != nil {
			return r, e
		}
		r, e = assess(c, s)
		if e != nil {
			return r, e
		}
	}
	return r, nil
}
