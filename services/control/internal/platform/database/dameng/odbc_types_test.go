//go:build cgo && odbc_manager

package dameng

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"reflect"
	"testing"
	"time"
	"unsafe"
)

// These tests inspect real C-owned bindings and the production result decoder.
// They do not connect to a driver or establish actual DM SQL/type fidelity.
func TestODBCNullableBooleanBindings(t *testing.T) {
	type namedBool bool
	for _, test := range []struct {
		name  string
		input any
		want  any
	}{
		{"true", true, true},
		{"false", false, false},
		{"named-false", namedBool(false), false},
		{"nullable-true", sql.NullBool{Bool: true, Valid: true}, true},
		{"nullable-false", sql.NullBool{Bool: false, Valid: true}, false},
		{"nullable-null", sql.NullBool{Bool: true, Valid: false}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			arg := driver.NamedValue{Ordinal: 1, Value: test.input}
			if err := (&odbcConnection{}).CheckNamedValue(&arg); err != nil {
				t.Fatal(err)
			}
			parameters, err := odbcBuildParameters([]driver.NamedValue{arg}, 8192)
			if err != nil {
				t.Fatal(err)
			}
			defer parameters.free()
			binding := parameters.pointer
			if test.want == nil {
				if int64(binding.indicator) != odbcNullData || binding.data != nil || binding.bytes != 0 {
					t.Fatal("nullable bool lost SQL NULL or allocated a value buffer")
				}
			} else {
				if int(binding.ctype) != odbcBooleanCType || int(binding.sqltype) != odbcBooleanSQLType || binding.precision != 1 || binding.scale != 0 || binding.bytes != 1 || binding.indicator != 1 || binding.data == nil {
					t.Fatal("bool native descriptor is not exact SQL_C_BIT / SQL_BIT")
				}
				wantByte := byte(0)
				if test.want == true {
					wantByte = 1
				}
				if *(*byte)(unsafe.Pointer(binding.data)) != wantByte {
					t.Fatal("native bool buffer changed true/false")
				}
			}
			value, err := odbcDecodeCell(unsafe.Pointer(binding.data), int(binding.ctype), int64(binding.indicator), int(binding.bytes), 16)
			if err != nil || value != test.want {
				t.Fatalf("nullable bool buffer result mismatch: %v", err)
			}
		})
	}
}

func TestODBCNullableBinaryBindingsAndCopies(t *testing.T) {
	input := []byte{0, 1, 255}
	parameters, err := odbcBuildParameters([]driver.NamedValue{
		{Ordinal: 1, Value: []byte(nil)},
		{Ordinal: 2, Value: []byte{}},
		{Ordinal: 3, Value: input},
	}, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer parameters.free()
	bindings := unsafe.Slice(parameters.pointer, parameters.count)
	for i := range bindings {
		if int(bindings[i].ctype) != odbcBinaryCType || int(bindings[i].sqltype) != odbcBinarySQLType {
			t.Fatal("binary type metadata lost")
		}
	}
	if int64(bindings[0].indicator) != odbcNullData || bindings[0].data != nil || bindings[0].bytes != 0 {
		t.Fatal("nil binary is not an unallocated SQL NULL")
	}
	if bindings[1].indicator != 0 || bindings[1].data == nil || bindings[1].bytes != 1 || bindings[1].precision != 1 {
		t.Fatal("empty binary lost its non-NULL owned sentinel")
	}
	if bindings[2].indicator != 3 || bindings[2].bytes != 3 || bindings[2].precision != 3 {
		t.Fatal("nonempty binary allocation is not exact")
	}
	input[0] = 99
	for i, want := range [][]byte{nil, {}, {0, 1, 255}} {
		binding := &bindings[i]
		value, err := odbcDecodeCell(unsafe.Pointer(binding.data), int(binding.ctype), int64(binding.indicator), int(binding.bytes), 16)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if value != nil {
				t.Fatal("binary SQL NULL projected a value")
			}
			continue
		}
		got, ok := value.([]byte)
		if !ok || got == nil || !bytes.Equal(got, want) {
			t.Fatal("binary copy or empty/NULL distinction lost")
		}
		if i == 2 {
			got[0] = 88
			if *(*byte)(unsafe.Pointer(binding.data)) != 0 {
				t.Fatal("result retained native buffer alias")
			}
		}
	}
}

func TestODBCNullableBindingsRespectExactNativeBudget(t *testing.T) {
	var empty odbcParameters
	descriptorBytes := int(unsafe.Sizeof(*empty.pointer))
	t.Run("empty-parameters", func(t *testing.T) {
		for _, args := range [][]driver.NamedValue{nil, {}} {
			for _, budget := range []int{0, 1, descriptorBytes - 1} {
				parameters, err := odbcBuildParameters(args, budget)
				if err != nil {
					t.Fatal("zero-allocation parameter list refused", err)
				}
				if parameters.pointer != nil || parameters.count != 0 {
					parameters.free()
					t.Fatal("empty parameter list allocated native ownership")
				}
				parameters.free()
			}
		}
	})
	for _, test := range []struct {
		name    string
		value   any
		payload int
	}{
		{"false", false, 1},
		{"empty-binary", []byte{}, 1},
		{"null-binary", []byte(nil), 0},
		{"nonempty-binary", []byte{0, 1, 255}, 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []driver.NamedValue{{Ordinal: 1, Value: test.value}}
			budget := descriptorBytes + test.payload
			parameters, err := odbcBuildParameters(args, budget)
			if err != nil {
				t.Fatal("exact native allocation budget refused", err)
			}
			parameters.free()
			parameters, err = odbcBuildParameters(args, budget-1)
			if err == nil {
				parameters.free()
				t.Fatal("native allocation exceeded exact budget")
			}
			if parameters.pointer != nil || parameters.count != 0 {
				t.Fatal("failed allocation published native ownership")
			}
		})
	}
}

func TestODBCBooleanResultRejectsNoncanonicalBuffers(t *testing.T) {
	parameters, err := odbcBuildParameters([]driver.NamedValue{{Ordinal: 1, Value: false}}, 8192)
	if err != nil {
		t.Fatal(err)
	}
	defer parameters.free()
	pointer := unsafe.Pointer(parameters.pointer.data)
	for _, value := range []byte{2, 255} {
		*(*byte)(pointer) = value
		got, err := odbcDecodeCell(pointer, odbcBooleanCType, 1, 1, 16)
		if err == nil || got != nil {
			t.Fatal("noncanonical native bit projected a boolean")
		}
	}
	*(*byte)(pointer) = 0
	for _, indicator := range []int64{-4, -2, 0, 2, 17} {
		got, err := odbcDecodeCell(pointer, odbcBooleanCType, indicator, 1, 16)
		if err == nil || got != nil {
			t.Fatal("invalid bit length projected a value")
		}
	}
	if got, err := odbcDecodeCell(nil, odbcBooleanCType, 1, 1, 16); err == nil || got != nil {
		t.Fatal("missing native buffer projected a value")
	}
	if got, err := odbcDecodeCell(nil, odbcBooleanCType, odbcNullData, 1, 16); err != nil || got != nil {
		t.Fatal("SQL NULL requires no value buffer")
	}
	// SQL integer 0/1 must retain integer identity; only SQL_BIT becomes bool.
	for _, number := range []int64{0, 1, -7} {
		got, err := odbcDecodeCell(unsafe.Pointer(&number), odbcSignedIntegerCType, 8, 8, 16)
		if err != nil || got != number {
			t.Fatal("integer result was coerced into bool")
		}
	}
}

func TestODBCScalarResultDecodingPreservesExistingTypes(t *testing.T) {
	decimal, err := Decimal("-123.45", 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := time.Date(2026, 10, 24, 1, 2, 3, 456789000, time.UTC)
	for _, test := range []struct {
		name  string
		input any
		want  any
	}{
		{"empty-text", "", ""},
		{"unicode", "Unicode \u4e2d\u6587😀", "Unicode \u4e2d\u6587😀"},
		{"signed-integer", int64(-9223372036854775807), int64(-9223372036854775807)},
		{"double", 1.5, 1.5},
		{"decimal-text", decimal, "-123.45"},
		{"utc-microseconds", timestamp, timestamp},
	} {
		t.Run(test.name, func(t *testing.T) {
			parameters, err := odbcBuildParameters([]driver.NamedValue{{Ordinal: 1, Value: test.input}}, 8192)
			if err != nil {
				t.Fatal(err)
			}
			defer parameters.free()
			binding := parameters.pointer
			got, err := odbcDecodeCell(unsafe.Pointer(binding.data), int(binding.ctype), int64(binding.indicator), int(binding.bytes), 128)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("existing scalar result contract changed: %v", err)
			}
		})
	}
}
