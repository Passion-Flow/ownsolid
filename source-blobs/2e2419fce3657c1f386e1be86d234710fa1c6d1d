package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestJoinedProcessDiagnosticsRetainOnlyNamedCauses(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"duplicate_shutdown", errors.Join(errors.New("SSO_MAINTENANCE_FAILED"), errors.New("SSO_MAINTENANCE_FAILED")), "CONTROL_FAILURE_CODE SSO_MAINTENANCE_FAILED\n"},
		{"multiple", errors.Join(errors.New("RETENTION_WORKER_FAILED"), errors.New("CONTROL_WORKERS_NOT_JOINED")), "CONTROL_FAILURE_CODE CONTROL_WORKERS_NOT_JOINED\nCONTROL_FAILURE_CODE RETENTION_WORKER_FAILED\n"},
		{"wrapped_private_detail", fmt.Errorf("private DSN postgres://user:password@host/database: %w", errors.New("STORAGE_COMMIT_OUTCOME_UNKNOWN")), "CONTROL_FAILURE_CODE STORAGE_COMMIT_OUTCOME_UNKNOWN\n"},
		{"untrusted_uppercase", errors.New("PRIVATE_CUSTOMER_SECRET"), ""},
		{"private_leaf", errors.New("private /path?token=secret"), ""},
		{"prefix_injection", errors.New("SSO_MAINTENANCE_FAILED\nPRIVATE_CUSTOMER_SECRET"), ""},
		{"mixed", errors.Join(errors.New("SSO_MAINTENANCE_FAILED"), errors.New("password=private-value")), "CONTROL_FAILURE_CODE SSO_MAINTENANCE_FAILED\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			reportProcessFailure(&out, test.err)
			if out.String() != test.want {
				t.Fatalf("redacted diagnostic mismatch: %q", out.String())
			}
		})
	}
	joined := errors.Join(errors.New("SSO_MAINTENANCE_FAILED"), errors.New("SSO_MAINTENANCE_FAILED"))
	if safeError(joined) != "OPERATION_FAILED" {
		t.Fatal("legacy terminal output changed")
	}
}

func TestRuntimeFailureDiagnosticsSurviveActualLifecycleJoin(t *testing.T) {
	g := newRuntimeLifecycle(t.Context())
	g.Go("MAIL_WORKER", func(context.Context) error {
		return errors.New("private vendor connection password=value")
	})
	<-g.ctx.Done()
	cleanup, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	closed := g.Close(cleanup)
	if closed == nil || g.Failure() == nil {
		t.Fatal("runtime failure was suppressed")
	}
	joined := errors.Join(g.Failure(), closed)
	var out bytes.Buffer
	reportProcessFailure(&out, joined)
	if safeError(joined) != "OPERATION_FAILED" || out.String() != "CONTROL_FAILURE_CODE MAIL_WORKER_FAILED\n" {
		t.Fatal("joined worker cause was lost or private detail escaped", out.String())
	}
	select {
	case <-g.joined:
	default:
		t.Fatal("diagnostic changed actual join outcome")
	}
}

type cyclicProcessError struct{}

func (*cyclicProcessError) Error() string   { panic("wrapper Error must not be evaluated") }
func (e *cyclicProcessError) Unwrap() error { return e }

func TestProcessDiagnosticsBoundCyclicAndWideTrees(t *testing.T) {
	var out bytes.Buffer
	reportProcessFailure(&out, &cyclicProcessError{})
	if out.Len() != 0 {
		t.Fatal("cyclic wrapper emitted text")
	}
	children := make([]error, processErrorNodes+2)
	for index := range children {
		children[index] = errors.New("private detail")
	}
	children[len(children)-1] = errors.New("SSO_MAINTENANCE_FAILED")
	reportProcessFailure(&out, errors.Join(children...))
	if out.Len() != 0 {
		t.Fatal("unbounded wide-tree traversal")
	}
	if processFailureCode(strings.Repeat("A", 1<<20)) {
		t.Fatal("arbitrary large value became a code")
	}
}
