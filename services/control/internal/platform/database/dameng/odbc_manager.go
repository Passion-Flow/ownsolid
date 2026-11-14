//go:build cgo && odbc_manager

package dameng

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"time"
	"unicode/utf16"
)

type odbcConnector struct{ manager *ODBCManager }

func (c *odbcConnector) Driver() driver.Driver { return odbcDriver{} }
func (odbcDriver) Open(string) (driver.Conn, error) {
	return nil, odbcError("DAMENG_ODBC_EXPLICIT_CONNECTOR_REQUIRED")
}

type odbcDriver struct{}

func (c *odbcConnector) Connect(ctx context.Context) (out driver.Conn, result error) {
	if e := odbcContext(ctx); e != nil {
		return nil, e
	}
	conn, e := c.manager.reserve()
	if e != nil {
		return nil, e
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	valid := false
	defer func() {
		if !valid {
			out = nil
			conn.closed = true
			conn.retired = true
			conn.poisoned = true
			conn.publish("retired")
			if cleanup := conn.closeNative(); cleanup != nil {
				result = odbcError("DAMENG_ODBC_NATIVE_RESOURCES_RETAINED")
			}
		}
	}()
	if conn.closed || !c.manager.accepting() {
		return nil, odbcError("DAMENG_ODBC_MANAGER_CLOSING")
	}
	var raw []byte
	if c.manager.connection == nil {
		raw, e = readODBCPrivateInput(conn.cfg.ConnectionFile, 65536)
	} else {
		raw, e = c.manager.connection.connectionString(ctx)
	}
	if e != nil {
		return nil, odbcError("DAMENG_ODBC_CONNECTION_FILE_INVALID")
	}
	defer clear(raw)
	text := string(raw)
	if !validODBCText(text, 65536) || len(text) == 0 || len(utf16.Encode([]rune(text))) > 32760 {
		return nil, odbcError("DAMENG_ODBC_CONNECTION_FILE_INVALID")
	}
	_, e = joinODBC(ctx, nil, func() { conn.owner.markDraining(conn.ownerID) }, func() (any, error) {
		conn.publish("allocating_environment")
		if native := odbcAllocateInto(odbcEnvironmentHandle, nil, &conn.environment); native != nil {
			conn.publish("poisoned")
			return nil, native
		}
		conn.publish("allocating_environment")
		if native := odbcEnvironment(conn.environment); native != nil {
			return nil, native
		}
		if native := ctx.Err(); native != nil {
			return nil, native
		}
		conn.publish("allocating_connection")
		if native := odbcAllocateInto(odbcConnectionHandle, conn.environment, &conn.handle); native != nil {
			conn.publish("poisoned")
			return nil, native
		}
		conn.publish("connecting")
		attributes := []struct {
			key   int
			value uint32
		}{
			{odbcLoginTimeoutAttribute, uint32(conn.cfg.LoginSeconds)},
			{odbcConnectionTimeoutAttribute, uint32(conn.cfg.ConnectionSeconds)},
		}
		for _, attribute := range attributes {
			if native := odbcConnectAttribute(conn.handle, attribute.key, attribute.value); native != nil {
				return nil, native
			}
		}
		if native := ctx.Err(); native != nil {
			return nil, native
		}
		var native error
		conn.connected, native = odbcConnect(conn.handle, text)
		if native != nil {
			return nil, native
		}
		// Driver-manager attribute reads can require an open connection. Keep
		// setting before connect, then require exact readback before using it.
		for _, attribute := range attributes {
			effective, native := odbcGetConnectAttribute(conn.handle, attribute.key)
			if native != nil {
				return nil, native
			}
			if effective != attribute.value {
				return nil, odbcError("DAMENG_ODBC_TIMEOUT_SUBSTITUTED")
			}
		}
		if native = odbcConnectAttribute(conn.handle, odbcAutocommitAttribute, odbcAutocommitOn); native != nil {
			return nil, native
		}
		effective, native := odbcGetConnectAttribute(conn.handle, odbcAutocommitAttribute)
		if native != nil {
			return nil, native
		}
		if effective != odbcAutocommitOn {
			return nil, odbcError("DAMENG_ODBC_AUTOCOMMIT_SUBSTITUTED")
		}
		if conn.cfg.FormatVersion == 3 {
			if native = odbcVerifyLongFunctions(conn.handle); native != nil {
				return nil, native
			}
		}
		if native = ctx.Err(); native != nil {
			return nil, native
		}
		return nil, nil
	})
	if e != nil {
		return nil, e
	}
	if _, e = conn.execute(ctx, "SET TIME ZONE '+00:00'", nil, false); e != nil {
		return nil, e
	}
	if c.manager.connection != nil {
		if e = conn.verifyNativeConnection(ctx, c.manager.connection); e != nil {
			return nil, e
		}
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if !c.manager.accepting() {
		return nil, odbcError("DAMENG_ODBC_MANAGER_CLOSING")
	}
	conn.publish("ready")
	valid = true
	return conn, nil
}

type odbcConnection struct {
	mu                                             sync.Mutex
	owner                                          *ODBCManager
	ownerID                                        uint64
	cfg                                            ODBCConfig
	environment, handle                            odbcHandle
	connected, poisoned, closed, retired, released bool
	pendingTransaction                             bool
	pinnedSession                                  bool
	tx                                             *odbcTransaction
	statements                                     map[*odbcNativeStatement]struct{}
}

var _ driver.Connector = (*odbcConnector)(nil)
var _ driver.Conn = (*odbcConnection)(nil)
var _ driver.ConnPrepareContext = (*odbcConnection)(nil)
var _ driver.ExecerContext = (*odbcConnection)(nil)
var _ driver.QueryerContext = (*odbcConnection)(nil)
var _ driver.ConnBeginTx = (*odbcConnection)(nil)
var _ driver.Pinger = (*odbcConnection)(nil)
var _ driver.Validator = (*odbcConnection)(nil)
var _ driver.SessionResetter = (*odbcConnection)(nil)
var _ driver.NamedValueChecker = (*odbcConnection)(nil)

func (c *odbcConnection) ready() error {
	if c.closed || c.poisoned || c.released || !c.connected || !c.owner.accepting() {
		return odbcError("DAMENG_ODBC_CONNECTION_UNAVAILABLE")
	}
	return nil
}
func (c *odbcConnection) IsValid() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.ready() == nil }
func (c *odbcConnection) ResetSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// ErrBadConn is only emitted before a NEW operation. A mutation error never
	// returns ErrBadConn, preventing database/sql from automatically replaying it.
	if c.ready() != nil || c.tx != nil {
		return driver.ErrBadConn
	}
	return odbcContext(ctx)
}
func (c *odbcConnection) CheckNamedValue(v *driver.NamedValue) error {
	if v.Name != "" {
		return odbcError("DAMENG_ODBC_NAMED_PARAMETERS_UNSUPPORTED")
	}
	if _, ok := v.Value.(ODBCDecimal); ok {
		return nil
	}
	value, e := driver.DefaultParameterConverter.ConvertValue(v.Value)
	if e != nil {
		return odbcError("DAMENG_ODBC_PARAMETER_TYPE_UNSUPPORTED")
	}
	v.Value = value
	return nil
}
func (c *odbcConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.retired = true
	c.publish("retired")
	return c.closeNative()
}
func (c *odbcConnection) closeNative() (result error) {
	if c.released {
		return nil
	}
	complete := c.beginNativeCleanup()
	defer func() {
		if recover() != nil {
			result = odbcError("DAMENG_ODBC_NATIVE_RESOURCES_RETAINED")
		}
		if result != nil {
			c.cleanupFailure()
		}
		complete()
	}()
	for statement := range c.statements {
		if e := c.releaseStatement(statement); e != nil {
			return odbcRetained()
		}
	}
	if c.handle != nil {
		if c.tx != nil || c.pendingTransaction {
			if e := c.owner.cleanup.rollback(c.handle); e != nil {
				return odbcRetained()
			}
			c.tx = nil
			c.pendingTransaction = false
		}
		if c.connected {
			if e := c.owner.cleanup.disconnect(c.handle); e != nil {
				return odbcRetained()
			}
			c.connected = false
		}
		if e := c.owner.cleanup.free(odbcConnectionHandle, c.handle); e != nil {
			return odbcRetained()
		}
		c.handle = nil
	}
	if c.environment != nil {
		if e := c.owner.cleanup.free(odbcEnvironmentHandle, c.environment); e != nil {
			return odbcRetained()
		}
		c.environment = nil
	}
	c.publish("retired")
	return c.releaseOwner()
}
func odbcRetained() error { return odbcError("DAMENG_ODBC_NATIVE_RESOURCES_RETAINED") }

type odbcNativeStatement struct {
	handle     odbcHandle
	parameters *odbcParameters
	readBuffer *odbcReadBuffer
}

func (c *odbcConnection) reserveStatement() (*odbcNativeStatement, error) {
	if len(c.statements) != 0 {
		return nil, odbcError("DAMENG_ODBC_STATEMENT_OWNER_BUSY")
	}
	entry := &odbcNativeStatement{}
	c.statements[entry] = struct{}{}
	c.publish("allocating_statement")
	return entry, nil
}
func (c *odbcConnection) allocateStatement(entry *odbcNativeStatement) error {
	e := odbcAllocateInto(odbcStatementHandle, c.handle, &entry.handle)
	c.publish("operating")
	return e
}
func (c *odbcConnection) releaseStatement(entry *odbcNativeStatement) error {
	if _, ok := c.statements[entry]; !ok {
		return nil
	}
	var reset error
	if entry.handle != nil {
		if entry.parameters != nil {
			reset = c.owner.cleanup.reset(entry.handle)
		}
		if e := c.owner.cleanup.free(odbcStatementHandle, entry.handle); e != nil {
			c.cleanupFailure()
			return odbcRetained()
		}
		entry.handle = nil
	}
	if entry.parameters != nil {
		entry.parameters.free()
		entry.parameters = nil
	}
	if entry.readBuffer != nil {
		entry.readBuffer.free()
		entry.readBuffer = nil
	}
	delete(c.statements, entry)
	c.publish("operating")
	if reset != nil {
		c.poisoned = true
		c.publish("poisoned")
		return odbcError("DAMENG_ODBC_STATEMENT_CLEANUP_FAILED")
	}
	return nil
}

func (c *odbcConnection) Prepare(query string) (driver.Stmt, error) {
	return nil, odbcError("DAMENG_ODBC_CONTEXT_REQUIRED")
}
func (c *odbcConnection) PrepareContext(ctx context.Context, query string) (out driver.Stmt, result error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.ready(); e != nil {
		return nil, e
	}
	if e := c.validateQuery(ctx, query); e != nil {
		return nil, e
	}
	entry, e := c.reserveStatement()
	if e != nil {
		return nil, e
	}
	defer func() {
		if cleanup := c.releaseStatement(entry); cleanup != nil {
			out = nil
			result = cleanup
		}
		if result != nil {
			c.poisoned = true
			c.publish("poisoned")
		}
		if ctx.Err() != nil {
			c.poisoned = true
			out = nil
			if result == nil {
				result = ctx.Err()
			}
			c.publish("poisoned")
		}
	}()
	if e = c.allocateStatement(entry); e != nil {
		return nil, e
	}
	_, e = joinODBC(ctx, func() error { return odbcCancel(entry.handle) }, func() { c.owner.markDraining(c.ownerID) }, func() (any, error) {
		if native := odbcStatementSynchronous(entry.handle); native != nil {
			return nil, native
		}
		if native := odbcStatementTimeout(entry.handle, c.cfg.QuerySeconds); native != nil {
			return nil, native
		}
		if native := ctx.Err(); native != nil {
			return nil, native
		}
		return nil, odbcPrepare(entry.handle, query)
	})
	if e != nil {
		return nil, e
	}
	return &odbcStatement{connection: c, query: query}, nil
}

func (c *odbcConnection) validateQuery(ctx context.Context, query string) error {
	if e := odbcContext(ctx); e != nil {
		return e
	}
	if query == "" || !validODBCText(query, c.cfg.MaximumSQLBytes) {
		return odbcError("DAMENG_ODBC_SQL_INVALID")
	}
	return nil
}

type odbcMaterialized struct {
	columns  []string
	rows     [][]driver.Value
	affected int64
	memory   *odbcMemoryLease
}

func (c *odbcConnection) execute(ctx context.Context, query string, args []driver.NamedValue, wantRows bool) (out *odbcMaterialized, result error) {
	if e := c.ready(); e != nil {
		return nil, e
	}
	if e := c.validateQuery(ctx, query); e != nil {
		return nil, e
	}
	entry, e := c.reserveStatement()
	if e != nil {
		return nil, e
	}
	var resultMemory *odbcMemoryLease
	defer func() {
		if cleanup := c.releaseStatement(entry); cleanup != nil {
			out = nil
			result = cleanup
		}
		if result != nil {
			c.poisoned = true
			c.publish("poisoned")
		}
		if ctx.Err() != nil {
			c.poisoned = true
			out = nil
			if result == nil {
				result = ctx.Err()
			}
			c.publish("poisoned")
		}
		if result != nil || out == nil {
			resultMemory.close()
		}
	}()
	parameterMemory, e := c.owner.memoryLease()
	if e != nil {
		return nil, e
	}
	parameters, e := odbcBuildParametersOwned(args, c.cfg, parameterMemory)
	if e != nil {
		parameters.free()
		parameterMemory.close()
		return nil, e
	}
	entry.parameters = &parameters
	if wantRows {
		resultMemory, e = c.owner.memoryLease()
		if e != nil {
			return nil, e
		}
		scratchMemory, err := c.owner.memoryLease()
		if err != nil {
			return nil, err
		}
		entry.readBuffer, e = odbcNewReadBuffer(max(c.cfg.MaximumCellBytes+2, c.cfg.TransferChunkBytes, 32), scratchMemory)
		if e != nil {
			scratchMemory.close()
			return nil, e
		}
	}
	c.publish("allocating_statement")
	if e = c.allocateStatement(entry); e != nil {
		return nil, e
	}
	h := entry.handle
	value, e := joinODBC(ctx, func() error { return odbcCancel(h) }, func() { c.owner.markDraining(c.ownerID) }, func() (any, error) {
		if native := odbcStatementSynchronous(h); native != nil {
			return nil, native
		}
		if native := odbcStatementTimeout(h, c.cfg.QuerySeconds); native != nil {
			return nil, native
		}
		if native := ctx.Err(); native != nil {
			return nil, native
		}
		if native := odbcPrepare(h, query); native != nil {
			return nil, native
		}
		if native := ctx.Err(); native != nil {
			return nil, native
		}
		if native := odbcBindParameters(h, parameters); native != nil {
			return nil, native
		}
		if native := ctx.Err(); native != nil {
			return nil, native
		}
		if native := odbcExecuteParameters(ctx, h, &parameters); native != nil {
			return nil, native
		}
		if native := ctx.Err(); native != nil {
			return nil, native
		}
		data := &odbcMaterialized{memory: resultMemory}
		if !wantRows {
			n, native := odbcRowCount(h)
			if native != nil {
				return nil, native
			}
			data.affected = n
			return data, nil
		}
		if native := resultMemory.grow(c.cfg.MaximumColumns * odbcColumnMemoryAllowance); native != nil {
			return nil, native
		}
		columns, native := odbcColumns(h, c.cfg.MaximumColumns)
		if native != nil {
			return nil, native
		}
		total := 0
		for _, column := range columns {
			data.columns = append(data.columns, column.name)
			total += len(column.name)
		}
		if total > c.cfg.MaximumResultBytes {
			return nil, odbcError("DAMENG_ODBC_RESULT_LIMIT_EXCEEDED")
		}
		for {
			if native = ctx.Err(); native != nil {
				return nil, native
			}
			more, failure := odbcFetch(h)
			if failure != nil {
				return nil, failure
			}
			if !more {
				break
			}
			if len(data.rows) >= c.cfg.MaximumRows {
				return nil, odbcError("DAMENG_ODBC_ROW_LIMIT_EXCEEDED")
			}
			// Account for slice/interface descriptors, including all-NULL/empty rows.
			total += len(columns)*32 + 24
			if total > c.cfg.MaximumResultBytes {
				return nil, odbcError("DAMENG_ODBC_RESULT_LIMIT_EXCEEDED")
			}
			if failure := resultMemory.grow(len(columns)*32 + 24); failure != nil {
				return nil, failure
			}
			row := make([]driver.Value, len(columns))
			for i, column := range columns {
				if native = ctx.Err(); native != nil {
					return nil, native
				}
				long := c.cfg.FormatVersion == 3 && odbcLongKind(column.kind)
				var cell driver.Value
				var failure error
				if long {
					cell, failure = odbcLongCell(ctx, h, i+1, column.kind, c.cfg, entry.readBuffer, resultMemory)
				} else {
					allowance := c.cfg.MaximumCellBytes*4 + 64
					if failure = resultMemory.grow(allowance); failure != nil {
						return nil, failure
					}
					cell, failure = odbcCellIn(h, i+1, column.kind, c.cfg.MaximumCellBytes, entry.readBuffer)
					retained := 64
					switch v := cell.(type) {
					case string:
						retained += len(v) * 4
					case []byte:
						retained += len(v) * 4
					}
					resultMemory.shrink(allowance - retained)
				}
				if failure != nil {
					return nil, failure
				}
				row[i] = cell
				switch v := cell.(type) {
				case string:
					if !long {
						total += len(v)
					}
				case []byte:
					if !long {
						total += len(v)
					}
				default:
					total += 16
				}
				if total > c.cfg.MaximumResultBytes {
					return nil, odbcError("DAMENG_ODBC_RESULT_LIMIT_EXCEEDED")
				}
			}
			data.rows = append(data.rows, row)
		}
		if native = odbcCloseCursor(h); native != nil {
			return nil, native
		}
		return data, nil
	})
	if e != nil {
		c.poisoned = true
		c.publish("poisoned")
		return nil, e
	}
	return value.(*odbcMaterialized), nil
}
func (c *odbcConnection) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, e := c.execute(ctx, q, args, false)
	if e != nil {
		return nil, e
	}
	return odbcResult{affected: result.affected}, nil
}
func (c *odbcConnection) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result, e := c.execute(ctx, q, args, true)
	if e != nil {
		return nil, e
	}
	return &odbcRows{ctx: ctx, columns: result.columns, rows: result.rows, memory: result.memory}, nil
}
func (c *odbcConnection) Ping(ctx context.Context) error {
	rows, e := c.QueryContext(ctx, "SELECT 1 FROM DUAL", nil)
	if e != nil {
		return e
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if e = rows.Next(values); e != nil {
		return e
	}
	if values[0] != int64(1) {
		return odbcError("DAMENG_ODBC_PING_MISMATCH")
	}
	if e = rows.Next(values); !errors.Is(e, io.EOF) {
		return odbcError("DAMENG_ODBC_PING_MISMATCH")
	}
	return nil
}
func (c *odbcConnection) Begin() (driver.Tx, error) {
	return nil, odbcError("DAMENG_ODBC_CONTEXT_REQUIRED")
}
func (c *odbcConnection) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.ready(); e != nil {
		return nil, e
	}
	if e := odbcContext(ctx); e != nil {
		return nil, e
	}
	if c.tx != nil {
		return nil, odbcError("DAMENG_ODBC_TRANSACTION_ACTIVE")
	}
	isolation, e := odbcIsolation(options.Isolation)
	if e != nil {
		return nil, e
	}
	_, e = joinODBC(ctx, nil, func() { c.owner.markDraining(c.ownerID) }, func() (any, error) {
		if native := odbcConnectAttribute(c.handle, odbcIsolationAttribute, isolation); native != nil {
			return nil, native
		}
		effective, native := odbcGetConnectAttribute(c.handle, odbcIsolationAttribute)
		if native != nil {
			return nil, native
		}
		if effective != isolation {
			return nil, odbcError("DAMENG_ODBC_ISOLATION_SUBSTITUTED")
		}
		if native = ctx.Err(); native != nil {
			return nil, native
		}
		c.pendingTransaction = true
		if native = odbcConnectAttribute(c.handle, odbcAutocommitAttribute, odbcAutocommitOff); native != nil {
			return nil, native
		}
		effective, native = odbcGetConnectAttribute(c.handle, odbcAutocommitAttribute)
		if native != nil {
			return nil, native
		}
		if effective != odbcAutocommitOff {
			return nil, odbcError("DAMENG_ODBC_AUTOCOMMIT_SUBSTITUTED")
		}
		return nil, nil
	})
	if e != nil {
		c.poisoned = true
		c.publish("poisoned")
		return nil, e
	}
	tx := &odbcTransaction{connection: c, ctx: ctx}
	c.tx = tx
	// Explicitly select both modes on every transaction; a prior read-only
	// transaction cannot leak its mode into a later pinned write transaction.
	mode := "SET TRANSACTION READ WRITE"
	if options.ReadOnly {
		mode = "SET TRANSACTION READ ONLY"
	}
	if _, e = c.execute(ctx, mode, nil, false); e != nil {
		c.poisoned = true
		return nil, e
	}

	return tx, nil
}

type odbcTransaction struct {
	connection *odbcConnection
	ctx        context.Context
	done       bool
}

func (t *odbcTransaction) Commit() error {
	c := t.connection
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.done || c.tx != t {
		return odbcError("DAMENG_ODBC_TRANSACTION_CLOSED")
	}
	t.done = true
	if e := c.ready(); e != nil {
		return e
	}
	if e := odbcContext(t.ctx); e != nil {
		c.poisoned = true
		c.publish("poisoned")
		return e
	}
	sent := false
	_, e := joinODBC(t.ctx, nil, func() { c.owner.markDraining(c.ownerID) }, func() (any, error) {
		if native := t.ctx.Err(); native != nil {
			return nil, native
		}
		sent = true
		return nil, odbcEndTransaction(c.handle, true)
	})
	if e == nil {
		e = t.ctx.Err()
	}
	if e != nil {
		c.poisoned = true
		c.publish("poisoned")
		if sent {
			return &ODBCError{code: "DAMENG_ODBC_COMMIT_UNKNOWN", commitSent: true}
		}
		return e
	}
	c.tx = nil
	c.pendingTransaction = false
	if e = c.finishPinnedTransaction(t.ctx); e != nil {
		// COMMIT was sent. A failed reset must never be mistaken for rollback or
		// authorize replay of the callback, even if the native commit returned OK.
		return &ODBCError{code: "DAMENG_ODBC_COMMIT_UNKNOWN", commitSent: true}
	}
	return nil
}
func (t *odbcTransaction) Rollback() error {
	c := t.connection
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.done || c.tx != t {
		return odbcError("DAMENG_ODBC_TRANSACTION_CLOSED")
	}
	t.done = true
	// Even expired request contexts do not permit freeing an active native call.
	// Rollback joins synchronously; driver cancellation/timeout behavior is a
	// target admission requirement rather than a fabricated process deadline.
	e := odbcEndTransaction(c.handle, false)
	if e == nil {
		c.tx = nil
		c.pendingTransaction = false
	}
	if e != nil {
		c.poisoned = true
		c.publish("poisoned")
		return e
	}
	// A prior statement error remains poisoned even after definite rollback.
	// Use an independent bounded cleanup context; the request may be cancelled.
	cleanup, cancel := context.WithTimeout(context.Background(), time.Duration(c.cfg.ConnectionSeconds)*time.Second)
	defer cancel()
	return c.finishPinnedTransaction(cleanup)
}

// Called with mu held, and only after SQLEndTran succeeded. Never switch to
// autocommit before that: ODBC would commit any pending transaction implicitly.
// Pinned reuse is opt-in for one exclusive sql.Conn, never idle-pool reuse.
func (c *odbcConnection) finishPinnedTransaction(ctx context.Context) error {
	if !c.pinnedSession || c.poisoned {
		c.poisoned = true
		c.publish("poisoned")
		return nil
	}
	_, e := joinODBC(ctx, nil, func() { c.owner.markDraining(c.ownerID) }, func() (any, error) {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		if e := odbcConnectAttribute(c.handle, odbcAutocommitAttribute, odbcAutocommitOn); e != nil {
			return nil, e
		}
		effective, e := odbcGetConnectAttribute(c.handle, odbcAutocommitAttribute)
		if e != nil {
			return nil, e
		}
		if effective != odbcAutocommitOn {
			return nil, odbcError("DAMENG_ODBC_AUTOCOMMIT_SUBSTITUTED")
		}
		return nil, nil
	})
	if e == nil {
		e = ctx.Err()
	}
	if e != nil {
		c.poisoned = true
		c.publish("poisoned")
		return e
	}
	c.publish("ready")
	return nil
}

type odbcStatement struct {
	mu         sync.Mutex
	connection *odbcConnection
	query      string
	closed     bool
}

func (s *odbcStatement) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.query = ""
	return nil
}
func (s *odbcStatement) NumInput() int { return -1 }
func (s *odbcStatement) CheckNamedValue(v *driver.NamedValue) error {
	return s.connection.CheckNamedValue(v)
}
func (s *odbcStatement) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, odbcError("DAMENG_ODBC_STATEMENT_CLOSED")
	}
	return s.connection.ExecContext(ctx, s.query, args)
}
func (s *odbcStatement) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, odbcError("DAMENG_ODBC_STATEMENT_CLOSED")
	}
	return s.connection.QueryContext(ctx, s.query, args)
}
func (s *odbcStatement) Exec([]driver.Value) (driver.Result, error) {
	return nil, odbcError("DAMENG_ODBC_CONTEXT_REQUIRED")
}
func (s *odbcStatement) Query([]driver.Value) (driver.Rows, error) {
	return nil, odbcError("DAMENG_ODBC_CONTEXT_REQUIRED")
}

type odbcRows struct {
	mu      sync.Mutex
	ctx     context.Context
	columns []string
	rows    [][]driver.Value
	index   int
	closed  bool
	memory  *odbcMemoryLease
}

func (r *odbcRows) Columns() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.columns...)
}
func (r *odbcRows) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.rows = nil
	r.columns = nil
	r.memory.close()
	r.memory = nil
	return nil
}
func (r *odbcRows) Next(dest []driver.Value) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	clear(dest)
	if e := r.ctx.Err(); e != nil {
		r.rows = nil
		r.closed = true
		r.columns = nil
		r.memory.close()
		r.memory = nil
		return e
	}
	if r.closed || r.index == len(r.rows) {
		return io.EOF
	}
	if len(dest) != len(r.columns) {
		return odbcError("DAMENG_ODBC_DESTINATION_INVALID")
	}
	copy(dest, r.rows[r.index])
	r.rows[r.index] = nil
	r.index++
	if e := r.ctx.Err(); e != nil {
		clear(dest)
		r.rows = nil
		r.closed = true
		r.columns = nil
		r.memory.close()
		r.memory = nil
		return e
	}
	return nil
}

// ODBC permits -1 when the affected count is unknown. Never convert it to zero
// or invent a successful row count; callers needing exact mutation counts fail.
type odbcResult struct{ affected int64 }

func (odbcResult) LastInsertId() (int64, error) {
	return 0, odbcError("DAMENG_ODBC_INSERT_ID_UNSUPPORTED")
}
func (r odbcResult) RowsAffected() (int64, error) {
	if r.affected < 0 {
		return 0, odbcError("DAMENG_ODBC_AFFECTED_COUNT_UNKNOWN")
	}
	return r.affected, nil
}
