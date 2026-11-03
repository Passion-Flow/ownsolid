package damengbackup

import (
	"context"
	"database/sql"
	"encoding/binary"
	"hash"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

type nativeQuery interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type nativeColumn struct {
	Name, Type string
	Nullable   bool
}
type nativeTable struct {
	Name    string
	Columns []nativeColumn
	Keys    []string
}

func nativeQuote(name string) string { return `"` + name + `"` }
func quotedNames(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = nativeQuote(n)
	}
	return strings.Join(out, ",")
}

// rowStream hashes typed lengths and bytes, never lossy JSON/string conversion.
// LOB chunk boundaries are not hashed, so different admitted chunk sizes produce
// the same digest for the same complete canonical data.
type rowStream struct {
	digest      hash.Hash
	bytes, rows int64
	policy      SnapshotPolicy
}

func newRowStream(p SnapshotPolicy) *rowStream { return &rowStream{digest: newHash(), policy: p} }
func (s *rowStream) put(raw []byte) error {
	if int64(len(raw)) > s.policy.MaximumBytes-s.bytes {
		return fail("SNAPSHOT_BYTE_LIMIT")
	}
	s.bytes += int64(len(raw))
	_, e := s.digest.Write(raw)
	return e
}
func (s *rowStream) number(n int64) error {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], uint64(n))
	return s.put(raw[:])
}
func (s *rowStream) framed(tag byte, raw []byte) error {
	if e := s.put([]byte{tag}); e != nil {
		return e
	}
	if e := s.number(int64(len(raw))); e != nil {
		return e
	}
	return s.put(raw)
}
func (s *rowStream) scalar(kind string, value any) error {
	if value == nil {
		return s.put([]byte{0})
	}
	switch kind {
	case "INTEGER", "INT", "SMALLINT", "BIGINT":
		n, ok := value.(int64)
		if !ok {
			return fail("NATIVE_INTEGER_TYPE_INVALID")
		}
		if e := s.put([]byte{'I'}); e != nil {
			return e
		}
		return s.number(n)
	case "BIT":
		var b bool
		switch v := value.(type) {
		case bool:
			b = v
		case int64:
			if v != 0 && v != 1 {
				return fail("NATIVE_BOOLEAN_TYPE_INVALID")
			}
			b = v == 1
		default:
			return fail("NATIVE_BOOLEAN_TYPE_INVALID")
		}
		v := byte(0)
		if b {
			v = 1
		}
		return s.put([]byte{'B', v})
	case "CHAR", "VARCHAR", "VARCHAR2":
		v, ok := value.(string)
		if !ok || !utf8.ValidString(v) {
			return fail("NATIVE_TEXT_TYPE_INVALID")
		}
		return s.framed('S', []byte(v))
	case "BINARY", "VARBINARY":
		v, ok := value.([]byte)
		if !ok {
			return fail("NATIVE_BINARY_TYPE_INVALID")
		}
		return s.framed('Y', v)
	case "TIMESTAMP", "DATETIME":
		v, ok := value.(time.Time)
		if !ok || v.IsZero() || v.Nanosecond()%1000 != 0 {
			return fail("NATIVE_TIMESTAMP_TYPE_INVALID")
		}
		return s.framed('T', []byte(v.UTC().Format("2006-01-02T15:04:05.000000000Z")))
	default:
		return fail("NATIVE_COLUMN_TYPE_UNSUPPORTED")
	}
}
func (s *rowStream) lob(ctx context.Context, q nativeQuery, table nativeTable, column nativeColumn, keys []any, length any) error {
	if length == nil {
		return s.put([]byte{0})
	}
	n, ok := length.(int64)
	if !ok || n < 0 || n > s.policy.MaximumLOBBytes {
		return fail("NATIVE_LOB_LENGTH_INVALID")
	}
	tag := byte('L')
	if column.Type == "BLOB" {
		tag = 'Z'
	}
	if e := s.put([]byte{tag}); e != nil {
		return e
	}
	if e := s.number(n); e != nil {
		return e
	}
	where := make([]string, len(table.Keys))
	for i, key := range table.Keys {
		where[i] = nativeQuote(key) + "=?"
	}
	query := "SELECT DBMS_LOB.SUBSTR(" + nativeQuote(column.Name) + ",?,?) FROM " + nativeQuote(table.Name) + " WHERE " + strings.Join(where, " AND ")
	var bytes int64
	for offset := int64(1); offset <= n; {
		if e := ctx.Err(); e != nil {
			return fail("SNAPSHOT_CANCELLED")
		}
		amount := min(int64(s.policy.LOBChunkSize), n-offset+1)
		args := append([]any{amount, offset}, keys...)
		var chunk any
		if q.QueryRowContext(ctx, query, args...).Scan(&chunk) != nil {
			return fail("NATIVE_LOB_READ_FAILED")
		}
		var raw []byte
		if column.Type == "BLOB" {
			v, ok := chunk.([]byte)
			if !ok || int64(len(v)) != amount {
				return fail("NATIVE_LOB_CHUNK_INVALID")
			}
			raw = v
		} else {
			v, ok := chunk.(string)
			if !ok || !utf8.ValidString(v) || int64(utf8.RuneCountInString(v)) != amount {
				return fail("NATIVE_LOB_CHUNK_INVALID")
			}
			raw = []byte(v)
		}
		if int64(len(raw)) > s.policy.MaximumLOBBytes-bytes {
			return fail("NATIVE_LOB_BYTE_LIMIT")
		}
		bytes += int64(len(raw))
		if e := s.put(raw); e != nil {
			return e
		}
		offset += amount
	}
	return nil
}

func readNativeTable(ctx context.Context, q nativeQuery, table nativeTable, s *rowStream, excludedAudit ...string) error {
	if !identifier.MatchString(table.Name) || len(table.Columns) < 1 || len(table.Keys) < 1 {
		return fail("NATIVE_TABLE_INVALID")
	}
	positions := map[string]int{}
	projection := make([]string, len(table.Columns))
	for i, c := range table.Columns {
		if !identifier.MatchString(c.Name) {
			return fail("NATIVE_COLUMN_INVALID")
		}
		if _, ok := positions[c.Name]; ok {
			return fail("NATIVE_COLUMN_INVALID")
		}
		positions[c.Name] = i
		projection[i] = nativeQuote(c.Name)
		if c.Type == "BLOB" || c.Type == "CLOB" {
			projection[i] = "CASE WHEN " + nativeQuote(c.Name) + " IS NULL THEN NULL ELSE DBMS_LOB.GETLENGTH(" + nativeQuote(c.Name) + ") END"
		}
	}
	for _, key := range table.Keys {
		i, ok := positions[key]
		if !ok || table.Columns[i].Nullable || table.Columns[i].Type == "CLOB" || table.Columns[i].Type == "BLOB" {
			return fail("NATIVE_PRIMARY_KEY_INVALID")
		}
	}
	filter := ""
	filterArgs := []any{}
	if len(excludedAudit) > 0 && excludedAudit[0] != "" && table.Name == "AUDIT_EVENTS" {
		filter = " WHERE " + nativeQuote("ID") + "<>?"
		filterArgs = append(filterArgs, excludedAudit[0])
	}
	var count int64
	if q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+nativeQuote(table.Name)+filter, filterArgs...).Scan(&count) != nil || count < 0 || count > s.policy.MaximumRows-s.rows {
		return fail("SNAPSHOT_ROW_LIMIT")
	}
	if e := s.framed('R', jsonBytes(table)); e != nil {
		return e
	}
	if e := s.number(count); e != nil {
		return e
	}
	var seen int64
	for offset := int64(0); offset < count; {
		if e := ctx.Err(); e != nil {
			return fail("SNAPSHOT_CANCELLED")
		}
		query := "SELECT " + strings.Join(projection, ",") + " FROM " + nativeQuote(table.Name) + filter + " ORDER BY " + quotedNames(table.Keys) + " OFFSET ? ROWS FETCH NEXT ? ROWS ONLY"
		args := append(append([]any{}, filterArgs...), offset, s.policy.PageRows)
		rows, e := q.QueryContext(ctx, query, args...)
		if e != nil {
			return fail("NATIVE_PAGE_READ_FAILED")
		}
		// Close each native cursor before querying LOB chunks on the same physical
		// session. Only a bounded page is retained, never the entire database.
		page := [][]any{}
		for rows.Next() {
			values := make([]any, len(table.Columns))
			dest := make([]any, len(values))
			for i := range values {
				dest[i] = &values[i]
			}
			if rows.Scan(dest...) != nil {
				rows.Close()
				return fail("NATIVE_PAGE_SCAN_FAILED")
			}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = append([]byte(nil), b...)
				}
			}
			page = append(page, values)
			if len(page) > s.policy.PageRows {
				rows.Close()
				return fail("NATIVE_PAGE_LIMIT")
			}
		}
		e = rows.Err()
		closeError := rows.Close()
		if e != nil || closeError != nil || len(page) == 0 || int64(len(page)) > count-offset {
			return fail("NATIVE_PAGE_INCOMPLETE")
		}
		for _, row := range page {
			keys := make([]any, len(table.Keys))
			for i, key := range table.Keys {
				keys[i] = row[positions[key]]
				if keys[i] == nil {
					return fail("NATIVE_PRIMARY_KEY_INVALID")
				}
			}
			for i, c := range table.Columns {
				if c.Type == "CLOB" || c.Type == "BLOB" {
					e = s.lob(ctx, q, table, c, keys, row[i])
				} else {
					e = s.scalar(c.Type, row[i])
				}
				if e != nil {
					return e
				}
			}
			seen++
			s.rows++
		}
		offset += int64(len(page))
		if offset > math.MaxInt64-int64(s.policy.PageRows) {
			return fail("SNAPSHOT_ROW_LIMIT")
		}
	}
	if seen != count {
		return fail("NATIVE_PAGE_INCOMPLETE")
	}
	return nil
}
