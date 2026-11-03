//go:build cgo && odbc_manager

package dameng

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// Explicit synthetic cleanup controls: these Go-memory tokens are NEVER passed
// to a native driver. Only this test port observes them. Queries/connect success,
// actual SQLFreeHandle and DM behavior are not simulated as product evidence.
type odbcCleanupFaults struct {
	mu      sync.Mutex
	fail    map[string]int
	calls   []string
	block   string
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *odbcCleanupFaults) call(name string) error {
	f.mu.Lock()
	f.calls = append(f.calls, name)
	fails := f.fail[name] > 0
	if fails {
		f.fail[name]--
	}
	block := name == f.block
	f.mu.Unlock()
	if block {
		f.once.Do(func() { close(f.entered) })
		<-f.release
	}
	if fails {
		return errors.New("synthetic-private-native-cleanup-detail")
	}
	return nil
}
func (f *odbcCleanupFaults) free(kind int, _ odbcHandle) error {
	return f.call(fmt.Sprint("free:", kind))
}
func (f *odbcCleanupFaults) reset(odbcHandle) error      { return f.call("reset") }
func (f *odbcCleanupFaults) disconnect(odbcHandle) error { return f.call("disconnect") }
func (f *odbcCleanupFaults) rollback(odbcHandle) error   { return f.call("rollback") }
func (f *odbcCleanupFaults) count() int                  { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }
func (f *odbcCleanupFaults) allow()                      { f.mu.Lock(); defer f.mu.Unlock(); f.fail = map[string]int{} }
func odbcSyntheticHandle() odbcHandle                    { return odbcHandle(unsafe.Pointer(new(byte))) }
func odbcOwnerTestManager(t *testing.T, faults *odbcCleanupFaults, limit int) *ODBCManager {
	t.Helper()
	cfg := odbcNativeTestConfig()
	cfg.MaximumPhysicalConnections = limit
	manager, e := NewODBCManagerForValidation(cfg)
	if e != nil {
		t.Fatal(e)
	}
	manager.cleanup = faults
	t.Cleanup(func() {
		faults.allow()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		status, e := manager.Close(ctx)
		if e != nil || status.State != "closed" || status.PhysicalOwners != 0 {
			t.Errorf("synthetic owner cleanup pending: %+v %v", status, e)
		}
	})
	return manager
}
func odbcOwnedSyntheticConnection(t *testing.T, m *ODBCManager) *odbcConnection {
	t.Helper()
	connection, e := m.reserve()
	if e != nil {
		t.Fatal(e)
	}
	connection.mu.Lock()
	connection.environment = odbcSyntheticHandle()
	connection.handle = odbcSyntheticHandle()
	connection.connected = true
	connection.publish("ready")
	connection.mu.Unlock()
	return connection
}
func odbcBoundedOwnerContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Second)
}
func TestODBCOwnerRetainsPartialFailureAndPhysicalBudget(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{fmt.Sprint("free:", odbcEnvironmentHandle): 2}}
	manager := odbcOwnerTestManager(t, faults, 1)
	connection, e := manager.reserve()
	if e != nil {
		t.Fatal(e)
	}
	connection.mu.Lock()
	connection.environment = odbcSyntheticHandle()
	connection.publish("allocating_environment")
	connection.mu.Unlock()
	if e = connection.Close(); e == nil {
		t.Fatal("synthetic native failure disappeared")
	}
	connection = nil // The registry, not this caller or a sql slot, retains the owner.
	status := manager.Status()
	if status.PhysicalOwners != 1 || !status.Connections[0].EnvironmentAssigned || status.Connections[0].LastCleanupError == "" {
		t.Fatal("partial allocation owner lost")
	}
	for i := 0; i < 8; i++ {
		if c, e := manager.reserve(); c != nil || e == nil || e.Error() != "DAMENG_ODBC_PHYSICAL_BUDGET_EXHAUSTED" {
			t.Fatal("failed connect escaped physical budget")
		}
	}
	if faults.count() != 1 {
		t.Fatal("budget exhaustion dispatched native work")
	}
	ctx, cancel := odbcBoundedOwnerContext()
	defer cancel()
	if status, e = manager.RetryCleanup(ctx); e == nil || status.PhysicalOwners != 1 {
		t.Fatal("failed explicit retry lost owner")
	}
	faults.allow()
	if status, e = manager.RetryCleanup(ctx); e != nil || status.PhysicalOwners != 0 {
		t.Fatal("successful explicit cleanup did not release budget")
	}
	replacement, e := manager.reserve()
	if e != nil {
		t.Fatal("budget not returned after actual modeled release")
	}
	if e = replacement.Close(); e != nil {
		t.Fatal(e)
	}
}

// This adoption connector is an explicit test double. It lets REAL database/sql
// close its ci and update numOpen while the native cleanup port fails. It makes
// no network call and is not an ODBC/DM protocol or successful product adapter.
type odbcSyntheticAdoption struct {
	mu         sync.Mutex
	connection *odbcConnection
}

func (c *odbcSyntheticAdoption) Driver() driver.Driver { return odbcDriver{} }
func (c *odbcSyntheticAdoption) Connect(context.Context) (driver.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connection == nil {
		return nil, errors.New("synthetic adoption already consumed")
	}
	owned := c.connection
	c.connection = nil
	return owned, nil
}
func TestODBCOwnerOutlivesRealSQLFinalCloseFailure(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{fmt.Sprint("free:", odbcConnectionHandle): 1}}
	manager := odbcOwnerTestManager(t, faults, 1)
	connection := odbcOwnedSyntheticConnection(t, manager)
	// Replace only the disposable test pool, before any pool consumer exists.
	if e := manager.pool.Close(); e != nil {
		t.Fatal(e)
	}
	manager.pool = sql.OpenDB(&odbcSyntheticAdoption{connection: connection})
	manager.pool.SetMaxOpenConns(1)
	manager.pool.SetMaxIdleConns(0)
	ctx, cancel := odbcBoundedOwnerContext()
	defer cancel()
	borrowed, e := manager.pool.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	_ = borrowed.Close()
	borrowed = nil
	connection = nil
	if manager.pool.Stats().OpenConnections != 0 {
		t.Fatal("real database/sql did not discard its logical slot")
	}
	if manager.Status().PhysicalOwners != 1 {
		t.Fatal("native owner followed discarded sql ci")
	}
	status, e := manager.RetryCleanup(ctx)
	if e != nil || status.PhysicalOwners != 0 {
		t.Fatal("retained owner could not explicitly release")
	}
}
func TestODBCOwnerStatementFailureRetainsExactBindingReservation(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{fmt.Sprint("free:", odbcStatementHandle): 1}}
	manager := odbcOwnerTestManager(t, faults, 1)
	connection := odbcOwnedSyntheticConnection(t, manager)
	connection.mu.Lock()
	statement, e := connection.reserveStatement()
	if e != nil {
		connection.mu.Unlock()
		t.Fatal(e)
	}
	statement.handle = odbcSyntheticHandle()
	token := statement.handle
	// Empty native buffer ownership token: C free(nil) is harmless. The assertion
	// checks that the exact reservation survives failure; no actual driver is used.
	parameters := &odbcParameters{}
	statement.parameters = parameters
	connection.publish("operating")
	e = connection.releaseStatement(statement)
	if e == nil || statement.handle != token || statement.parameters != parameters || len(connection.statements) != 1 {
		connection.mu.Unlock()
		t.Fatal("failed prepare/execute cleanup dropped exact statement/bindings")
	}
	connection.mu.Unlock()
	if status := manager.Status(); status.PhysicalOwners != 1 || status.Connections[0].StatementsAssigned != 1 || status.Connections[0].BindingReservations != 1 {
		t.Fatal("statement owner not observable")
	}
	ctx, cancel := odbcBoundedOwnerContext()
	defer cancel()
	status, e := manager.RetryCleanup(ctx)
	if e != nil || status.PhysicalOwners != 0 || statement.handle != nil || statement.parameters != nil {
		t.Fatal("statement retry failed to release owned graph")
	}
}
func TestODBCOwnerEveryNativeCleanupStageKeepsParentOwnership(t *testing.T) {
	for _, stage := range []string{"rollback", "disconnect", fmt.Sprint("free:", odbcConnectionHandle), fmt.Sprint("free:", odbcEnvironmentHandle)} {
		t.Run(stage, func(t *testing.T) {
			faults := &odbcCleanupFaults{fail: map[string]int{stage: 1}}
			manager := odbcOwnerTestManager(t, faults, 1)
			connection := odbcOwnedSyntheticConnection(t, manager)
			if stage == "rollback" {
				connection.mu.Lock()
				connection.pendingTransaction = true
				connection.mu.Unlock()
			}
			if e := connection.Close(); e == nil || e.Error() != "DAMENG_ODBC_NATIVE_RESOURCES_RETAINED" {
				t.Fatal("cleanup failure lost fixed classification")
			}
			status := manager.Status()
			if status.PhysicalOwners != 1 || !status.Connections[0].EnvironmentAssigned {
				t.Fatal("native parent owner lost on failure")
			}
			ctx, cancel := odbcBoundedOwnerContext()
			defer cancel()
			status, e := manager.RetryCleanup(ctx)
			if e != nil || status.PhysicalOwners != 0 {
				t.Fatal("explicit staged cleanup failed")
			}
		})
	}
}
func TestODBCOwnerCleanupWaitsForOriginalAndCancelJoin(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{}}
	manager := odbcOwnerTestManager(t, faults, 1)
	connection := odbcOwnedSyntheticConnection(t, manager)
	originalRelease, cancelRelease, started, cancelStarted, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOriginal, releaseCancel sync.Once
	t.Cleanup(func() {
		releaseOriginal.Do(func() { close(originalRelease) })
		releaseCancel.Do(func() { close(cancelRelease) })
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("synthetic joined operation still active")
		}
	})
	operation, cancelOperation := odbcBoundedOwnerContext()
	defer cancelOperation()
	go func() {
		connection.mu.Lock()
		defer connection.mu.Unlock()
		defer close(finished)
		// An explicit retire/poison intent is modeled while the operation owns its
		// mutex. Cleanup must queue and cannot free its native graph before join.
		connection.poisoned = true
		connection.retired = true
		connection.publish("operating")
		_, _ = joinODBC(operation, func() error { close(cancelStarted); <-cancelRelease; return nil }, func() { manager.markDraining(connection.ownerID) }, func() (any, error) { close(started); <-originalRelease; return nil, nil })
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("synthetic operation did not start")
	}
	cancelOperation()
	select {
	case <-cancelStarted:
	case <-time.After(time.Second):
		t.Fatal("synthetic cancellation did not start")
	}
	short, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	status, e := manager.RetryCleanup(short)
	if !errors.Is(e, context.DeadlineExceeded) || status.PhysicalOwners != 1 || faults.count() != 0 {
		t.Fatal("cleanup freed active/joining operation")
	}
	releaseOriginal.Do(func() { close(originalRelease) })
	if faults.count() != 0 {
		t.Fatal("original completion skipped outstanding cancel join")
	}
	releaseCancel.Do(func() { close(cancelRelease) })
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("joined operation did not finish")
	}
	ctx, cancel := odbcBoundedOwnerContext()
	defer cancel()
	status, e = manager.RetryCleanup(ctx)
	if e != nil || status.PhysicalOwners != 0 {
		t.Fatal("joined cleanup did not finish")
	}
}
func TestODBCOwnerRepeatedRetriesJoinSingleTrackedCleanup(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{}, block: "disconnect", entered: make(chan struct{}), release: make(chan struct{})}
	manager := odbcOwnerTestManager(t, faults, 1)
	connection := odbcOwnedSyntheticConnection(t, manager)
	connection.mu.Lock()
	connection.poisoned = true
	connection.publish("poisoned")
	connection.mu.Unlock()
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(faults.release) }) })
	short, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	status, e := manager.RetryCleanup(short)
	if !errors.Is(e, context.DeadlineExceeded) || status.CleanupJobs != 1 || status.PhysicalOwners != 1 {
		t.Fatal("blocked cleanup was not retained")
	}
	if replacement, failure := manager.reserve(); replacement != nil || failure == nil || failure.Error() != "DAMENG_ODBC_PHYSICAL_BUDGET_EXHAUSTED" {
		t.Fatal("in-flight cleanup returned physical budget early")
	}
	select {
	case <-faults.entered:
	case <-time.After(time.Second):
		t.Fatal("synthetic cleanup did not enter")
	}
	second, cancelSecond := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelSecond()
	status, e = manager.RetryCleanup(second)
	if !errors.Is(e, context.DeadlineExceeded) || status.CleanupJobs != 1 || faults.count() != 1 {
		t.Fatal("repeated request spawned duplicate native cleanup")
	}
	release.Do(func() { close(faults.release) })
	ctx, cancel := odbcBoundedOwnerContext()
	defer cancel()
	status, e = manager.Close(ctx)
	if e != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
		t.Fatal("explicit manager shutdown did not join cleanup")
	}
	if _, e = manager.BorrowPool(); e == nil {
		t.Fatal("closed manager handed out pool")
	}
}

func TestODBCOwnerStatusAndManagerFormattingStayRedacted(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{fmt.Sprint("free:", odbcConnectionHandle): 1}}
	manager := odbcOwnerTestManager(t, faults, 1)
	connection := odbcOwnedSyntheticConnection(t, manager)
	if e := connection.Close(); e == nil {
		t.Fatal("synthetic cleanup did not fail")
	}
	for _, rendered := range []string{fmt.Sprint(manager), fmt.Sprintf("%#v", manager)} {
		if rendered != "DAMENG_ODBC_MANAGER_REDACTED" {
			t.Fatal("manager formatting exposed internal state")
		}
	}
	status := manager.Status()
	if len(status.Connections) != 1 || status.Connections[0].LastCleanupError != "DAMENG_ODBC_NATIVE_RESOURCES_RETAINED" || status.Connections[0].NativeCleanupActive || status.Connections[0].CleanupPending {
		t.Fatal("fixed cleanup status unavailable after synchronous failure")
	}
}

// A still-borrowed healthy logical connection is not proof of completed Close.
// Only the explicit test adoption connector supplies synthetic native tokens.
func TestODBCOwnerCloseReportsBorrowedOwnerUntilCallerReturnsIt(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{}}
	manager := odbcOwnerTestManager(t, faults, 1)
	connection := odbcOwnedSyntheticConnection(t, manager)
	if e := manager.pool.Close(); e != nil {
		t.Fatal(e)
	}
	manager.pool = sql.OpenDB(&odbcSyntheticAdoption{connection: connection})
	manager.pool.SetMaxOpenConns(1)
	manager.pool.SetMaxIdleConns(0)
	ctx, cancel := odbcBoundedOwnerContext()
	defer cancel()
	borrowed, e := manager.pool.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer borrowed.Close()
	status, e := manager.Close(ctx)
	if e == nil || e.Error() != "DAMENG_ODBC_NATIVE_OWNERS_PENDING" || status.State == "closed" || status.PhysicalOwners != 1 || faults.count() != 0 {
		t.Fatal("close claimed borrowed native owner released")
	}
	if replacement, e := manager.reserve(); replacement != nil || e == nil || e.Error() != "DAMENG_ODBC_MANAGER_CLOSING" {
		t.Fatal("shutdown admitted another physical reservation")
	}
	if e = borrowed.Close(); e != nil {
		t.Fatal(e)
	}
	status, e = manager.Close(ctx)
	if e != nil || status.State != "closed" || status.PhysicalOwners != 0 {
		t.Fatal("returned borrowed owner could not finish shutdown")
	}
}
func TestODBCOwnerResetFailureAfterDefiniteFreeStillPoisons(t *testing.T) {
	faults := &odbcCleanupFaults{fail: map[string]int{"reset": 1}}
	manager := odbcOwnerTestManager(t, faults, 1)
	connection := odbcOwnedSyntheticConnection(t, manager)
	connection.mu.Lock()
	statement, e := connection.reserveStatement()
	if e != nil {
		connection.mu.Unlock()
		t.Fatal(e)
	}
	statement.handle = odbcSyntheticHandle()
	statement.parameters = &odbcParameters{}
	e = connection.releaseStatement(statement)
	if e == nil || e.Error() != "DAMENG_ODBC_STATEMENT_CLEANUP_FAILED" || statement.handle != nil || statement.parameters != nil || len(connection.statements) != 0 || !connection.poisoned {
		connection.mu.Unlock()
		t.Fatal("reset failure claimed success or discarded failed live ownership")
	}
	connection.mu.Unlock()
	if status := manager.Status(); status.PhysicalOwners != 1 || !status.Connections[0].Poisoned {
		t.Fatal("poisoned parent owner not retained")
	}
	ctx, cancel := odbcBoundedOwnerContext()
	defer cancel()
	if status, e := manager.RetryCleanup(ctx); e != nil || status.PhysicalOwners != 0 {
		t.Fatal("reset failure parent cleanup failed")
	}
}
