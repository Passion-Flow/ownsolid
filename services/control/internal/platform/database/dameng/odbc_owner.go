//go:build cgo && odbc_manager

package dameng

import (
	"context"
	"database/sql"
	"sort"
	"sync"
)

// ODBCManager owns physical resources independently from database/sql logical
// slots. The caller must retain this object until Close reports state closed.
type ODBCManager struct {
	mu                                                     sync.Mutex
	cfg                                                    ODBCConfig
	connection                                             *connectionBinding
	pool                                                   *sql.DB
	cleanup                                                odbcCleanupCalls
	records                                                map[uint64]*odbcOwnerRecord
	nextID                                                 uint64
	closing                                                bool
	poolCloseStarted, poolCloseFinished, poolClosePanicked bool
	poolCloseDone                                          chan struct{}
	lastPoolCloseError                                     string
	cleanupJobs                                            int
	retainedBytes, memoryLeases                            int
}

func (*ODBCManager) String() string   { return "DAMENG_ODBC_MANAGER_REDACTED" }
func (*ODBCManager) GoString() string { return "DAMENG_ODBC_MANAGER_REDACTED" }

type ODBCPhysicalStatus struct {
	OwnerID               uint64 `json:"owner_id"`
	State                 string `json:"state"`
	EnvironmentAssigned   bool   `json:"environment_assigned"`
	ConnectionAssigned    bool   `json:"connection_assigned"`
	StatementReservations int    `json:"statement_reservations"`
	StatementsAssigned    int    `json:"statements_assigned"`
	BindingReservations   int    `json:"binding_reservations"`
	Retired               bool   `json:"retired"`
	Poisoned              bool   `json:"poisoned"`
	CleanupPending        bool   `json:"cleanup_pending"`
	NativeCleanupActive   bool   `json:"native_cleanup_active"`
	LastCleanupError      string `json:"last_cleanup_error,omitempty"`
}
type ODBCManagerStatus struct {
	State              string               `json:"state"`
	RetainedBytes      int                  `json:"retained_bytes"`
	MemoryLeases       int                  `json:"memory_leases"`
	RetainedLimit      int                  `json:"retained_limit"`
	PhysicalLimit      int                  `json:"physical_limit"`
	PhysicalOwners     int                  `json:"physical_owners"`
	CleanupJobs        int                  `json:"cleanup_jobs"`
	PoolCloseStarted   bool                 `json:"pool_close_started"`
	PoolCloseFinished  bool                 `json:"pool_close_finished"`
	LastPoolCloseError string               `json:"last_pool_close_error,omitempty"`
	Connections        []ODBCPhysicalStatus `json:"connections"`
}
type odbcOwnerRecord struct {
	connection   *odbcConnection
	fact         ODBCPhysicalStatus
	pending      bool
	done         chan struct{}
	nativeActive bool
	nativeDone   chan struct{}
	released     bool
}

// This port covers cleanup only. Production construction always fixes it to the
// real standard calls below. Fault implementations exist only in explicit tests.
type odbcCleanupCalls interface {
	free(int, odbcHandle) error
	reset(odbcHandle) error
	disconnect(odbcHandle) error
	rollback(odbcHandle) error
}
type odbcStandardCleanup struct{}

func (odbcStandardCleanup) free(kind int, h odbcHandle) error { return odbcFree(kind, h) }
func (odbcStandardCleanup) reset(h odbcHandle) error          { return odbcResetParameters(h) }
func (odbcStandardCleanup) disconnect(h odbcHandle) error     { return odbcDisconnect(h) }
func (odbcStandardCleanup) rollback(h odbcHandle) error       { return odbcEndTransaction(h, false) }

// NewODBCManagerForValidation creates no native handles and grants no DM/security
// admission. The explicit Manager, not the borrowed sql.DB, owns lifecycle state.
func NewODBCManagerForValidation(cfg ODBCConfig) (*ODBCManager, error) {
	if e := cfg.Validate(); e != nil {
		return nil, e
	}
	return newODBCManager(cfg, nil), nil
}
func newODBCManager(cfg ODBCConfig, connection *connectionBinding) *ODBCManager {
	m := &ODBCManager{cfg: cfg, connection: connection, cleanup: odbcStandardCleanup{}, records: make(map[uint64]*odbcOwnerRecord)}
	m.pool = sql.OpenDB(&odbcConnector{manager: m})
	m.pool.SetMaxOpenConns(cfg.MaximumOpenConnections)
	m.pool.SetMaxIdleConns(MaximumIdleConnections)
	return m
}
func (m *ODBCManager) BorrowPool() (*sql.DB, error) {
	if m == nil {
		return nil, odbcError("DAMENG_ODBC_MANAGER_REQUIRED")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing || m.pool == nil {
		return nil, odbcError("DAMENG_ODBC_MANAGER_CLOSING")
	}
	return m.pool, nil
}
func (m *ODBCManager) accepting() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return !m.closing
}
func (m *ODBCManager) reserve() (*odbcConnection, error) {
	if m == nil {
		return nil, odbcError("DAMENG_ODBC_MANAGER_REQUIRED")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, odbcError("DAMENG_ODBC_MANAGER_CLOSING")
	}
	if len(m.records) >= m.cfg.MaximumPhysicalConnections {
		return nil, odbcError("DAMENG_ODBC_PHYSICAL_BUDGET_EXHAUSTED")
	}
	if m.nextID == ^uint64(0) {
		return nil, odbcError("DAMENG_ODBC_OWNER_ID_EXHAUSTED")
	}
	m.nextID++
	c := &odbcConnection{owner: m, ownerID: m.nextID, cfg: m.cfg, statements: make(map[*odbcNativeStatement]struct{})}
	m.records[c.ownerID] = &odbcOwnerRecord{connection: c, fact: ODBCPhysicalStatus{OwnerID: c.ownerID, State: "reserved"}}
	return c, nil
}

// publish is called only while connection.mu owns native fields. Manager never
// takes that mutex while holding its registry lock; Status uses copied metadata.
func (c *odbcConnection) publish(state string) {
	fact := ODBCPhysicalStatus{OwnerID: c.ownerID, State: state, EnvironmentAssigned: c.environment != nil, ConnectionAssigned: c.handle != nil, StatementReservations: len(c.statements), Retired: c.retired, Poisoned: c.poisoned}
	for statement := range c.statements {
		if statement.handle != nil {
			fact.StatementsAssigned++
		}
		if statement.parameters != nil {
			fact.BindingReservations++
		}
	}
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if record := c.owner.records[c.ownerID]; record != nil {
		fact.LastCleanupError = record.fact.LastCleanupError
		if record.fact.State == "draining" && state != "retired" && state != "retained" && state != "cleanup" {
			fact.State = "draining"
		}
		record.fact = fact
	}
}
func (m *ODBCManager) markDraining(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.records[id]; r != nil {
		r.fact.State = "draining"
	}
}
func (c *odbcConnection) cleanupFailure() {
	c.poisoned = true
	c.retired = true
	c.closed = true
	c.publish("retained")
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if r := c.owner.records[c.ownerID]; r != nil {
		r.fact.LastCleanupError = "DAMENG_ODBC_NATIVE_RESOURCES_RETAINED"
	}
}
func (c *odbcConnection) releaseOwner() error {
	// Keep even a fully freed entry until its tracked cleanup completion is joined.
	// This bounds both physical reservations and finite cleanup goroutines.
	if c.environment != nil || c.handle != nil || len(c.statements) != 0 {
		return odbcRetained()
	}
	c.released = true
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	if record := c.owner.records[c.ownerID]; record != nil {
		record.released = true
		record.fact.State = "released_pending_join"
		if !record.pending && !record.nativeActive {
			delete(c.owner.records, c.ownerID)
		}
	}
	return nil
}

func (m *ODBCManager) Status() ODBCManagerStatus {
	if m == nil {
		return ODBCManagerStatus{State: "unavailable", Connections: []ODBCPhysicalStatus{}}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := "open"
	if m.closing {
		state = "closing"
	}
	if m.poolCloseFinished && len(m.records) == 0 && m.cleanupJobs == 0 && m.retainedBytes == 0 && m.memoryLeases == 0 {
		state = "closed"
	}
	if m.poolClosePanicked {
		state = "pool_close_failed"
	}
	out := ODBCManagerStatus{State: state, RetainedBytes: m.retainedBytes, MemoryLeases: m.memoryLeases, RetainedLimit: m.cfg.retainedLimit(), PhysicalLimit: m.cfg.MaximumPhysicalConnections, PhysicalOwners: len(m.records), CleanupJobs: m.cleanupJobs, PoolCloseStarted: m.poolCloseStarted, PoolCloseFinished: m.poolCloseFinished, LastPoolCloseError: m.lastPoolCloseError, Connections: make([]ODBCPhysicalStatus, 0, len(m.records))}
	for _, r := range m.records {
		fact := r.fact
		fact.CleanupPending = r.pending
		fact.NativeCleanupActive = r.nativeActive
		out.Connections = append(out.Connections, fact)
	}
	sort.Slice(out.Connections, func(i, j int) bool { return out.Connections[i].OwnerID < out.Connections[j].OwnerID })
	return out
}
func (c *odbcConnection) beginNativeCleanup() func() {
	m := c.owner
	m.mu.Lock()
	r := m.records[c.ownerID]
	done := make(chan struct{})
	if r != nil {
		r.nativeActive = true
		r.nativeDone = done
		r.fact.State = "cleanup"
	}
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		if current := m.records[c.ownerID]; current != nil {
			current.nativeActive = false
			if current.released && !current.pending {
				delete(m.records, c.ownerID)
			}
		}
		close(done)
		m.mu.Unlock()
	}
}
func (m *ODBCManager) startCleanup(id uint64) <-chan struct{} {
	m.mu.Lock()
	r := m.records[id]
	if r == nil {
		m.mu.Unlock()
		return nil
	}
	if r.pending {
		done := r.done
		m.mu.Unlock()
		return done
	}
	if r.nativeActive {
		done := r.nativeDone
		m.mu.Unlock()
		return done
	}
	if !r.fact.Retired && !r.fact.Poisoned {
		m.mu.Unlock()
		return nil
	}
	done := make(chan struct{})
	r.pending = true
	r.done = done
	m.cleanupJobs++
	c := r.connection
	m.mu.Unlock()
	// At most one finite tracked job per physical entry; no retry loop or daemon.
	go func() {
		defer func() {
			if recover() != nil {
				m.mu.Lock()
				if current := m.records[id]; current != nil {
					current.fact.State = "retained"
					current.fact.LastCleanupError = "DAMENG_ODBC_NATIVE_RESOURCES_RETAINED"
				}
				m.mu.Unlock()
			}
			m.mu.Lock()
			if current := m.records[id]; current != nil {
				current.pending = false
				if current.released && !current.nativeActive {
					delete(m.records, id)
				}
			}
			m.cleanupJobs--
			close(done)
			m.mu.Unlock()
		}()
		c.mu.Lock()
		defer c.mu.Unlock()
		c.closed = true
		c.retired = true
		c.publish("retired")
		_ = c.closeNative()
	}()
	return done
}
func (m *ODBCManager) RetryCleanup(ctx context.Context) (ODBCManagerStatus, error) {
	if m == nil {
		return ODBCManagerStatus{}, odbcError("DAMENG_ODBC_MANAGER_REQUIRED")
	}
	if e := odbcContext(ctx); e != nil {
		return m.Status(), e
	}
	m.mu.Lock()
	ids := make([]uint64, 0, len(m.records))
	for id := range m.records {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	waits := make([]<-chan struct{}, 0, len(ids))
	for _, id := range ids {
		if e := ctx.Err(); e != nil {
			return m.Status(), e
		}
		if done := m.startCleanup(id); done != nil {
			waits = append(waits, done)
		}
	}
	for _, done := range waits {
		select {
		case <-done:
		case <-ctx.Done():
			return m.Status(), ctx.Err()
		}
	}
	status := m.Status()
	for _, entry := range status.Connections {
		if entry.Retired || entry.Poisoned || entry.State == "draining" || entry.NativeCleanupActive || entry.CleanupPending {
			return status, odbcError("DAMENG_ODBC_NATIVE_RESOURCES_RETAINED")
		}
	}
	return status, nil
}
func (m *ODBCManager) startPoolClose() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closing = true
	if m.poolCloseStarted {
		return m.poolCloseDone
	}
	m.poolCloseStarted = true
	m.poolCloseDone = make(chan struct{})
	done := m.poolCloseDone
	pool := m.pool
	go func() {
		panicked := true
		var result error
		defer func() {
			_ = recover()
			m.mu.Lock()
			m.poolCloseFinished = true
			m.poolClosePanicked = panicked
			if panicked {
				m.lastPoolCloseError = "DAMENG_ODBC_POOL_CLOSE_PANIC"
			} else if result != nil {
				m.lastPoolCloseError = "DAMENG_ODBC_POOL_CLOSE_REPORTED_FAILURE"
			}
			close(done)
			m.mu.Unlock()
		}()
		if pool != nil {
			result = pool.Close()
		}
		panicked = false
	}()
	return done
}
func (m *ODBCManager) Close(ctx context.Context) (ODBCManagerStatus, error) {
	if m == nil {
		return ODBCManagerStatus{}, odbcError("DAMENG_ODBC_MANAGER_REQUIRED")
	}
	if e := odbcContext(ctx); e != nil {
		return m.Status(), e
	}
	done := m.startPoolClose()
	select {
	case <-done:
	case <-ctx.Done():
		return m.Status(), ctx.Err()
	}
	status, e := m.RetryCleanup(ctx)
	if e != nil {
		return status, e
	}
	if status.State == "pool_close_failed" {
		return status, odbcError("DAMENG_ODBC_POOL_CLOSE_PANIC")
	}
	if status.PhysicalOwners != 0 || status.CleanupJobs != 0 || status.MemoryLeases != 0 || status.RetainedBytes != 0 {
		return status, odbcError("DAMENG_ODBC_NATIVE_OWNERS_PENDING")
	}
	return status, nil
}

// A lease is independent of the physical connection: eager result ownership can
// survive statement disposal. All operations take only the Manager mutex; there
// is no registry-to-connection lock inversion or finalizer.
type odbcMemoryLease struct {
	manager *ODBCManager
	bytes   int
	closed  bool
}

func (m *ODBCManager) memoryLease() (*odbcMemoryLease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return nil, odbcError("DAMENG_ODBC_MANAGER_CLOSING")
	}
	m.memoryLeases++
	return &odbcMemoryLease{manager: m}, nil
}
func (l *odbcMemoryLease) grow(n int) error {
	if l == nil {
		return nil
	} // Unmanaged buffer helpers used only by explicit C boundary tests.
	m := l.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if l.closed || n < 0 || n > m.cfg.retainedLimit()-m.retainedBytes {
		return odbcError("DAMENG_ODBC_MEMORY_BUDGET_EXCEEDED")
	}
	l.bytes += n
	m.retainedBytes += n
	return nil
}
func (l *odbcMemoryLease) shrink(n int) {
	if l == nil {
		return
	}
	m := l.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !l.closed && n >= 0 && n <= l.bytes {
		l.bytes -= n
		m.retainedBytes -= n
	}
}
func (l *odbcMemoryLease) close() {
	if l == nil {
		return
	}
	m := l.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if !l.closed {
		m.retainedBytes -= l.bytes
		m.memoryLeases--
		l.bytes = 0
		l.closed = true
	}
}
