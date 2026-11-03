package dameng

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type cyclicConnectorError struct{}

func (*cyclicConnectorError) Error() string   { return "secret native diagnostic" }
func (e *cyclicConnectorError) Unwrap() error { return e }
func TestConnectorErrorsPreserveSignedCodesAndCommitUnknown(t *testing.T) {
	for _, code := range []int32{-6602, -2147483648, 2147483647} {
		e := SafeError(&ODBCError{code: "DAMENG_ODBC_FAILURE", sqlState: "23000", nativeCode: code}).(*Error)
		if e.NativeCode != code {
			t.Fatal("signed native code truncated")
		}
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, e), "6602") {
				t.Fatal("diagnostic detail formatted")
			}
		}
	}
	unknown := &ODBCError{code: "DAMENG_ODBC_COMMIT_UNKNOWN", nativeCode: -70000, commitSent: true}
	for _, e := range []error{errors.Join(context.Canceled, unknown), errors.Join(unknown, context.DeadlineExceeded), fmt.Errorf("wrapper: %w", unknown), ErrCommitOutcomeUnknown} {
		safe := SafeError(e)
		if !errors.Is(safe, ErrCommitOutcomeUnknown) || errors.Is(safe, context.Canceled) {
			t.Fatal("commit uncertainty lost")
		}
	}
	if e := SafeError(&cyclicConnectorError{}).(*Error); e.Kind != Unavailable {
		t.Fatal("cyclic graph admitted")
	}
	if e := SafeError(errors.New("password=secret /private/driver")).Error(); strings.Contains(e, "secret") || e != Unavailable {
		t.Fatal("raw diagnostic leaked")
	}
	if FailureCode(fail("DAMENG_NATIVE_INPUT_CHANGED")) != "DAMENG_NATIVE_INPUT_CHANGED" {
		t.Fatal("owned safe code lost")
	}
}
