package privateclusters

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"unicode/utf8"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Kubernetes may add fields, but it may not alias a field we use for authority.
// Go's default case-insensitive field matching and null-to-zero scalar behavior
// would otherwise turn malformed third-party JSON into a valid observation.
func decodeResponse(raw []byte, destination any) error {
	var checked json.RawMessage
	if !utf8.Valid(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || release.UniqueJSON(raw, &checked) != nil || jsonFields(raw, reflect.TypeOf(destination), 0, true) != nil {
		return fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
	}
	return json.Unmarshal(raw, destination)
}

func decodeProtectedJSON(raw []byte, destination any) error {
	if !utf8.Valid(raw) || jsonFields(raw, reflect.TypeOf(destination), 0, false) != nil {
		return fail("PRIVATE_CLUSTER_CONFIGURATION_INVALID")
	}
	return release.StrictJSON(raw, destination)
}

// A missing/null items collection is not a completed inventory. In particular it
// must never stand in for the observed Job/Pod absence needed to release a Lease.
func (list *objectList[T]) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if release.UniqueJSON(raw, &fields) != nil {
		return fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
	}
	for _, name := range []string{"apiVersion", "kind", "metadata", "items"} {
		value, present := fields[name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
		}
	}
	type plain objectList[T]
	var value plain
	if decodeResponse(raw, &value) != nil || value.Items == nil {
		return fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
	}
	*list = objectList[T](value)
	return nil
}

func jsonFields(raw []byte, kind reflect.Type, depth int, extensions bool) error {
	bad := fail("PRIVATE_CLUSTER_RESPONSE_INVALID")
	if kind == nil || depth > 64 {
		return bad
	}
	if kind == reflect.TypeFor[json.RawMessage]() || kind.Kind() == reflect.Interface {
		return nil
	}
	if kind.Kind() == reflect.Pointer {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil
		}
		return jsonFields(raw, kind.Elem(), depth+1, extensions)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if kind.Kind() == reflect.Map || kind.Kind() == reflect.Slice {
			return nil // API-optional collections have the same empty semantics.
		}
		return bad
	}
	switch kind.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return bad
		}
		names := map[string]reflect.Type{}
		responseFieldTypes(kind, names)
		for name, value := range fields {
			field, known := names[name]
			if !known {
				if !extensions {
					return bad
				}
				for canonical := range names {
					if strings.EqualFold(canonical, name) {
						return bad
					}
				}
				continue // Preserve Kubernetes API extensibility.
			}
			if jsonFields(value, field, depth+1, extensions) != nil {
				return bad
			}
		}
	case reflect.Slice, reflect.Array:
		if kind.Elem().Kind() == reflect.Uint8 {
			return nil // encoding/json validates base64-encoded []byte itself.
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return bad
		}
		for _, value := range values {
			if jsonFields(value, kind.Elem(), depth+1, extensions) != nil {
				return bad
			}
		}
	case reflect.Map:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return bad
		}
		for _, value := range fields {
			if jsonFields(value, kind.Elem(), depth+1, extensions) != nil {
				return bad
			}
		}
	}
	return nil
}

func responseFieldTypes(kind reflect.Type, names map[string]reflect.Type) {
	for index := 0; index < kind.NumField(); index++ {
		field := kind.Field(index)
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" && field.Type.Kind() == reflect.Struct {
			responseFieldTypes(field.Type, names)
			continue
		}
		if field.PkgPath != "" { // Unexported fields are not decoded by encoding/json.
			continue
		}
		if name == "" {
			name = field.Name
		}
		names[name] = field.Type
	}
}
