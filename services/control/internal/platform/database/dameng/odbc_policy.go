package dameng

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// ODBCConfig belongs to the standalone calling-layer constructor. It does not
// enable a mainline database selector or establish a secure native DM session.
type ODBCConfig struct {
	FormatVersion              int    `json:"format_version"`
	ConnectionFile             string `json:"connection_file"`
	LoginSeconds               int    `json:"login_seconds"`
	ConnectionSeconds          int    `json:"connection_seconds"`
	QuerySeconds               int    `json:"query_seconds"`
	MaximumParameterBytes      int    `json:"maximum_parameter_bytes"`
	MaximumCellBytes           int    `json:"maximum_cell_bytes"`
	MaximumRows                int    `json:"maximum_rows"`
	MaximumColumns             int    `json:"maximum_columns"`
	MaximumResultBytes         int    `json:"maximum_result_bytes"`
	MaximumSQLBytes            int    `json:"maximum_sql_bytes"`
	MaximumOpenConnections     int    `json:"maximum_open_connections"`
	MaximumPhysicalConnections int    `json:"maximum_physical_connections"`
	MaximumLongValueBytes      int    `json:"maximum_long_value_bytes,omitempty"`
	TransferChunkBytes         int    `json:"transfer_chunk_bytes,omitempty"`
	MaximumRetainedBytes       int    `json:"maximum_retained_bytes,omitempty"`
}

// Includes the one-row lookahead for the retained 10000-sample monitoring
// contract. Aggregate scalar/LOB/retained-memory bounds remain independent.
const maximumODBCRows = 10001

func (ODBCConfig) String() string   { return "DAMENG_ODBC_CONFIGURATION_REDACTED" }
func (ODBCConfig) GoString() string { return "DAMENG_ODBC_CONFIGURATION_REDACTED" }
func (c ODBCConfig) Validate() error {
	if (c.FormatVersion != 2 && c.FormatVersion != 3) || !canonicalPath(c.ConnectionFile) {
		return fail("DAMENG_ODBC_CONFIGURATION_INVALID")
	}
	return c.validateBounds()
}
func (c ODBCConfig) validateBounds() error {
	if c.FormatVersion == 2 {
		if c.MaximumLongValueBytes != 0 || c.TransferChunkBytes != 0 || c.MaximumRetainedBytes != 0 {
			return fail("DAMENG_ODBC_CONFIGURATION_INVALID")
		}
	} else if c.FormatVersion == 3 {
		if c.MaximumLongValueBytes < c.MaximumCellBytes || c.MaximumLongValueBytes > 64<<20 || c.TransferChunkBytes < 4096 || c.TransferChunkBytes > 256<<10 || c.TransferChunkBytes%4 != 0 || c.TransferChunkBytes > c.MaximumParameterBytes || c.MaximumRetainedBytes < c.MaximumLongValueBytes*4+c.TransferChunkBytes*2 || c.MaximumRetainedBytes > 512<<20 {
			return fail("DAMENG_ODBC_CONFIGURATION_INVALID")
		}
	} else {
		return fail("DAMENG_ODBC_CONFIGURATION_INVALID")
	}

	if c.LoginSeconds < 1 || c.LoginSeconds > 30 || c.ConnectionSeconds < 1 || c.ConnectionSeconds > 120 || c.QuerySeconds < 1 || c.QuerySeconds > 120 || c.MaximumParameterBytes < 1 || c.MaximumParameterBytes > 1<<20 || c.MaximumCellBytes < 16 || c.MaximumCellBytes > 1<<20 || c.MaximumRows < 1 || c.MaximumRows > maximumODBCRows || c.MaximumColumns < 1 || c.MaximumColumns > 128 || c.MaximumResultBytes < c.MaximumCellBytes || c.MaximumResultBytes > 16<<20 || c.MaximumSQLBytes < 1 || c.MaximumSQLBytes > 65536 || c.MaximumOpenConnections < 1 || c.MaximumOpenConnections > 4 || c.MaximumPhysicalConnections < c.MaximumOpenConnections || c.MaximumPhysicalConnections > 4 {
		return fail("DAMENG_ODBC_CONFIGURATION_INVALID")
	}
	return nil
}
func LoadODBCConfig(path string) (ODBCConfig, error) {
	raw, e := readODBCPrivateInput(path, MaximumConfigurationBytes)
	var c ODBCConfig
	if e != nil || release.StrictJSON(raw, &c) != nil {
		return ODBCConfig{}, fail("DAMENG_ODBC_CONFIGURATION_INVALID")
	}
	fields := 13
	if c.FormatVersion == 3 {
		fields = 16
	}
	var present map[string]json.RawMessage
	if release.StrictJSON(raw, &present) != nil || len(present) != fields {
		return ODBCConfig{}, fail("DAMENG_ODBC_CONFIGURATION_INVALID")
	}
	for _, value := range present {
		if strings.TrimSpace(string(value)) == "null" {
			return ODBCConfig{}, fail("DAMENG_ODBC_CONFIGURATION_INVALID")
		}
	}
	if e = c.Validate(); e != nil {
		return ODBCConfig{}, e
	}
	return c, nil
}

// ODBCError retains only standard SQLSTATE/native integer facts. MessageText,
// server-supplied diagnostic strings and the credential-bearing DSN never enter it.
type ODBCError struct {
	code       string
	sqlState   string
	nativeCode int32
	commitSent bool
}

func (e *ODBCError) Error() string    { return e.code }
func (e *ODBCError) String() string   { return e.code }
func (e *ODBCError) GoString() string { return e.code }

func odbcError(code string) error { return &ODBCError{code: code} }
func odbcContext(ctx context.Context) error {
	if ctx == nil {
		return odbcError("DAMENG_ODBC_CONTEXT_REQUIRED")
	}
	if _, ok := ctx.Deadline(); !ok {
		return odbcError("DAMENG_ODBC_DEADLINE_REQUIRED")
	}
	return ctx.Err()
}

type odbcCompletion struct {
	value any
	err   error
}

// joinODBC never interprets cancellation acknowledgement as completed execution.
// The caller owns the handle and every binding until BOTH calls have completed.
// A native driver ignoring its timeout/cancel can delay this join indefinitely;
// the actual target must qualify that behavior before in-process serving admission.
func joinODBC(ctx context.Context, cancel func() error, draining func(), operation func() (any, error)) (any, error) {
	if e := odbcContext(ctx); e != nil {
		return nil, e
	}
	done := make(chan odbcCompletion, 1)
	go func() {
		result := odbcCompletion{}
		defer func() {
			if recover() != nil {
				result = odbcCompletion{err: odbcError("DAMENG_ODBC_OPERATION_PANIC")}
			}
			done <- result
		}()
		result.value, result.err = operation()
	}()
	select {
	case result := <-done:
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if result.err != nil {
			return nil, result.err
		}
		return result.value, nil
	case <-ctx.Done():
		if draining != nil {
			draining()
		}
		cancelDone := make(chan struct{})
		go func() {
			defer close(cancelDone)
			defer func() { recover() }()
			if cancel != nil {
				_ = cancel()
			}
		}()
		<-done
		<-cancelDone
		return nil, ctx.Err()
	}
}

// ODBCDecimal is an exact validated decimal binding, never a float conversion.
type ODBCDecimal struct {
	text             string
	precision, scale int
}

var odbcDecimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

func Decimal(text string, precision, scale int) (ODBCDecimal, error) {
	if precision < 1 || precision > 38 || scale < 0 || scale > precision || !odbcDecimalPattern.MatchString(text) {
		return ODBCDecimal{}, odbcError("DAMENG_ODBC_DECIMAL_INVALID")
	}
	parts := strings.Split(strings.TrimPrefix(text, "-"), ".")
	fraction := 0
	if len(parts) == 2 {
		fraction = len(parts[1])
	}
	integer := len(strings.TrimLeft(parts[0], "0"))
	if fraction > scale || integer > precision-scale {
		return ODBCDecimal{}, odbcError("DAMENG_ODBC_DECIMAL_INVALID")
	}
	return ODBCDecimal{text, precision, scale}, nil
}
func (ODBCDecimal) String() string   { return "DAMENG_ODBC_DECIMAL_REDACTED" }
func (ODBCDecimal) GoString() string { return "DAMENG_ODBC_DECIMAL_REDACTED" }

func validODBCText(value string, maximum int) bool {
	return utf8.ValidString(value) && len(value) <= maximum && !strings.ContainsRune(value, 0)
}
func validODBCTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Year() >= 1970 && value.Year() <= 9999 && value.Nanosecond()%1000 == 0
}
func cancelledODBC(e error) bool {
	return errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded)
}

// ODBCCapacity is a copied, nonsecret capability projection. Bytes cover driver
// ownership, not memory retained by application values after Rows.Close.
type ODBCCapacity struct {
	LongValues                                                              bool
	InlineParameterBytes, InlineCellBytes, InlineResultBytes, Rows, Columns int
	LongValueBytes, ChunkBytes, RetainedBytes                               int
}

func (c ODBCConfig) Capacity() ODBCCapacity {
	return ODBCCapacity{c.FormatVersion == 3, c.MaximumParameterBytes, c.MaximumCellBytes, c.MaximumResultBytes, c.MaximumRows, c.MaximumColumns, c.MaximumLongValueBytes, c.TransferChunkBytes, c.retainedLimit()}
}

// odbcColumnMemoryAllowance bounds names and descriptors before native metadata
// allocation. Both the query reservation and legacy derived ceiling use it.
const odbcColumnMemoryAllowance = 1024

func (c ODBCConfig) retainedLimit() int {
	if c.FormatVersion == 3 {
		return c.MaximumRetainedBytes
	}
	// Legacy policy retains its input semantics; derive a finite conservative
	// ceiling for its existing inline allocations and materialized results.
	return c.MaximumPhysicalConnections * (c.MaximumParameterBytes*4 + c.MaximumResultBytes*4 + c.MaximumCellBytes*8 + c.MaximumColumns*odbcColumnMemoryAllowance + 65536)
}
