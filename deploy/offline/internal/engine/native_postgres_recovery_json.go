package engine

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// A Go fixed array silently discards extra JSON elements. The native profile
// instead requires exactly its original four store entries, and exact first-
// party field names throughout. This changes no marshaled task/legacy bytes.
// File/journal byte admission and current task authority remain their owners'
// responsibility; this decoder grants no command, volume or native SQL access.
func (p *NativePostgreSQLRecoveryProfile) UnmarshalJSON(raw []byte) error {
	bad := errors.New("OFFLINE_ENGINE_NATIVE_RECOVERY_PROFILE_JSON_INVALID")
	if p == nil || !utf8.Valid(raw) {
		return bad
	}
	type plain NativePostgreSQLRecoveryProfile
	var value plain
	if wire.StrictJSON(raw, &value) != nil || !exactNativeRecoveryJSONShape(raw, reflect.TypeOf(value)) {
		return bad
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return bad
	}
	stores, present := fields["stores"]
	if !present {
		return bad
	}
	var entries []json.RawMessage
	if json.Unmarshal(stores, &entries) != nil || len(entries) != len(value.Stores) {
		return bad
	}
	*p = NativePostgreSQLRecoveryProfile(value)
	return nil
}

// StrictJSON has already bounded depth, refused duplicate/null/unknown fields
// and type mismatches. This additional shape check rejects encoding/json's case
// aliases and fixed-array truncation, including nested first-party structures.
func exactNativeRecoveryJSONShape(raw []byte, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return false
		}
		declared := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			declared[name] = f.Type
		}
		for name, body := range fields {
			kind, ok := declared[name]
			if !ok || !exactNativeRecoveryJSONShape(body, kind) {
				return false
			}
		}
	case reflect.Array, reflect.Slice:
		var entries []json.RawMessage
		if json.Unmarshal(raw, &entries) != nil || entries == nil || t.Kind() == reflect.Array && len(entries) != t.Len() {
			return false
		}
		for _, body := range entries {
			if !exactNativeRecoveryJSONShape(body, t.Elem()) {
				return false
			}
		}
	}
	return true
}
