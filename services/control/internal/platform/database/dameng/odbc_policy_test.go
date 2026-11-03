package dameng

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Pure operation closures below are explicitly synthetic control fixtures. No
// driver manager, native driver, SQL connection or target support is exercised.
func TestODBCJoinRetainsOwnershipUntilOriginalAndCancelComplete(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started, originalRelease, cancelStarted, cancelRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		value, e := joinODBC(ctx, func() error { close(cancelStarted); <-cancelRelease; return nil }, nil, func() (any, error) { close(started); <-originalRelease; return "late-private-row", nil })
		if value != nil {
			finished <- errors.New("late projection")
			return
		}
		finished <- e
	}()
	<-started
	cancel()
	<-cancelStarted
	close(originalRelease)
	select {
	case <-finished:
		t.Fatal("released before cancellation completed")
	default:
	}
	close(cancelRelease)
	if e := <-finished; !errors.Is(e, context.Canceled) {
		t.Fatal("wrong cancellation result")
	}
}
func TestODBCJoinWaitsForOriginalAfterCancelAcknowledgement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started, release, acknowledged := make(chan struct{}), make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, e := joinODBC(ctx, func() error { close(acknowledged); return errors.New("synthetic cancel failure") }, nil, func() (any, error) { close(started); <-release; return 9, nil })
		finished <- e
	}()
	<-started
	cancel()
	<-acknowledged
	select {
	case <-finished:
		t.Fatal("cancel acknowledgement freed original operation")
	default:
	}
	close(release)
	if !errors.Is(<-finished, context.Canceled) {
		t.Fatal("wrong failure")
	}
}
func TestODBCJoinPrecancelAndPanicPublishZero(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	value, e := joinODBC(ctx, nil, nil, func() (any, error) { t.Fatal("pre-cancelled operation dispatched"); return nil, nil })
	if value != nil || !errors.Is(e, context.Canceled) {
		t.Fatal("pre-cancel projected")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, e = joinODBC(ctx, nil, nil, func() (any, error) { panic("synthetic private driver detail") })
	if value != nil || e == nil || e.Error() != "DAMENG_ODBC_OPERATION_PANIC" {
		t.Fatal("panic leaked or projected")
	}
}
func TestODBCPolicyAndExactDecimalBoundaries(t *testing.T) {
	valid := ODBCConfig{FormatVersion: 2, ConnectionFile: "/run/owned/connection", LoginSeconds: 3, ConnectionSeconds: 7, QuerySeconds: 11, MaximumParameterBytes: 8192, MaximumCellBytes: 16384, MaximumRows: 31, MaximumColumns: 19, MaximumResultBytes: 32768, MaximumSQLBytes: 4096, MaximumOpenConnections: 2, MaximumPhysicalConnections: 2}
	if valid.Validate() != nil {
		t.Fatal("nondefault policy rejected")
	}
	for _, change := range []func(*ODBCConfig){func(c *ODBCConfig) { c.MaximumRows = 0 }, func(c *ODBCConfig) { c.ConnectionFile = "relative" }, func(c *ODBCConfig) { c.LoginSeconds = 0 }, func(c *ODBCConfig) { c.MaximumColumns = 129 }} {
		candidate := valid
		change(&candidate)
		if candidate.Validate() == nil {
			t.Fatal("invalid accepted")
		}
	}
	for _, text := range []string{"0.00", "-0.12", "123.45"} {
		if _, e := Decimal(text, 5, 2); e != nil {
			t.Fatal("exact decimal refused")
		}
	}
	for _, text := range []string{"1e2", "NaN", "01.2", "1234.56", "1.234", "secret"} {
		if _, e := Decimal(text, 5, 2); e == nil {
			t.Fatal("invalid decimal admitted")
		}
	}
}

func TestODBCErrorRedactsDiagnosticFacts(t *testing.T) {
	e := &ODBCError{code: "DAMENG_ODBC_COMMIT_UNKNOWN", sqlState: "08S01", nativeCode: -6602, commitSent: true}
	if e.Error() != "DAMENG_ODBC_COMMIT_UNKNOWN" || e.String() != e.Error() || e.GoString() != e.Error() {
		t.Fatal("private native facts escaped")
	}
}

func TestODBCPhysicalPolicyBoundsAreIndependentAndRequired(t *testing.T) {
	valid := ODBCConfig{FormatVersion: 2, ConnectionFile: "/run/private/connection", LoginSeconds: 3, ConnectionSeconds: 7, QuerySeconds: 11, MaximumParameterBytes: 8192, MaximumCellBytes: 16384, MaximumRows: 31, MaximumColumns: 19, MaximumResultBytes: 32768, MaximumSQLBytes: 4096, MaximumOpenConnections: 1, MaximumPhysicalConnections: 2}
	if valid.Validate() != nil {
		t.Fatal("independent physical budget refused")
	}
	for _, change := range []func(*ODBCConfig){func(c *ODBCConfig) { c.MaximumPhysicalConnections = 0 }, func(c *ODBCConfig) { c.MaximumPhysicalConnections = 5 }, func(c *ODBCConfig) { c.MaximumOpenConnections = 3 }, func(c *ODBCConfig) { c.FormatVersion = 1 }} {
		candidate := valid
		change(&candidate)
		if candidate.Validate() == nil {
			t.Fatal("missing/contradictory physical ownership policy admitted")
		}
	}
}
