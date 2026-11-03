//go:build cgo && odbc_manager && dameng_long_integration

package dameng

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"
)

// These flags are test-only explicit inputs. The existing owning loader validates
// the protected reference. No ambient credentials, automatic service or retry.
var longTargetConfiguration = flag.String("dameng-long-config", "", "required protected connector format2 file")
var longTargetDisposable = flag.Bool("dameng-long-disposable", false, "explicit permission for one synthetic CLOB/BLOB table in the selected schema")

func TestActualDamengLongValues(t *testing.T) {
	if !*longTargetDisposable {
		t.Fatal("explicit disposable-table opt-in required")
	}
	c, e := LoadConnectionConfiguration(*longTargetConfiguration, "")
	if e != nil || c.FormatVersion != 2 || c.Policy.MaximumLongValueBytes < 2<<20 || c.Policy.MaximumRetainedBytes < 32<<20 || c.Policy.MaximumRows < 4 || c.Policy.MaximumResultBytes < 5<<20 || c.Policy.MaximumColumns < 3 {
		t.Fatal("protected long-value configuration and adequate test capacity required")
	}
	ctx, stop := context.WithTimeout(t.Context(), time.Duration(c.TransactionSeconds*12)*time.Second)
	defer stop()
	store, _, e := Open(ctx, c)
	if store != nil {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Duration(c.CleanupSeconds)*time.Second)
			defer cancel()
			status, e := store.Close(cleanup)
			if e != nil || status.State != "closed" || status.PhysicalOwners != 0 || status.CleanupJobs != 0 || status.MemoryLeases != 0 || status.RetainedBytes != 0 {
				t.Error("native cleanup pending; retain owner and target diagnostics")
			}
		}()
	}
	if e != nil {
		t.Fatal("native target admission failed", SafeError(e))
	}
	pool, e := store.BorrowPool()
	if e != nil {
		t.Fatal(e)
	}
	var random [12]byte
	if _, e = rand.Read(random[:]); e != nil {
		t.Fatal("fixture identity generation failed")
	}
	table := "P1_LOB_" + strings.ToUpper(hex.EncodeToString(random[:]))
	// The generated SQL identifier is synthetic and safe to record for recovery.
	t.Log("disposable table:", table)
	if _, e = pool.ExecContext(ctx, "CREATE TABLE "+table+" (ID BIGINT PRIMARY KEY,TEXT_VALUE CLOB,BINARY_VALUE BLOB)"); e != nil {
		t.Fatal("CREATE outcome failed or unknown; inspect exact fixture before any cleanup", SafeError(e))
	}
	preserve := true
	defer func() {
		if preserve {
			t.Log("fixture retained for outcome reconciliation:", table)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Duration(c.OperationSeconds)*time.Second)
		defer cancel()
		if _, e := pool.ExecContext(cleanup, "DROP TABLE "+table); e != nil {
			t.Error("exact fixture cleanup failed or unknown; no automatic retry", SafeError(e))
		}
	}()
	text := strings.Repeat("A\u4e2d😀", 262144) // Exactly2MiB UTF-8; cross-chunk non-BMP text.
	binary := append(bytes.Repeat([]byte{0, 1, 127, 255}, 262144), byte(7))
	insert := "INSERT INTO " + table + " (ID,TEXT_VALUE,BINARY_VALUE) VALUES (?,?,?)"
	e = store.Within(ctx, false, func(ctx context.Context, tx *sql.Tx) error {
		for _, row := range []struct {
			id           int64
			text, binary any
		}{{1, text, binary}, {2, nil, []byte(nil)}, {3, "", []byte{}}, {4, text, binary}} {
			result, e := tx.ExecContext(ctx, insert, row.id, row.text, row.binary)
			if e != nil {
				return e
			}
			n, e := result.RowsAffected()
			if e != nil {
				return e
			}
			if n != 1 {
				return errors.New("fixture affected-row mismatch")
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal("write outcome failed or unknown; preserve fixture and do not replay", SafeError(e))
	}
	preserve = false // All mutations have definite completion; read failures may clean this owned fixture.
	rows, e := pool.QueryContext(ctx, "SELECT ID,TEXT_VALUE,BINARY_VALUE FROM "+table+" ORDER BY ID")
	if e != nil {
		t.Fatal("native LONG fetch failed", SafeError(e))
	}
	defer rows.Close()
	if s := store.Status(); s.RetainedBytes <= 0 || s.MemoryLeases <= 0 {
		t.Fatal("materialized results escaped manager budget")
	}
	count := 0
	for rows.Next() {
		var id int64
		var gotText sql.NullString
		var gotBinary []byte
		if e := rows.Scan(&id, &gotText, &gotBinary); e != nil {
			t.Fatal("native LONG scan failed", SafeError(e))
		}
		count++
		switch id {
		case 1, 4:
			if !gotText.Valid || len(gotText.String) != len(text) || sha256.Sum256([]byte(gotText.String)) != sha256.Sum256([]byte(text)) || len(gotBinary) != len(binary) || sha256.Sum256(gotBinary) != sha256.Sum256(binary) {
				t.Fatal("native long-value round trip differs")
			}
		case 2:
			if gotText.Valid || gotBinary != nil {
				t.Fatal("SQL NULL changed")
			}
		case 3:
			if !gotText.Valid || gotText.String != "" || gotBinary == nil || len(gotBinary) != 0 {
				t.Fatal("target conflates empty values and NULL")
			}
		default:
			t.Fatal("unexpected synthetic row")
		}
	}
	if e := rows.Err(); e != nil {
		t.Fatal("native LONG rows failed", SafeError(e))
	}
	if e := rows.Close(); e != nil || count != 4 {
		t.Fatal("native LONG rows incomplete")
	}
	if s := store.Status(); s.RetainedBytes != 0 || s.MemoryLeases != 0 {
		t.Fatal("Rows.Close left retained materialization")
	}
	// A definite rollback must leave no fifth row. It is an ordinary transaction,
	// not a simulated commit success or an automatic retry after uncertainty.
	refusal := errors.New("intentional long fixture rollback")
	preserve = true
	e = store.Within(ctx, false, func(ctx context.Context, tx *sql.Tx) error {
		if _, e := tx.ExecContext(ctx, insert, int64(5), text, binary); e != nil {
			return e
		}
		return refusal
	})
	if e != refusal {
		t.Fatal("rollback failed or uncertain; preserve fixture", SafeError(e))
	}
	preserve = false
	var remaining int64
	if e = pool.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE ID=5").Scan(&remaining); e != nil || remaining != 0 {
		t.Fatal("native long rollback readback failed", SafeError(e))
	}
}
