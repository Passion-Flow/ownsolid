package transaction

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type outcomeCycle struct{}

func (outcomeCycle) Error() string { return "EXPLICIT_TEST_CYCLE" }
func (outcomeCycle) Unwrap() error { return outcomeCycle{} }

type outcomeChildren []error

func (outcomeChildren) Error() string     { return "EXPLICIT_TEST_CHILDREN" }
func (e outcomeChildren) Unwrap() []error { return e }

func TestCommitOutcomeBoundsAndPrecedence(t *testing.T) {
	unknown, rollback := &Error{Kind: CommitUnknown}, &Error{Kind: CommitRolledBack}
	wide := make(outcomeChildren, maximumCommitOutcomeNodes+1)
	for i := range wide {
		wide[i] = context.Canceled
	}
	var deep error = context.Canceled
	for i := 0; i < maximumCommitOutcomeNodes; i++ {
		deep = fmt.Errorf("explicit test wrapper: %w", deep)
	}
	for _, sample := range []struct {
		name       string
		input      error
		want       ErrorKind
		classified bool
	}{
		{"nil", nil, "", false},
		{"ordinary", errors.New("ACCESS_DENIED"), "", false},
		{"typed_unknown", unknown, CommitUnknown, true},
		{"plain_unknown", errors.New(string(CommitUnknown)), CommitUnknown, true},
		{"wrapped_unknown", fmt.Errorf("private wrapper: %w", unknown), CommitUnknown, true},
		{"typed_rollback", rollback, CommitRolledBack, true},
		{"plain_rollback", errors.New(string(CommitRolledBack)), CommitRolledBack, true},
		{"rollback_then_unknown", errors.Join(rollback, unknown), CommitUnknown, true},
		{"cancel_then_unknown", errors.Join(context.Canceled, unknown), CommitUnknown, true},
		{"cancel_then_rollback", errors.Join(context.DeadlineExceeded, rollback), CommitRolledBack, true},
		{"only_cancel", errors.Join(context.Canceled, context.DeadlineExceeded), "", false},
		{"mixed_critical", errors.Join(context.Canceled, errors.New("private cause")), "", false},
		{"cycle", outcomeCycle{}, Unavailable, true},
		{"empty_join", outcomeChildren{}, Unavailable, true},
		{"nil_child", outcomeChildren{nil}, Unavailable, true},
		{"wide_graph", wide, Unavailable, true},
		{"deep_graph", deep, Unavailable, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			kind, ok := CommitOutcome(sample.input)
			if kind != sample.want || ok != sample.classified {
				t.Fatal("commit outcome classification changed", kind, ok)
			}
		})
	}
}
