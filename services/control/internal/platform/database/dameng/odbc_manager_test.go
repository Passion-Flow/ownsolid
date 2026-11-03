//go:build cgo && odbc_manager

package dameng

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"
)

// These tests call the real public manager only. They do not load a DM driver or
// claim database/TLS/permission qualification. Root must admit libodbc first.
func odbcNativeTestConfig() ODBCConfig {
	return ODBCConfig{FormatVersion: 2, ConnectionFile: "/run/private/connection", LoginSeconds: 3, ConnectionSeconds: 7, QuerySeconds: 11, MaximumParameterBytes: 8192, MaximumCellBytes: 16384, MaximumRows: 31, MaximumColumns: 19, MaximumResultBytes: 32768, MaximumSQLBytes: 4096, MaximumOpenConnections: 1, MaximumPhysicalConnections: 1}
}
func odbcCloseNativeTestManager(t *testing.T, m *ODBCManager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, e := m.Close(ctx)
	if e != nil || status.State != "closed" || status.PhysicalOwners != 0 {
		t.Errorf("manager cleanup incomplete: %+v %v", status, e)
	}
}
func TestODBCManagerOwnsRealHandleHierarchy(t *testing.T) {
	manager, e := NewODBCManagerForValidation(odbcNativeTestConfig())
	if e != nil {
		t.Fatal(e)
	}
	defer odbcCloseNativeTestManager(t, manager)
	connection, e := manager.reserve()
	if e != nil {
		t.Fatal(e)
	}
	connection.mu.Lock()
	e = odbcAllocateInto(odbcEnvironmentHandle, nil, &connection.environment)
	connection.publish("allocating_environment")
	if e == nil {
		e = odbcEnvironment(connection.environment)
	}
	if e == nil {
		e = odbcAllocateInto(odbcConnectionHandle, connection.environment, &connection.handle)
	}
	connection.publish("allocating_connection")
	connection.mu.Unlock()
	if cleanup := connection.Close(); cleanup != nil {
		t.Fatal(cleanup)
	}
	if e != nil {
		t.Fatal(e)
	}
	if manager.Status().PhysicalOwners != 0 {
		t.Fatal("actual handle ownership not released")
	}
}
func TestODBCManagerMissingDriverIsRealRefusal(t *testing.T) {
	cfg := odbcNativeTestConfig()
	cfg.ConnectionFile = odbcPrivatePolicyFixture(t, []byte("DRIVER={ProjectOneAbsentSyntheticDriver9EBF9C80}"))
	manager, e := NewODBCManagerForValidation(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer odbcCloseNativeTestManager(t, manager)
	pool, e := manager.BorrowPool()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	e = pool.PingContext(ctx)
	if e == nil {
		t.Fatal("missing driver connected")
	}
	var native *ODBCError
	if !errors.As(e, &native) {
		t.Fatal("missing-driver typed diagnostic fact lost")
	}
	if native.sqlState != "IM002" && native.sqlState != "01000" && native.sqlState != "IM003" {
		t.Fatalf("missing-driver standard diagnostic fact lost: code=%s state=%q native=%d", native.code, native.sqlState, native.nativeCode)
	}
	if manager.Status().PhysicalOwners != 0 {
		t.Fatal("failed actual connect cleanup still owns physical resources")
	}
}
func TestODBCBindingsUseOwnedNativeMemoryAndRefuseInvalid(t *testing.T) {
	decimal, e := Decimal("-123.45", 5, 2)
	if e != nil {
		t.Fatal(e)
	}
	values := []any{nil, "", "Unicode \u4e2d\u6587😀", []byte{}, []byte{0, 1, 255}, int64(-7), 1.5, decimal, time.Date(2026, 10, 23, 0, 0, 0, 123456000, time.UTC)}
	args := make([]driver.NamedValue, len(values))
	for i, v := range values {
		args[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	parameters, e := odbcBuildParameters(args, 8192)
	if e != nil {
		t.Fatal(e)
	}
	parameters.free()
	for _, v := range []any{string([]byte{0xff}), "zero\x00byte", time.Now(), map[string]string{"x": "y"}} {
		p, e := odbcBuildParameters([]driver.NamedValue{{Ordinal: 1, Value: v}}, 8192)
		if e == nil {
			p.free()
			t.Fatal("invalid native binding admitted")
		}
	}
	if p, e := odbcBuildParameters(args, 1); e == nil {
		p.free()
		t.Fatal("descriptor budget ignored")
	}
	if _, e = odbcIsolation(driver.IsolationLevel(sql.LevelRepeatableRead)); e == nil {
		t.Fatal("unsupported isolation admitted")
	}
}
func TestODBCMemoryRowsDiscardOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	rows := &odbcRows{ctx: ctx, columns: []string{"title"}, rows: [][]driver.Value{{"late-private-row"}}}
	cancel()
	dest := []driver.Value{"prior"}
	if e := rows.Next(dest); !errors.Is(e, context.Canceled) || dest[0] != nil {
		t.Fatal("cancelled row projected")
	}
	if e := rows.Close(); e != nil {
		t.Fatal(e)
	}
	if _, e := (odbcResult{affected: -1}).RowsAffected(); e == nil {
		t.Fatal("unknown native count invented")
	}
	live := &odbcRows{ctx: context.Background(), columns: []string{"v"}, rows: [][]driver.Value{{int64(1)}}}
	if e := live.Next(dest); e != nil || dest[0] != int64(1) {
		t.Fatal("bounded materialized value lost")
	}
	if e := live.Next(dest); !errors.Is(e, io.EOF) || dest[0] != nil {
		t.Fatal("exhausted row retained prior projection")
	}
}
