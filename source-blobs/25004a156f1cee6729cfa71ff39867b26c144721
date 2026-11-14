//go:build cgo && odbc_manager

package dameng

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync/atomic"
	"time"
)

type Observation struct {
	FormatVersion        int    `json:"format_version"`
	Transport            string `json:"transport"`
	Architecture         string `json:"architecture"`
	NativeManifestSHA256 string `json:"native_manifest_sha256"`
	DriverVersion        string `json:"driver_version"`
	DBMSName             string `json:"dbms_name"`
	DBMSVersion          string `json:"dbms_version"`
}

// Store owns its manager even when Open returns a non-nil error. The caller must
// retain every returned Store until Close reports closed, including failed opens.
type Store struct {
	manager       *ODBCManager
	configuration ConnectionConfiguration
	admitted      atomic.Bool
}

func (*Store) String() string   { return "DAMENG_STORE_REDACTED" }
func (*Store) GoString() string { return "DAMENG_STORE_REDACTED" }

func Open(ctx context.Context, c ConnectionConfiguration) (*Store, Observation, error) {
	var observation Observation
	if e := odbcContext(ctx); e != nil {
		return nil, observation, SafeError(e)
	}
	if e := c.Validate(); e != nil {
		return nil, observation, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.OperationSeconds)*time.Second)
	defer cancel()
	b, e := bindConnectionInputs(ctx, c)
	if e != nil {
		return nil, observation, e
	}
	s := &Store{manager: newODBCManager(c.Policy.native(), b), configuration: c}
	p, e := s.manager.BorrowPool()
	if e != nil {
		return s, observation, SafeError(e)
	}
	connection, e := p.Conn(ctx)
	if e != nil {
		return s, observation, SafeError(e)
	}
	e = connection.Close()
	status := s.manager.Status()
	if e != nil || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
		return s, observation, &Error{Kind: Unavailable}
	}
	if e = ctx.Err(); e != nil {
		return s, observation, SafeError(e)
	}
	s.admitted.Store(true)
	observation = Observation{1, LocalSocketTransport, b.manifest.Architecture, b.manifestDigest, b.manifest.DriverVersion, b.manifest.DBMSName, b.manifest.DBMSVersion}
	return s, observation, nil
}

// BorrowPool grants no lifecycle authority. Every physical connection repeats
// native identity, session, pinned-artifact and local-socket checks. Idle reuse
// remains disabled. SessionStore reuse is an explicit, exclusively pinned loan;
// it does not enable idle pool reuse.
func (s *Store) BorrowPool() (*sql.DB, error) {
	if s == nil || !s.admitted.Load() {
		return nil, &Error{Kind: Unavailable}
	}
	return s.manager.BorrowPool()
}
func (s *Store) OperationContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if s == nil {
		return nil, nil, &Error{Kind: Unavailable}
	}
	if e := odbcContext(ctx); e != nil {
		return nil, nil, SafeError(e)
	}
	if _, e := s.BorrowPool(); e != nil {
		return nil, nil, SafeError(e)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.OperationSeconds)*time.Second)
	return bounded, cancel, nil
}
func (s *Store) Status() ODBCManagerStatus {
	if s == nil {
		return (*ODBCManager)(nil).Status()
	}
	return s.manager.Status()
}

// SameSchemaTarget compares immutable bindings, including the original socket
// inode and native manifest. It reveals no path/secret and grants no SQL rights.
func (s *Store) SameSchemaTarget(other *Store) bool {
	if s == nil || other == nil || !s.admitted.Load() || !other.admitted.Load() || s.manager == nil || other.manager == nil || s.manager.connection == nil || other.manager.connection == nil {
		return false
	}
	a, b := s.manager.connection, other.manager.connection
	if a.socket == nil || b.socket == nil {
		return false
	}
	return a.configuration.Schema == b.configuration.Schema && a.configuration.SocketFile == b.configuration.SocketFile && a.configuration.SocketOwnerUID == b.configuration.SocketOwnerUID && a.manifestDigest == b.manifestDigest && os.SameFile(a.socket, b.socket)
}

// ValidateSchemaUse requires operator limits to fit the actual admitted native
// policy before a migration session or grant operation can allocate work.
func (s *Store) ValidateSchemaUse(c SchemaConfiguration, runtime bool, maximumSQLBytes int) error {
	if s == nil || !s.admitted.Load() || c.Validate() != nil {
		return fail("DAMENG_SCHEMA_CONNECTION_INVALID")
	}
	principal := c.Schema
	if runtime {
		principal = c.RuntimeUser
	}
	p := s.configuration.Policy
	if s.configuration.Schema != c.Schema || s.configuration.User != principal || p.MaximumRows != c.MaximumMetadataRows || p.MaximumCellBytes != c.MaximumMetadataBytes || p.MaximumResultBytes < c.MaximumMetadataBytes || p.MaximumColumns < 10 || p.MaximumSQLBytes < maximumSQLBytes || s.configuration.OperationSeconds < c.StepSeconds || p.QuerySeconds < c.StepSeconds || p.ConnectionSeconds < c.StepSeconds {
		return fail("DAMENG_SCHEMA_CONNECTION_POLICY_MISMATCH")
	}
	return nil
}
func (s *Store) Close(ctx context.Context) (ODBCManagerStatus, error) {
	if s == nil {
		return s.Status(), &Error{Kind: Unavailable}
	}
	s.admitted.Store(false)
	if e := odbcContext(ctx); e != nil {
		return s.Status(), SafeError(e)
	}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.CleanupSeconds)*time.Second)
	defer cancel()
	status, e := s.manager.Close(bounded)
	return status, SafeError(e)
}
func (s *Store) Within(ctx context.Context, readOnly bool, operation func(context.Context, *sql.Tx) error) (result error) {
	if operation == nil {
		return fail("DAMENG_TRANSACTION_OPERATION_REQUIRED")
	}
	if e := odbcContext(ctx); e != nil {
		return SafeError(e)
	}
	p, e := s.BorrowPool()
	if e != nil {
		return SafeError(e)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.configuration.TransactionSeconds)*time.Second)
	defer cancel()
	tx, e := p.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: readOnly})
	if e != nil {
		return SafeError(e)
	}
	defer tx.Rollback()
	if e = operation(ctx, tx); e != nil {
		rollback := tx.Rollback()
		if rollback != nil && rollback != sql.ErrTxDone {
			return errors.Join(e, SafeError(rollback))
		}
		return e
	}
	// Commit errors are classified before observing a concurrently cancelled ctx.
	return SafeError(tx.Commit())
}

// Capacity exposes only immutable admitted limits for repository page admission.
func (s *Store) Capacity() (ODBCCapacity, error) {
	if _, e := s.BorrowPool(); e != nil {
		return ODBCCapacity{}, SafeError(e)
	}
	return s.configuration.Policy.native().Capacity(), nil
}
