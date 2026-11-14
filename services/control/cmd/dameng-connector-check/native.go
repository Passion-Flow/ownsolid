//go:build cgo && odbc_manager

package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/dameng"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func checkNative(c dameng.ConnectionConfiguration, mutate bool) (r result) {
	r = result{FormatVersion: 1, Checks: []string{}, CleanupState: "not_created"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.OperationSeconds)*time.Second)
	s, observation, e := dameng.Open(ctx, c)
	cancel()
	if s != nil {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer cancel()
			status, err := s.Close(ctx)
			r.CleanupState = status.State
			if err != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 {
				r.Success = false
				if r.Code != string(transaction.CommitUnknown) {
					r.Code = "DAMENG_CHECK_CLEANUP_PENDING"
				}
			}
		}()
	}
	if e != nil {
		r.Code = checkCode(e)
		return r
	}
	r.Observation = observation
	r.Checks = append(r.Checks, "native_identity_and_local_session")
	repository, e := transaction.FromDameng(s)
	if e != nil {
		r.Code = checkCode(e)
		return r
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Duration(c.TransactionSeconds)*time.Second)
	e = repository.Within(ctx, true, func(ctx context.Context, tx transaction.Executor) error {
		var value int64
		if e := tx.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT 1 FROM DUAL")).Scan(&value); e != nil {
			return e
		}
		if value != 1 {
			return errors.New("check mismatch")
		}
		return nil
	})
	cancel()
	if e != nil {
		r.Code = checkCode(e)
		return r
	}
	r.Checks = append(r.Checks, "read_only_transaction")
	if mutate {
		if e = transactionProbe(c, repository, &r); e != nil {
			r.Code = checkCode(e)
			return r
		}
	}
	r.Success = true
	return r
}

// Only this explicit option creates data. The generated identifier is recorded
// until verified DROP; an unknown commit suppresses DROP and all further writes.
func transactionProbe(c dameng.ConnectionConfiguration, repository transaction.Store, r *result) (result error) {
	var random [12]byte
	if _, e := rand.Read(random[:]); e != nil {
		return e
	}
	table := "P1_CONNECTOR_" + strings.ToUpper(hex.EncodeToString(random[:]))
	query := func(sql string) transaction.Statement { return transaction.SQL(transaction.Dameng, sql) }
	operation := func(fn func(context.Context) error) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.OperationSeconds)*time.Second)
		defer cancel()
		return fn(ctx)
	}
	r.RetainedTable = table
	if e := operation(func(ctx context.Context) error {
		_, e := repository.Exec(ctx, query("CREATE TABLE "+table+" (ID INTEGER PRIMARY KEY, LABEL VARCHAR(128), ENABLED BIT, PAYLOAD VARBINARY(32))"))
		return e
	}); e != nil {
		return e
	}
	unknown := false
	defer func() {
		if unknown {
			return
		}
		e := operation(func(ctx context.Context) error { _, e := repository.Exec(ctx, query("DROP TABLE "+table)); return e })
		if e == nil {
			r.RetainedTable = ""
		} else {
			result = errors.Join(result, e)
		}
	}()
	within := func(readOnly bool, fn func(context.Context, transaction.Executor) error) error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.TransactionSeconds)*time.Second)
		defer cancel()
		e := repository.Within(ctx, readOnly, fn)
		if kind, ok := transaction.CommitOutcome(e); ok && kind == transaction.CommitUnknown {
			unknown = true
		}
		return e
	}
	if e := within(false, func(ctx context.Context, tx transaction.Executor) error {
		_, e := tx.Exec(ctx, transaction.SQL(transaction.Dameng, "INSERT INTO "+table+" (ID,LABEL,ENABLED,PAYLOAD) VALUES (?,?,?,?)", int64(1), "\u8fbe\u68a6 transaction", true, []byte{}))
		return e
	}); e != nil {
		return e
	}
	if e := operation(func(ctx context.Context) error {
		var label string
		var enabled bool
		var payload []byte
		if e := repository.QueryRow(ctx, query("SELECT LABEL,ENABLED,PAYLOAD FROM "+table+" WHERE ID=1")).Scan(&label, &enabled, &payload); e != nil {
			return e
		}
		if label != "\u8fbe\u68a6 transaction" || !enabled || payload == nil || len(payload) != 0 {
			return errors.New("round trip mismatch")
		}
		return nil
	}); e != nil {
		return e
	}
	r.Checks = append(r.Checks, "commit_and_unicode_bit_empty_binary_readback")
	rollback := errors.New("connector requested rollback")
	e := within(false, func(ctx context.Context, tx transaction.Executor) error {
		if _, e := tx.Exec(ctx, transaction.SQL(transaction.Dameng, "INSERT INTO "+table+" (ID,LABEL,ENABLED,PAYLOAD) VALUES (?,?,?,?)", int64(2), "rollback", nil, []byte(nil))); e != nil {
			return e
		}
		return rollback
	})
	if e != rollback {
		return eOrMismatch(e)
	}
	count := func(id int64) (int64, error) {
		var n int64
		e := operation(func(ctx context.Context) error {
			return repository.QueryRow(ctx, transaction.SQL(transaction.Dameng, "SELECT COUNT(*) FROM "+table+" WHERE ID=?", id)).Scan(&n)
		})
		return n, e
	}
	if n, e := count(2); e != nil || n != 0 {
		return eOrMismatch(e)
	}
	r.Checks = append(r.Checks, "rollback_readback")
	denied := false
	e = within(true, func(ctx context.Context, tx transaction.Executor) error {
		_, e := tx.Exec(ctx, transaction.SQL(transaction.Dameng, "INSERT INTO "+table+" (ID) VALUES (?)", int64(3)))
		var failure *transaction.Error
		if errors.As(e, &failure) && failure.SQLState == "25006" {
			denied = true
			return rollback
		}
		return eOrMismatch(e)
	})
	if !denied || e != rollback {
		return eOrMismatch(e)
	}
	if n, e := count(3); e != nil || n != 0 {
		return eOrMismatch(e)
	}
	r.Checks = append(r.Checks, "native_read_only_write_refusal")
	return nil
}
func eOrMismatch(e error) error {
	if e != nil {
		return e
	}
	return errors.New("connector result mismatch")
}
func checkCode(e error) string {
	if kind, ok := transaction.CommitOutcome(e); ok {
		return string(kind)
	}
	return dameng.FailureCode(e)
}
