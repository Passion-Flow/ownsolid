package transaction

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

type cyclicCancellationError struct{}

func (cyclicCancellationError) Error() string { return "EXPLICIT_TEST_CYCLIC_UNWRAP" }
func (cyclicCancellationError) Unwrap() error { return cyclicCancellationError{} }

type emptyCancellationError struct{}

func (emptyCancellationError) Error() string   { return "EXPLICIT_TEST_EMPTY_UNWRAP" }
func (emptyCancellationError) Unwrap() []error { return nil }

func TestCancellationOnlyPreservesActualFailureLeaves(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil", nil, false},
		{"cancelled", context.Canceled, true},
		{"deadline", context.DeadlineExceeded, true},
		{"wrapped_cancel", fmt.Errorf("explicit test wrapper: %w", context.Canceled), true},
		{"normal_join", errors.Join(context.Canceled, context.DeadlineExceeded), true},
		{"storage_cancel", &Error{Kind: Cancelled}, true},
		{"storage_timeout", &Error{Kind: Timeout}, true},
		{"unknown_commit", errors.Join(context.Canceled, &Error{Kind: CommitUnknown}), false},
		{"rolled_back_commit", errors.Join(context.DeadlineExceeded, &Error{Kind: CommitRolledBack}), false},
		{"nested_actual_failure", errors.Join(fmt.Errorf("wrapper: %w", context.Canceled), errors.Join(context.DeadlineExceeded, errors.New("EXPLICIT_TEST_ACTUAL_FAILURE"))), false},
		{"empty_graph", emptyCancellationError{}, false},
		{"cyclic_graph", cyclicCancellationError{}, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if actual := IsCancellationOnly(test.err); actual != test.expected {
				t.Fatalf("cancellation classification=%v expected=%v", actual, test.expected)
			}
		})
	}
}
