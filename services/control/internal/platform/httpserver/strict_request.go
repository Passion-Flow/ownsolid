package httpserver

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"reflect"
	"strings"
)

// DecodeRequiredObject accepts the exact required JSON fields of a request DTO.
// Explicit empty objects are supported; null, duplicate/case variants, trailing
// values and unknown fields never become default-valued mutation requests.
func DecodeRequiredObject(r *http.Request, value any, maximum int64) error {
	return decodeObject(r, value, maximum, func(reflect.StructField) bool { return true })
}

// DecodePatchObject is DecodeRequiredObject for partial updates: a field tagged omitempty
// may be absent. A field that is present still follows the same rules and is never null.
func DecodePatchObject(r *http.Request, value any, maximum int64) error {
	return decodeObject(r, value, maximum, func(field reflect.StructField) bool {
		return !strings.Contains(field.Tag.Get("json"), ",omitempty")
	})
}

func decodeObject(r *http.Request, value any, maximum int64, required func(reflect.StructField) bool) error {
	if r == nil || r.Body == nil {
		return errors.New("VALIDATION_FAILED")
	}
	typ := reflect.TypeOf(value)
	if typ == nil || typ.Kind() != reflect.Pointer || reflect.ValueOf(value).IsNil() || typ.Elem().Kind() != reflect.Struct || maximum < 2 || maximum > MaxJSONBytes {
		return errors.New("VALIDATION_FAILED")
	}
	media, parameters, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" || len(parameters) > 1 || len(parameters) == 1 && parameters["charset"] != "utf-8" {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, maximum+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if int64(len(raw)) > maximum {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	var fields map[string]json.RawMessage
	if strict.DecodeStrict(raw, &fields) != nil || fields == nil {
		return errors.New("VALIDATION_FAILED")
	}
	known := 0
	for i := 0; i < typ.Elem().NumField(); i++ {
		field := typ.Elem().Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		v, ok := fields[name]
		if name == "" || name == "-" || !ok && required(field) || ok && string(v) == "null" {
			return errors.New("VALIDATION_FAILED")
		}
		if ok {
			known++
		}
	}
	if known != len(fields) || strict.DecodeStrict(raw, value) != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
