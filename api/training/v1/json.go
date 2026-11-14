package v1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"
)

func invalidWire() error { return errors.New("TRAINING_WIRE_INVALID") }

// ExactObject validates dynamic third-party tensor/adapter metadata against an explicitly supplied complete key set.
// It is not an opaque machine DTO: callers still decode each value into its fixed metadata type.
func ExactObject(raw []byte, keys []string) (map[string]json.RawMessage, error) {
	if !utf8.Valid(raw) || !dataset.ValidJSONUnicode(raw) {
		return nil, invalidWire()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if uniqueValue(d) != nil {
		return nil, invalidWire()
	}
	if _, e := d.Token(); e != io.EOF {
		return nil, invalidWire()
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil || len(values) != len(keys) {
		return nil, invalidWire()
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			return nil, invalidWire()
		}
		seen[key] = true
		if _, present := values[key]; !present {
			return nil, invalidWire()
		}
	}
	return values, nil
}

// DecodeStrict permits null only for declared pointers; fields remain exact and case-sensitive.
func DecodeStrict(raw []byte, out any) error {
	t := reflect.TypeOf(out)
	if t == nil || t.Kind() != reflect.Pointer || !utf8.Valid(raw) || !dataset.ValidJSONUnicode(raw) {
		return invalidWire()
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if uniqueValue(d) != nil {
		return invalidWire()
	}
	if _, e := d.Token(); e != io.EOF {
		return invalidWire()
	}
	if exactValue(raw, t.Elem()) != nil {
		return invalidWire()
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return invalidWire()
	}
	return nil
}

func uniqueValue(d *json.Decoder) error {
	token, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			token, e = d.Token()
			if e != nil {
				return e
			}
			key, ok := token.(string)
			if !ok || seen[key] {
				return invalidWire()
			}
			seen[key] = true
			if e = uniqueValue(d); e != nil {
				return e
			}
		}
		closing, e := d.Token()
		if e != nil || closing != json.Delim('}') {
			return invalidWire()
		}
	case '[':
		for d.More() {
			if e = uniqueValue(d); e != nil {
				return e
			}
		}
		closing, e := d.Token()
		if e != nil || closing != json.Delim(']') {
			return invalidWire()
		}
	default:
		return invalidWire()
	}
	return nil
}

func exactValue(raw json.RawMessage, t reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if t.Kind() == reflect.Pointer {
			return nil
		}
		return invalidWire()
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(time.Time{}) {
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" {
			return invalidWire()
		}
		_, e := time.Parse(time.RFC3339Nano, value)
		return e
	}
	switch t.Kind() {
	case reflect.Struct:
		var values map[string]json.RawMessage
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return invalidWire()
		}
		fields := map[string]reflect.StructField{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := f.Tag.Get("json")
			name := strings.Split(tag, ",")[0]
			if name == "" || name == "-" {
				continue
			}
			fields[name] = f
			if !strings.Contains(tag, ",omitempty") {
				if _, ok := values[name]; !ok {
					return invalidWire()
				}
			}
		}
		for name, value := range values {
			f, ok := fields[name]
			if !ok {
				return invalidWire()
			}
			if exactValue(value, f.Type) != nil {
				return invalidWire()
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return invalidWire()
		}
		for _, value := range values {
			if exactValue(value, t.Elem()) != nil {
				return invalidWire()
			}
		}
	case reflect.Map, reflect.Interface:
		return invalidWire()
	}
	return nil
}
