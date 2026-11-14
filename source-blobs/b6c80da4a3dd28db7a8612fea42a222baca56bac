//go:build cgo && odbc_manager

package dameng

/*
#cgo CFLAGS: -std=c11
#cgo LDFLAGS: -lodbc
#include <sql.h>
#include <sqlext.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
_Static_assert(sizeof(void*) == 8, "ODBC_REQUIRES_64_BIT_POINTERS");
_Static_assert(sizeof(SQLLEN) == 8, "ODBC_REQUIRES_64_BIT_SQLLEN");
_Static_assert(sizeof(SQLULEN) == 8, "ODBC_REQUIRES_64_BIT_SQLULEN");
_Static_assert(sizeof(SQLWCHAR) == 2, "ODBC_REQUIRES_UTF16_SQLWCHAR");
typedef struct { SQLCHAR state[6]; SQLINTEGER native; } po_diagnostic;
static po_diagnostic po_diag(SQLSMALLINT kind, SQLHANDLE handle) {
    po_diagnostic d; memset(&d, 0, sizeof(d));
    SQLSMALLINT needed = 0; SQLCHAR unused[1];
    SQLRETURN rc = SQLGetDiagRec(kind, handle, 1, d.state, &d.native, unused, 0, &needed);
    if (rc != SQL_SUCCESS && rc != SQL_SUCCESS_WITH_INFO) memset(&d, 0, sizeof(d));
    return d;
}
static SQLRETURN po_connect_uint(SQLHDBC h, SQLINTEGER attr, SQLUINTEGER value) {
    return SQLSetConnectAttr(h, attr, (SQLPOINTER)(uintptr_t)value, SQL_IS_UINTEGER);
}
static SQLRETURN po_statement_uint(SQLHSTMT h, SQLINTEGER attr, SQLUINTEGER value) {
    return SQLSetStmtAttr(h, attr, (SQLPOINTER)(uintptr_t)value, SQL_IS_UINTEGER);
}
static SQLRETURN po_environment_version(SQLHENV h) {
    return SQLSetEnvAttr(h, SQL_ATTR_ODBC_VERSION, (SQLPOINTER)(uintptr_t)SQL_OV_ODBC3, SQL_IS_UINTEGER);
}
typedef struct {
    SQLPOINTER data; SQLLEN bytes; SQLLEN indicator;
    SQLSMALLINT ctype; SQLSMALLINT sqltype; SQLULEN precision; SQLSMALLINT scale; int stream;
} po_parameter;
_Static_assert(sizeof(po_parameter) <= 64, "ODBC_PARAMETER_DESCRIPTOR_CAPACITY_BOUND");
static po_parameter* po_parameters(size_t n) { return calloc(n ? n : 1, sizeof(po_parameter)); }
static void po_parameters_free(po_parameter* p, size_t n) {
    if (!p) return;
    for (size_t i=0; i<n; ++i) { if (p[i].data && !p[i].stream) { memset(p[i].data, 0, (size_t)p[i].bytes); free(p[i].data); } }
    free(p);
}
static po_parameter* po_parameter_at(po_parameter* p, size_t n) { return &p[n]; }
static SQLRETURN po_bind(SQLHSTMT h, SQLUSMALLINT index, po_parameter* p) {
    return SQLBindParameter(h, index, SQL_PARAM_INPUT, p->ctype, p->sqltype,
        p->precision, p->scale, p->data, p->bytes, &p->indicator);
}
static void po_stream_parameter(po_parameter* p, SQLLEN length) {
    p->stream=1; p->data=(SQLPOINTER)p; p->bytes=0; p->indicator=SQL_LEN_DATA_AT_EXEC(length);
}
static int po_only_truncation(SQLHSTMT h) {
    for (SQLSMALLINT i=1; i<=64; ++i) {
        SQLCHAR state[6]={0}; SQLINTEGER native=0; SQLSMALLINT needed=0; SQLCHAR unused[1];
        SQLRETURN rc=SQLGetDiagRec(SQL_HANDLE_STMT,h,i,state,&native,unused,0,&needed);
        if (rc==SQL_NO_DATA) return i>1;
        if ((rc!=SQL_SUCCESS && rc!=SQL_SUCCESS_WITH_INFO) || memcmp(state,"01004",5)!=0) return 0;
    }
    return 0;
}
static void po_set_timestamp(SQL_TIMESTAMP_STRUCT* t, int year, int month, int day, int hour, int minute, int second, unsigned fraction) {
    memset(t, 0, sizeof(*t)); t->year=year; t->month=month; t->day=day;
    t->hour=hour; t->minute=minute; t->second=second; t->fraction=fraction;
}
*/
import "C"

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"math"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

type odbcHandle unsafe.Pointer
type odbcParameters struct {
	pointer    *C.po_parameter
	count      int
	streams    []any
	chunkBytes int
	transfer   unsafe.Pointer
	memory     *odbcMemoryLease
}
type odbcColumn struct {
	name string
	kind int
}

const odbcEnvironmentHandle = int(C.SQL_HANDLE_ENV)
const odbcConnectionHandle = int(C.SQL_HANDLE_DBC)
const odbcStatementHandle = int(C.SQL_HANDLE_STMT)

// These identifiers come from the admitted native headers, never copied ABI
// numbers. They also let buffer-level checks inspect the actual native contract.
const odbcBooleanCType = int(C.SQL_C_BIT)
const odbcBooleanSQLType = int(C.SQL_BIT)
const odbcBinaryCType = int(C.SQL_C_BINARY)
const odbcBinarySQLType = int(C.SQL_VARBINARY)
const odbcSignedIntegerCType = int(C.SQL_C_SBIGINT)
const odbcNullData = int64(C.SQL_NULL_DATA)
const odbcNoTotal = int64(C.SQL_NO_TOTAL)

func odbcStatus(rc C.SQLRETURN, kind int, h odbcHandle) error {
	if rc == C.SQL_SUCCESS {
		return nil
	}
	code := "DAMENG_ODBC_NATIVE_FAILURE"
	if rc == C.SQL_SUCCESS_WITH_INFO {
		code = "DAMENG_ODBC_NATIVE_WARNING"
	}
	if rc == C.SQL_INVALID_HANDLE {
		return odbcError("DAMENG_ODBC_INVALID_HANDLE")
	}
	d := C.po_diag(C.SQLSMALLINT(kind), C.SQLHANDLE(h))
	state := ""
	for _, b := range d.state[:5] {
		if !(b >= '0' && b <= '9' || b >= 'A' && b <= 'Z') {
			state = ""
			break
		}
		state += string(byte(b))
	}
	return &ODBCError{code: code, sqlState: state, nativeCode: int32(d.native)}
}
func odbcAllocateInto(kind int, parent odbcHandle, owned *odbcHandle) error {
	if owned == nil || *owned != nil {
		return odbcError("DAMENG_ODBC_ALLOCATION_OWNER_REQUIRED")
	}
	var handle C.SQLHANDLE
	rc := C.SQLAllocHandle(C.SQLSMALLINT(kind), C.SQLHANDLE(parent), &handle)
	// Publish to its pre-registered owner BEFORE diagnostics or any error path.
	*owned = odbcHandle(handle)
	if rc != C.SQL_SUCCESS {
		if parent == nil {
			return odbcError("DAMENG_ODBC_HANDLE_ALLOCATION_FAILED")
		}
		parentKind := odbcEnvironmentHandle
		if kind == odbcStatementHandle {
			parentKind = odbcConnectionHandle
		}
		return odbcStatus(rc, parentKind, parent)
	}
	if handle == nil {
		return odbcError("DAMENG_ODBC_HANDLE_REQUIRED")
	}
	return nil
}

func odbcFree(kind int, h odbcHandle) error {
	if h == nil {
		return nil
	}
	return odbcStatus(C.SQLFreeHandle(C.SQLSMALLINT(kind), C.SQLHANDLE(h)), kind, h)
}
func odbcEnvironment(h odbcHandle) error {
	return odbcStatus(C.po_environment_version(C.SQLHENV(h)), odbcEnvironmentHandle, h)
}
func odbcConnectAttribute(h odbcHandle, attribute int, value uint32) error {
	return odbcStatus(C.po_connect_uint(C.SQLHDBC(h), C.SQLINTEGER(attribute), C.SQLUINTEGER(value)), odbcConnectionHandle, h)
}
func odbcGetConnectAttribute(h odbcHandle, attribute int) (uint32, error) {
	var value C.SQLULEN
	rc := C.SQLGetConnectAttr(C.SQLHDBC(h), C.SQLINTEGER(attribute), C.SQLPOINTER(unsafe.Pointer(&value)), C.SQLINTEGER(unsafe.Sizeof(value)), nil)
	if e := odbcStatus(rc, odbcConnectionHandle, h); e != nil {
		return 0, e
	}
	if uint64(value) > math.MaxUint32 {
		return 0, odbcError("DAMENG_ODBC_ATTRIBUTE_INVALID")
	}
	return uint32(value), nil
}
func odbcStatementTimeout(h odbcHandle, seconds int) error {
	if e := odbcStatus(C.po_statement_uint(C.SQLHSTMT(h), C.SQL_ATTR_QUERY_TIMEOUT, C.SQLUINTEGER(seconds)), odbcStatementHandle, h); e != nil {
		return e
	}
	var effective C.SQLULEN
	rc := C.SQLGetStmtAttr(C.SQLHSTMT(h), C.SQL_ATTR_QUERY_TIMEOUT, C.SQLPOINTER(unsafe.Pointer(&effective)), C.SQLINTEGER(unsafe.Sizeof(effective)), nil)
	if e := odbcStatus(rc, odbcStatementHandle, h); e != nil {
		return e
	}
	if uint64(effective) != uint64(seconds) {
		return odbcError("DAMENG_ODBC_QUERY_TIMEOUT_SUBSTITUTED")
	}
	return nil
}
func odbcWideBytes(text string) int {
	n := 2
	for _, r := range text {
		if r > 0xffff {
			n += 4
		} else {
			n += 2
		}
	}
	return n
}
func odbcWide(text string) (unsafe.Pointer, int, error) {
	bytes := odbcWideBytes(text)
	p := C.calloc(C.size_t(bytes), 1)
	if p == nil {
		return nil, 0, odbcError("DAMENG_ODBC_ALLOCATION_FAILED")
	}
	units := unsafe.Slice((*uint16)(p), bytes/2)
	i := 0
	for _, r := range text {
		if r > 0xffff {
			a, b := utf16.EncodeRune(r)
			units[i] = uint16(a)
			units[i+1] = uint16(b)
			i += 2
		} else {
			units[i] = uint16(r)
			i++
		}
	}
	return p, bytes, nil
}
func odbcReadWide(p unsafe.Pointer, bytes int) (string, error) {
	if bytes < 0 || bytes%2 != 0 {
		return "", odbcError("DAMENG_ODBC_UNICODE_INVALID")
	}
	units := unsafe.Slice((*uint16)(p), bytes/2)
	for i := 0; i < len(units); i++ {
		v := units[i]
		if v == 0 {
			return "", odbcError("DAMENG_ODBC_UNICODE_INVALID")
		}
		if v >= 0xd800 && v <= 0xdbff {
			i++
			if i >= len(units) || units[i] < 0xdc00 || units[i] > 0xdfff {
				return "", odbcError("DAMENG_ODBC_UNICODE_INVALID")
			}
		} else if v >= 0xdc00 && v <= 0xdfff {
			return "", odbcError("DAMENG_ODBC_UNICODE_INVALID")
		}
	}
	return string(utf16.Decode(units)), nil
}
func odbcConnect(h odbcHandle, text string) (bool, error) {
	p, bytes, e := odbcWide(text)
	if e != nil {
		return false, e
	}
	defer func() { C.memset(p, 0, C.size_t(bytes)); C.free(p) }()
	rc := C.SQLDriverConnectW(C.SQLHDBC(h), nil, (*C.SQLWCHAR)(p), C.SQL_NTS, nil, 0, nil, C.SQL_DRIVER_NOPROMPT)
	return rc == C.SQL_SUCCESS || rc == C.SQL_SUCCESS_WITH_INFO, odbcStatus(rc, odbcConnectionHandle, h)
}
func odbcDisconnect(h odbcHandle) error {
	return odbcStatus(C.SQLDisconnect(C.SQLHDBC(h)), odbcConnectionHandle, h)
}
func odbcPrepare(h odbcHandle, query string) error {
	p, _, e := odbcWide(query)
	if e != nil {
		return e
	}
	defer C.free(p)
	return odbcStatus(C.SQLPrepareW(C.SQLHSTMT(h), (*C.SQLWCHAR)(p), C.SQL_NTS), odbcStatementHandle, h)
}
func odbcCancel(h odbcHandle) error {
	// Never inspect diagnostic records concurrently with the original operation.
	rc := C.SQLCancel(C.SQLHSTMT(h))
	if rc != C.SQL_SUCCESS {
		return odbcError("DAMENG_ODBC_CANCEL_NOT_CONFIRMED")
	}
	return nil
}
func odbcExecute(h odbcHandle) error {
	rc := C.SQLExecute(C.SQLHSTMT(h))
	if rc == C.SQL_NO_DATA {
		return nil
	}
	return odbcStatus(rc, odbcStatementHandle, h)
}
func odbcEndTransaction(h odbcHandle, commit bool) error {
	completion := C.SQLSMALLINT(C.SQL_ROLLBACK)
	if commit {
		completion = C.SQL_COMMIT
	}
	return odbcStatus(C.SQLEndTran(C.SQL_HANDLE_DBC, C.SQLHANDLE(h), completion), odbcConnectionHandle, h)
}
func odbcRowCount(h odbcHandle) (int64, error) {
	var n C.SQLLEN
	if e := odbcStatus(C.SQLRowCount(C.SQLHSTMT(h), &n), odbcStatementHandle, h); e != nil {
		return 0, e
	}
	if n < -1 {
		return 0, odbcError("DAMENG_ODBC_AFFECTED_COUNT_INVALID")
	}
	return int64(n), nil
}
func (p *odbcParameters) free() {
	C.po_parameters_free(p.pointer, C.size_t(p.count))
	if p.transfer != nil {
		C.memset(p.transfer, 0, C.size_t(p.chunkBytes))
		C.free(p.transfer)
		p.transfer = nil
	}
	p.pointer = nil
	p.count = 0
	p.streams = nil
	p.memory.close()
	p.memory = nil
}
func odbcBuildParameters(args []driver.NamedValue, maximum int) (odbcParameters, error) {
	return odbcBuildParametersOwned(args, ODBCConfig{FormatVersion: 2, MaximumParameterBytes: maximum}, nil)
}
func odbcBuildParametersOwned(args []driver.NamedValue, cfg ODBCConfig, memory *odbcMemoryLease) (out odbcParameters, result error) {
	maximum := cfg.MaximumParameterBytes
	defer func() {
		if result != nil {
			memory.close()
		}
	}()
	if len(args) > 32767 {
		return out, odbcError("DAMENG_ODBC_PARAMETER_COUNT_INVALID")
	}
	descriptors := len(args) * int(C.sizeof_po_parameter)
	if descriptors > maximum {
		return out, odbcError("DAMENG_ODBC_PARAMETER_BUDGET_EXCEEDED")
	}
	if len(args) == 0 {
		return odbcParameters{memory: memory}, nil
	}
	// Reserve Go planning/stream descriptors before creating those slices.
	if e := memory.grow(len(args) * 64); e != nil {
		return out, e
	}
	stream := make([]bool, len(args))
	total := descriptors
	retained := descriptors
	streamCount := 0
	for i, arg := range args {
		if arg.Name != "" || arg.Ordinal != i+1 {
			return out, odbcError("DAMENG_ODBC_PARAMETER_POSITION_INVALID")
		}
		size := 0
		source := 0
		switch v := arg.Value.(type) {
		case nil:
		case string:
			limit := maximum
			if cfg.FormatVersion == 3 {
				limit = cfg.MaximumLongValueBytes
			}
			if !validODBCText(v, limit) {
				return out, odbcError("DAMENG_ODBC_PARAMETER_INVALID")
			}
			size = odbcWideBytes(v)
			source = len(v)
			stream[i] = cfg.FormatVersion == 3 && size > cfg.TransferChunkBytes
			if stream[i] {
				source *= 2
			} // Private clone avoids retaining a larger backing string.
		case []byte:
			if v != nil {
				size = max(1, len(v))
				source = len(v)
			}
			limit := maximum
			if cfg.FormatVersion == 3 {
				limit = cfg.MaximumLongValueBytes
			}
			if len(v) > limit {
				return out, odbcError("DAMENG_ODBC_PARAMETER_INVALID")
			}
			stream[i] = cfg.FormatVersion == 3 && size > cfg.TransferChunkBytes
			if stream[i] {
				source *= 2
			} // Retain a private snapshot, never a mutable caller alias.
		case bool:
			size = int(C.sizeof_SQLCHAR)
		case int64:
			size = 8
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return out, odbcError("DAMENG_ODBC_PARAMETER_INVALID")
			}
			size = 8
		case ODBCDecimal:
			if _, e := Decimal(v.text, v.precision, v.scale); e != nil {
				return out, e
			}
			size = len(v.text) + 1
		case time.Time:
			if !validODBCTime(v) {
				return out, odbcError("DAMENG_ODBC_PARAMETER_INVALID")
			}
			size = int(C.sizeof_SQL_TIMESTAMP_STRUCT)
		default:
			return out, odbcError("DAMENG_ODBC_PARAMETER_TYPE_UNSUPPORTED")
		}
		if stream[i] {
			streamCount++
			size = 0
		}
		if size > maximum-total {
			return out, odbcError("DAMENG_ODBC_PARAMETER_BUDGET_EXCEEDED")
		}
		total += size
		retained += size + source
	}
	if streamCount > 0 {
		if cfg.TransferChunkBytes > maximum-total {
			return out, odbcError("DAMENG_ODBC_PARAMETER_BUDGET_EXCEEDED")
		}
		retained += cfg.TransferChunkBytes
	}
	if e := memory.grow(retained); e != nil {
		return out, e
	}
	out = odbcParameters{pointer: C.po_parameters(C.size_t(len(args))), count: len(args), streams: make([]any, len(args)), chunkBytes: cfg.TransferChunkBytes, memory: memory}
	if out.pointer == nil {
		out.free()
		out = odbcParameters{}
		return out, odbcError("DAMENG_ODBC_ALLOCATION_FAILED")
	}
	valid := false
	defer func() {
		if !valid {
			out.free()
			out = odbcParameters{}
		}
	}()
	for i, arg := range args {
		if arg.Name != "" || arg.Ordinal != i+1 {
			return out, odbcError("DAMENG_ODBC_PARAMETER_POSITION_INVALID")
		}
		p := C.po_parameter_at(out.pointer, C.size_t(i))
		p.ctype = C.SQL_C_WCHAR
		p.sqltype = C.SQL_WVARCHAR
		p.precision = 1
		if arg.Value == nil {
			p.indicator = C.SQL_NULL_DATA
			continue
		}
		if stream[i] {
			var nativeBytes int
			switch v := arg.Value.(type) {
			case string:
				nativeBytes = odbcWideBytes(v) - 2
				p.precision = C.SQLULEN(nativeBytes / 2)
				p.sqltype = C.SQL_WLONGVARCHAR
				out.streams[i] = strings.Clone(v)
			case []byte:
				nativeBytes = len(v)
				p.precision = C.SQLULEN(nativeBytes)
				p.ctype = C.SQL_C_BINARY
				p.sqltype = C.SQL_LONGVARBINARY
				out.streams[i] = append([]byte(nil), v...)
			}
			C.po_stream_parameter(p, C.SQLLEN(nativeBytes))
			continue
		}
		var pointer unsafe.Pointer
		var bytes int
		switch value := arg.Value.(type) {
		case string:
			if !validODBCText(value, maximum) {
				return out, odbcError("DAMENG_ODBC_PARAMETER_INVALID")
			}
			var e error
			pointer, bytes, e = odbcWide(value)
			if e != nil {
				return out, e
			}
			p.precision = C.SQLULEN((bytes - 2) / 2)
			if p.precision == 0 {
				p.precision = 1
			}
			p.indicator = C.SQLLEN(bytes - 2)
		case []byte:
			p.ctype = C.SQL_C_BINARY
			p.sqltype = C.SQL_VARBINARY
			if value == nil {
				p.indicator = C.SQL_NULL_DATA
				continue
			}
			if len(value) > maximum {
				return out, odbcError("DAMENG_ODBC_PARAMETER_INVALID")
			}
			// An empty non-NULL binary value still needs an owned native address.
			// Count that byte in the budget; nonempty binary needs no terminator.
			bytes = max(1, len(value))
			pointer = C.calloc(C.size_t(bytes), 1)
			if pointer != nil {
				copy(unsafe.Slice((*byte)(pointer), len(value)), value)
			}
			p.precision = C.SQLULEN(max(1, len(value)))
			p.indicator = C.SQLLEN(len(value))
		case bool:
			bytes = int(C.sizeof_SQLCHAR)
			pointer = C.calloc(1, C.size_t(bytes))
			if pointer != nil && value {
				*(*C.SQLCHAR)(pointer) = 1
			}
			p.ctype = C.SQL_C_BIT
			p.sqltype = C.SQL_BIT
			p.precision = 1
			p.indicator = C.SQLLEN(bytes)
		case int64:
			bytes = 8
			pointer = C.malloc(8)
			if pointer != nil {
				*(*int64)(pointer) = value
			}
			p.ctype = C.SQL_C_SBIGINT
			p.sqltype = C.SQL_BIGINT
			p.precision = 19
			p.indicator = 8
		case float64:
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return out, odbcError("DAMENG_ODBC_PARAMETER_INVALID")
			}
			bytes = 8
			pointer = C.malloc(8)
			if pointer != nil {
				*(*float64)(pointer) = value
			}
			p.ctype = C.SQL_C_DOUBLE
			p.sqltype = C.SQL_DOUBLE
			p.precision = 15
			p.indicator = 8
		case ODBCDecimal:
			if _, e := Decimal(value.text, value.precision, value.scale); e != nil {
				return out, e
			}
			bytes = len(value.text) + 1
			pointer = C.calloc(C.size_t(bytes), 1)
			if pointer != nil {
				copy(unsafe.Slice((*byte)(pointer), bytes), []byte(value.text))
			}
			p.ctype = C.SQL_C_CHAR
			p.sqltype = C.SQL_DECIMAL
			p.precision = C.SQLULEN(value.precision)
			p.scale = C.SQLSMALLINT(value.scale)
			p.indicator = C.SQLLEN(bytes - 1)
		case time.Time:
			if !validODBCTime(value) {
				return out, odbcError("DAMENG_ODBC_PARAMETER_INVALID")
			}
			bytes = int(C.sizeof_SQL_TIMESTAMP_STRUCT)
			pointer = C.calloc(1, C.size_t(bytes))
			if pointer != nil {
				C.po_set_timestamp((*C.SQL_TIMESTAMP_STRUCT)(pointer), C.int(value.Year()), C.int(value.Month()), C.int(value.Day()), C.int(value.Hour()), C.int(value.Minute()), C.int(value.Second()), C.uint(value.Nanosecond()))
			}
			p.ctype = C.SQL_C_TYPE_TIMESTAMP
			p.sqltype = C.SQL_TYPE_TIMESTAMP
			p.precision = 26
			p.scale = 6
			p.indicator = C.SQLLEN(bytes)
		default:
			return out, odbcError("DAMENG_ODBC_PARAMETER_TYPE_UNSUPPORTED")
		}
		if pointer == nil {
			return out, odbcError("DAMENG_ODBC_ALLOCATION_FAILED")
		}
		p.data = C.SQLPOINTER(pointer)
		p.bytes = C.SQLLEN(bytes)
	}
	if streamCount > 0 {
		out.transfer = C.calloc(C.size_t(cfg.TransferChunkBytes), 1)
		if out.transfer == nil {
			return out, odbcError("DAMENG_ODBC_ALLOCATION_FAILED")
		}
	}
	valid = true
	return out, nil
}
func odbcBindParameters(h odbcHandle, p odbcParameters) error {
	var count C.SQLSMALLINT
	if e := odbcStatus(C.SQLNumParams(C.SQLHSTMT(h), &count), odbcStatementHandle, h); e != nil {
		return e
	}
	if int(count) != p.count {
		return odbcError("DAMENG_ODBC_PARAMETER_COUNT_INVALID")
	}
	for i := 0; i < p.count; i++ {
		if e := odbcStatus(C.po_bind(C.SQLHSTMT(h), C.SQLUSMALLINT(i+1), C.po_parameter_at(p.pointer, C.size_t(i))), odbcStatementHandle, h); e != nil {
			return e
		}
	}
	return nil
}
func odbcResetParameters(h odbcHandle) error {
	return odbcStatus(C.SQLFreeStmt(C.SQLHSTMT(h), C.SQL_RESET_PARAMS), odbcStatementHandle, h)
}
func odbcCloseCursor(h odbcHandle) error {
	return odbcStatus(C.SQLCloseCursor(C.SQLHSTMT(h)), odbcStatementHandle, h)
}

func odbcColumns(h odbcHandle, maximum int) ([]odbcColumn, error) {
	var count C.SQLSMALLINT
	if e := odbcStatus(C.SQLNumResultCols(C.SQLHSTMT(h), &count), odbcStatementHandle, h); e != nil {
		return nil, e
	}
	if count < 1 || int(count) > maximum {
		return nil, odbcError("DAMENG_ODBC_COLUMN_COUNT_INVALID")
	}
	columns := make([]odbcColumn, int(count))
	for i := range columns {
		var name [129]C.SQLWCHAR
		var length, kind, scale, nullable C.SQLSMALLINT
		var size C.SQLULEN
		rc := C.SQLDescribeColW(C.SQLHSTMT(h), C.SQLUSMALLINT(i+1), &name[0], 129, &length, &kind, &size, &scale, &nullable)
		if e := odbcStatus(rc, odbcStatementHandle, h); e != nil {
			return nil, e
		}
		if length < 1 || length > 128 {
			return nil, odbcError("DAMENG_ODBC_COLUMN_NAME_INVALID")
		}
		text, e := odbcReadWide(unsafe.Pointer(&name[0]), int(length)*2)
		if e != nil {
			return nil, e
		}
		columns[i] = odbcColumn{text, int(kind)}
	}
	return columns, nil
}
func odbcFetch(h odbcHandle) (bool, error) {
	rc := C.SQLFetch(C.SQLHSTMT(h))
	if rc == C.SQL_NO_DATA {
		return false, nil
	}
	if e := odbcStatus(rc, odbcStatementHandle, h); e != nil {
		return false, e
	}
	return true, nil
}
func odbcCell(h odbcHandle, column int, kind int, maximum int) (driver.Value, error) {
	b, e := odbcNewReadBuffer(maximum+2, nil)
	if e != nil {
		return nil, e
	}
	defer b.free()
	return odbcCellIn(h, column, kind, maximum, b)
}
func odbcCellIn(h odbcHandle, column int, kind int, maximum int, buffer *odbcReadBuffer) (driver.Value, error) {
	ctype := C.SQLSMALLINT(C.SQL_C_WCHAR)
	bytes := maximum + 2
	switch kind {
	case int(C.SQL_BINARY), int(C.SQL_VARBINARY), int(C.SQL_LONGVARBINARY):
		ctype = C.SQL_C_BINARY
		bytes = maximum
	case odbcBooleanSQLType:
		ctype = C.SQL_C_BIT
		bytes = int(C.sizeof_SQLCHAR)
	case int(C.SQL_TINYINT), int(C.SQL_SMALLINT), int(C.SQL_INTEGER), int(C.SQL_BIGINT):
		ctype = C.SQL_C_SBIGINT
		bytes = 8
	case int(C.SQL_REAL), int(C.SQL_FLOAT), int(C.SQL_DOUBLE):
		ctype = C.SQL_C_DOUBLE
		bytes = 8
	case int(C.SQL_DECIMAL), int(C.SQL_NUMERIC):
		ctype = C.SQL_C_CHAR
		bytes = maximum + 1
	case int(C.SQL_TYPE_TIMESTAMP), int(C.SQL_TIMESTAMP):
		ctype = C.SQL_C_TYPE_TIMESTAMP
		bytes = int(C.sizeof_SQL_TIMESTAMP_STRUCT)
	case int(C.SQL_CHAR), int(C.SQL_VARCHAR), int(C.SQL_LONGVARCHAR), int(C.SQL_WCHAR), int(C.SQL_WVARCHAR), int(C.SQL_WLONGVARCHAR):
	default:
		return nil, odbcError("DAMENG_ODBC_COLUMN_TYPE_UNSUPPORTED")
	}
	if buffer == nil || buffer.pointer == nil || bytes > buffer.bytes {
		return nil, odbcError("DAMENG_ODBC_BUFFER_REQUIRED")
	}
	p := buffer.pointer
	C.memset(p, 0, C.size_t(bytes))

	var indicator C.SQLLEN
	rc := C.SQLGetData(C.SQLHSTMT(h), C.SQLUSMALLINT(column), ctype, C.SQLPOINTER(p), C.SQLLEN(bytes), &indicator)
	if e := odbcStatus(rc, odbcStatementHandle, h); e != nil {
		return nil, e
	}
	return odbcDecodeCell(p, int(ctype), int64(indicator), bytes, maximum)
}

// odbcDecodeCell consumes only a successful SQLGetData buffer while its caller
// retains ownership. It makes copies of variable-length results and never stores
// the native pointer. This is also the buffer-level test boundary, not a driver
// replacement or a way to fabricate successful SQLGetData/connection outcomes.
func odbcDecodeCell(p unsafe.Pointer, ctype int, indicator int64, bytes, maximum int) (driver.Value, error) {
	if indicator == odbcNullData {
		return nil, nil
	}
	if p == nil || bytes <= 0 {
		return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
	}
	if indicator < 0 || indicator > int64(maximum) || indicator > int64(bytes) {
		return nil, odbcError("DAMENG_ODBC_CELL_LIMIT_EXCEEDED")
	}
	switch ctype {
	case odbcBinaryCType:
		return C.GoBytes(p, C.int(indicator)), nil
	case odbcBooleanCType:
		if bytes != int(C.sizeof_SQLCHAR) || indicator != int64(C.sizeof_SQLCHAR) {
			return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
		}
		value := *(*C.SQLCHAR)(p)
		if value != 0 && value != 1 {
			return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
		}
		return value == 1, nil
	case odbcSignedIntegerCType:
		if bytes != 8 || indicator != 8 {
			return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
		}
		return *(*int64)(p), nil
	case int(C.SQL_C_DOUBLE):
		if bytes != 8 || indicator != 8 {
			return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
		}
		v := *(*float64)(p)
		if math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
		}
		return v, nil
	case int(C.SQL_C_CHAR):
		text := string(C.GoBytes(p, C.int(indicator)))
		if !odbcDecimalPattern.MatchString(text) {
			return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
		}
		return text, nil
	case int(C.SQL_C_TYPE_TIMESTAMP):
		if bytes != int(C.sizeof_SQL_TIMESTAMP_STRUCT) || indicator != int64(C.sizeof_SQL_TIMESTAMP_STRUCT) {
			return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
		}
		t := (*C.SQL_TIMESTAMP_STRUCT)(p)
		v := time.Date(int(t.year), time.Month(t.month), int(t.day), int(t.hour), int(t.minute), int(t.second), int(t.fraction), time.UTC)
		if !validODBCTime(v) || v.Year() != int(t.year) || int(v.Month()) != int(t.month) || v.Day() != int(t.day) || v.Hour() != int(t.hour) || v.Minute() != int(t.minute) || v.Second() != int(t.second) {
			return nil, odbcError("DAMENG_ODBC_COLUMN_VALUE_INVALID")
		}
		return v, nil
	case int(C.SQL_C_WCHAR):
		text, e := odbcReadWide(p, int(indicator))
		if e != nil {
			return nil, e
		}
		if len(text) > maximum {
			return nil, odbcError("DAMENG_ODBC_CELL_LIMIT_EXCEEDED")
		}
		return text, nil
	default:
		return nil, odbcError("DAMENG_ODBC_COLUMN_TYPE_UNSUPPORTED")
	}
}

// Header-defined isolation values are never copied numeric constants.
func odbcIsolation(level driver.IsolationLevel) (uint32, error) {
	switch sql.IsolationLevel(level) {
	case sql.LevelDefault, sql.LevelReadCommitted:
		return uint32(C.SQL_TXN_READ_COMMITTED), nil
	case sql.LevelReadUncommitted:
		return uint32(C.SQL_TXN_READ_UNCOMMITTED), nil
	case sql.LevelSerializable:
		return uint32(C.SQL_TXN_SERIALIZABLE), nil
	}
	return 0, odbcError("DAMENG_ODBC_ISOLATION_UNSUPPORTED")
}

const odbcLoginTimeoutAttribute = int(C.SQL_ATTR_LOGIN_TIMEOUT)
const odbcConnectionTimeoutAttribute = int(C.SQL_ATTR_CONNECTION_TIMEOUT)
const odbcIsolationAttribute = int(C.SQL_ATTR_TXN_ISOLATION)
const odbcAutocommitAttribute = int(C.SQL_ATTR_AUTOCOMMIT)
const odbcAutocommitOn = uint32(C.SQL_AUTOCOMMIT_ON)
const odbcAutocommitOff = uint32(C.SQL_AUTOCOMMIT_OFF)

func odbcStatementSynchronous(h odbcHandle) error {
	if e := odbcStatus(C.po_statement_uint(C.SQLHSTMT(h), C.SQL_ATTR_ASYNC_ENABLE, C.SQL_ASYNC_ENABLE_OFF), odbcStatementHandle, h); e != nil {
		return e
	}
	var effective C.SQLULEN
	rc := C.SQLGetStmtAttr(C.SQLHSTMT(h), C.SQL_ATTR_ASYNC_ENABLE, C.SQLPOINTER(unsafe.Pointer(&effective)), C.SQLINTEGER(unsafe.Sizeof(effective)), nil)
	if e := odbcStatus(rc, odbcStatementHandle, h); e != nil {
		return e
	}
	if effective != C.SQL_ASYNC_ENABLE_OFF {
		return odbcError("DAMENG_ODBC_ASYNC_NOT_DISABLED")
	}
	return nil
}

func odbcInfoText(h odbcHandle, key C.SQLUSMALLINT) (string, error) {
	var buffer [256]C.SQLCHAR
	var length C.SQLSMALLINT
	rc := C.SQLGetInfo(C.SQLHDBC(h), key, C.SQLPOINTER(unsafe.Pointer(&buffer[0])), C.SQLSMALLINT(len(buffer)), &length)
	if e := odbcStatus(rc, odbcConnectionHandle, h); e != nil {
		return "", e
	}
	if length < 1 || int(length) >= len(buffer) {
		return "", odbcError("DAMENG_NATIVE_IDENTITY_INVALID")
	}
	value := string(C.GoBytes(unsafe.Pointer(&buffer[0]), C.int(length)))
	if !validODBCText(value, 128) {
		return "", odbcError("DAMENG_NATIVE_IDENTITY_INVALID")
	}
	return value, nil
}
func odbcVerifyIdentity(h odbcHandle, m NativeManifest) error {
	for _, v := range []struct {
		key      C.SQLUSMALLINT
		expected string
	}{{C.SQL_DRIVER_VER, m.DriverVersion}, {C.SQL_DBMS_NAME, m.DBMSName}, {C.SQL_DBMS_VER, m.DBMSVersion}} {
		value, e := odbcInfoText(h, v.key)
		if e != nil {
			return e
		}
		if value != v.expected {
			return odbcError("DAMENG_NATIVE_IDENTITY_MISMATCH")
		}
	}
	for _, function := range []C.SQLUSMALLINT{C.SQL_API_SQLDRIVERCONNECT, C.SQL_API_SQLPREPARE, C.SQL_API_SQLBINDPARAMETER, C.SQL_API_SQLEXECUTE, C.SQL_API_SQLFETCH, C.SQL_API_SQLGETDATA, C.SQL_API_SQLENDTRAN, C.SQL_API_SQLCANCEL, C.SQL_API_SQLGETCONNECTATTR, C.SQL_API_SQLSETCONNECTATTR, C.SQL_API_SQLGETSTMTATTR, C.SQL_API_SQLSETSTMTATTR} {
		var supported C.SQLUSMALLINT
		if e := odbcStatus(C.SQLGetFunctions(C.SQLHDBC(h), function, &supported), odbcConnectionHandle, h); e != nil {
			return e
		}
		if supported != C.SQL_TRUE {
			return odbcError("DAMENG_NATIVE_FUNCTION_UNSUPPORTED")
		}
	}
	return nil
}

// Read scratch and DAE transfer buffers are owned by the statement, not by a
// cancellable call frame. releaseStatement frees them only after joinODBC.
type odbcReadBuffer struct {
	pointer unsafe.Pointer
	bytes   int
	memory  *odbcMemoryLease
}

func odbcNewReadBuffer(bytes int, memory *odbcMemoryLease) (*odbcReadBuffer, error) {
	if bytes < 16 {
		return nil, odbcError("DAMENG_ODBC_BUFFER_REQUIRED")
	}
	if e := memory.grow(bytes); e != nil {
		return nil, e
	}
	p := C.calloc(C.size_t(bytes), 1)
	if p == nil {
		memory.shrink(bytes)
		return nil, odbcError("DAMENG_ODBC_ALLOCATION_FAILED")
	}
	return &odbcReadBuffer{p, bytes, memory}, nil
}
func (b *odbcReadBuffer) free() {
	if b == nil {
		return
	}
	if b.pointer != nil {
		C.memset(b.pointer, 0, C.size_t(b.bytes))
		C.free(b.pointer)
		b.pointer = nil
	}
	b.memory.close()
	b.memory = nil
}
func odbcExecuteParameters(ctx context.Context, h odbcHandle, p *odbcParameters) error {
	count := 0
	for _, v := range p.streams {
		if v != nil {
			count++
		}
	}
	if count == 0 {
		return odbcExecute(h)
	}
	if p.transfer == nil || p.chunkBytes < 4 {
		return odbcError("DAMENG_ODBC_STREAM_BUFFER_REQUIRED")
	}
	rc := C.SQLExecute(C.SQLHSTMT(h))
	if rc != C.SQL_NEED_DATA {
		if rc == C.SQL_SUCCESS || rc == C.SQL_NO_DATA {
			return odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
		}
		return odbcStatus(rc, odbcStatementHandle, h)
	}
	seen := make([]bool, p.count)
	for sent := 0; sent <= count; sent++ {
		if e := ctx.Err(); e != nil {
			return e
		}
		var token C.SQLPOINTER
		rc = C.SQLParamData(C.SQLHSTMT(h), &token)
		if rc == C.SQL_SUCCESS || rc == C.SQL_NO_DATA {
			if sent != count {
				return odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
			}
			return nil
		}
		if rc != C.SQL_NEED_DATA {
			return odbcStatus(rc, odbcStatementHandle, h)
		}
		source, e := p.takeStream(unsafe.Pointer(token), seen)
		if e != nil {
			return e
		}
		offset := 0
		for {
			if e := ctx.Err(); e != nil {
				return e
			}
			n, next, e := odbcTransferChunk(source, offset, p.transfer, p.chunkBytes)
			if e != nil {
				return e
			}
			offset = next

			if n == 0 {
				break
			}
			if e := odbcStatus(C.SQLPutData(C.SQLHSTMT(h), C.SQLPOINTER(p.transfer), C.SQLLEN(n)), odbcStatementHandle, h); e != nil {
				return e
			}
		}
	}
	return odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
}
func odbcLongKind(kind int) bool {
	return kind == int(C.SQL_LONGVARCHAR) || kind == int(C.SQL_WLONGVARCHAR) || kind == int(C.SQL_LONGVARBINARY)
}

// odbcLongAccumulator checks lengths and UTF-16 before append. The factor four
// covers geometric byte-slice growth, final string conversion and one scan copy;
// memory after caller-owned values escape Rows.Close belongs to the application.
type odbcLongAccumulator struct {
	value   []byte
	high    uint16
	maximum int
	binary  bool
	memory  *odbcMemoryLease
}

func (a *odbcLongAccumulator) appendBytes(b []byte) error {
	if len(b) > a.maximum-len(a.value) {
		return odbcError("DAMENG_ODBC_LONG_VALUE_LIMIT_EXCEEDED")
	}
	if e := a.memory.grow(len(b) * 4); e != nil {
		return e
	}
	a.value = append(a.value, b...)
	return nil
}
func (a *odbcLongAccumulator) wide(units []uint16, final bool) error {
	high := a.high
	size := 0
	for _, u := range units {
		if high != 0 {
			if u < 0xdc00 || u > 0xdfff {
				return odbcError("DAMENG_ODBC_UNICODE_INVALID")
			}
			high = 0
			size += 4
		} else if u >= 0xd800 && u <= 0xdbff {
			high = u
		} else if u == 0 || u >= 0xdc00 && u <= 0xdfff {
			return odbcError("DAMENG_ODBC_UNICODE_INVALID")
		} else {
			size += utf8.RuneLen(rune(u))
		}
	}
	if final && high != 0 {
		return odbcError("DAMENG_ODBC_UNICODE_INVALID")
	}
	if size > a.maximum-len(a.value) {
		return odbcError("DAMENG_ODBC_LONG_VALUE_LIMIT_EXCEEDED")
	}
	if e := a.memory.grow(size * 4); e != nil {
		return e
	}
	for _, u := range units {
		if a.high != 0 {
			a.value = utf8.AppendRune(a.value, utf16.DecodeRune(rune(a.high), rune(u)))
			a.high = 0
		} else if u >= 0xd800 && u <= 0xdbff {
			a.high = u
		} else {
			a.value = utf8.AppendRune(a.value, rune(u))
		}
	}
	return nil
}
func odbcLongCell(ctx context.Context, h odbcHandle, column, kind int, cfg ODBCConfig, buffer *odbcReadBuffer, memory *odbcMemoryLease) (driver.Value, error) {
	binary := kind == int(C.SQL_LONGVARBINARY)
	ctype := C.SQLSMALLINT(C.SQL_C_WCHAR)
	payload := cfg.TransferChunkBytes - 2
	if binary {
		ctype = C.SQL_C_BINARY
		payload = cfg.TransferChunkBytes
	}
	if buffer == nil || buffer.pointer == nil || buffer.bytes < cfg.TransferChunkBytes {
		return nil, odbcError("DAMENG_ODBC_BUFFER_REQUIRED")
	}
	if e := memory.grow(64); e != nil {
		return nil, e
	}
	a := odbcLongAccumulator{maximum: cfg.MaximumLongValueBytes, binary: binary, memory: memory}
	frame := odbcLongFrame{}
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		C.memset(buffer.pointer, 0, C.size_t(cfg.TransferChunkBytes))
		var indicator C.SQLLEN
		rc := C.SQLGetData(C.SQLHSTMT(h), C.SQLUSMALLINT(column), ctype, C.SQLPOINTER(buffer.pointer), C.SQLLEN(cfg.TransferChunkBytes), &indicator)
		more := rc == C.SQL_SUCCESS_WITH_INFO
		if more {
			if C.po_only_truncation(C.SQLHSTMT(h)) != 1 {
				return nil, odbcError("DAMENG_ODBC_LONG_VALUE_WARNING")
			}
		} else if rc != C.SQL_SUCCESS {
			return nil, odbcStatus(rc, odbcStatementHandle, h)
		}
		n, isNull, e := frame.accept(int64(indicator), more, payload)
		if e != nil {
			return nil, e
		}
		if isNull {
			return nil, nil
		}

		if binary {
			if e := a.appendBytes(unsafe.Slice((*byte)(buffer.pointer), n)); e != nil {
				return nil, e
			}
		} else {
			if n%2 != 0 || *(*uint16)(unsafe.Add(buffer.pointer, n)) != 0 {
				return nil, odbcError("DAMENG_ODBC_UNICODE_INVALID")
			}
			if e := a.wide(unsafe.Slice((*uint16)(buffer.pointer), n/2), !more); e != nil {
				return nil, e
			}
		}
		if !more {
			if binary {
				if a.value == nil {
					return []byte{}, nil
				}
				return a.value, nil
			}
			return string(a.value), nil
		}
	}
}

func odbcVerifyLongFunctions(h odbcHandle) error {
	for _, function := range []C.SQLUSMALLINT{C.SQL_API_SQLPARAMDATA, C.SQL_API_SQLPUTDATA} {
		var supported C.SQLUSMALLINT
		if e := odbcStatus(C.SQLGetFunctions(C.SQLHDBC(h), function, &supported), odbcConnectionHandle, h); e != nil {
			return e
		}
		if supported != C.SQL_TRUE {
			return odbcError("DAMENG_NATIVE_LONG_VALUE_FUNCTION_UNSUPPORTED")
		}
	}
	return nil
}

func odbcLongLength(indicator int64, more bool, payload int, seen bool) (int, bool, error) {
	bad := odbcError("DAMENG_ODBC_LONG_VALUE_LENGTH_INVALID")
	if payload < 1 {
		return 0, false, bad
	}
	if indicator == int64(C.SQL_NULL_DATA) {
		if more || seen {
			return 0, false, bad
		}
		return 0, true, nil
	}
	if more {
		if indicator != int64(C.SQL_NO_TOTAL) && (indicator < 0 || indicator <= int64(payload)) {
			return 0, false, bad
		}
		return payload, false, nil
	}
	// An initial zero is an empty non-NULL value. After a continuation, ODBC
	// requires a positive final length; zero cannot prove a complete value.
	if indicator < 0 || indicator > int64(payload) || seen && indicator == 0 {
		return 0, false, bad
	}
	return int(indicator), false, nil
}

func odbcTransferChunk(source any, offset int, buffer unsafe.Pointer, chunk int) (int, int, error) {
	if buffer == nil || chunk < 4 || chunk%4 != 0 || offset < 0 {
		return 0, offset, odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
	}
	n := 0
	switch v := source.(type) {
	case string:
		if offset > len(v) {
			return 0, offset, odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
		}
		units := unsafe.Slice((*uint16)(buffer), chunk/2)
		for offset < len(v) {
			r, size := utf8.DecodeRuneInString(v[offset:])
			need := 2
			if r > 0xffff {
				need = 4
			}
			if r == 0 || r == utf8.RuneError && size == 1 {
				return 0, offset, odbcError("DAMENG_ODBC_UNICODE_INVALID")
			}
			if n+need > chunk {
				break
			}
			if need == 4 {
				a, b := utf16.EncodeRune(r)
				units[n/2] = uint16(a)
				units[n/2+1] = uint16(b)
			} else {
				units[n/2] = uint16(r)
			}
			n += need
			offset += size
		}
	case []byte:
		if offset > len(v) {
			return 0, offset, odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
		}
		n = min(chunk, len(v)-offset)
		copy(unsafe.Slice((*byte)(buffer), n), v[offset:offset+n])
		offset += n
	default:
		return 0, offset, odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
	}
	return n, offset, nil
}

// A known length is remaining native bytes before this call, never an excuse
// to accept an early successful EOF. Unknown totals become pinned when known.
type odbcLongFrame struct {
	consumed, total int64
	known, seen     bool
}

func (f *odbcLongFrame) accept(indicator int64, more bool, payload int) (int, bool, error) {
	n, isNull, e := odbcLongLength(indicator, more, payload, f.seen)
	if e != nil || isNull {
		return n, isNull, e
	}
	const maximumNativeLength = 1<<63 - 1
	if int64(n) > maximumNativeLength-f.consumed || indicator >= 0 && indicator > maximumNativeLength-f.consumed {
		return 0, false, odbcError("DAMENG_ODBC_LONG_VALUE_LENGTH_INVALID")
	}
	if indicator >= 0 {
		if !f.known {
			f.total = f.consumed + indicator
			f.known = true
		} else if indicator != f.total-f.consumed {
			return 0, false, odbcError("DAMENG_ODBC_LONG_VALUE_LENGTH_INVALID")
		}
	}
	f.consumed += int64(n)
	f.seen = true
	if !more && f.known && f.consumed != f.total {
		return 0, false, odbcError("DAMENG_ODBC_LONG_VALUE_LENGTH_INVALID")
	}
	return n, false, nil
}

// Tokens are native descriptor addresses, never Go pointers or user identities.
// This selection is used only after real SQLParamData requests the next value.
func (p *odbcParameters) takeStream(token unsafe.Pointer, seen []bool) (any, error) {
	if token == nil || p.pointer == nil || len(seen) != p.count || len(p.streams) != p.count {
		return nil, odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
	}
	for i, value := range p.streams {
		if value != nil && token == unsafe.Pointer(C.po_parameter_at(p.pointer, C.size_t(i))) {
			if seen[i] {
				return nil, odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
			}
			seen[i] = true
			return value, nil
		}
	}
	return nil, odbcError("DAMENG_ODBC_STREAM_SEQUENCE_INVALID")
}
