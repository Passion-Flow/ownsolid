package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
)

const objectStorageEnvironmentPrefix = "PROJECT_ONE_OBJECT_STORAGE_"

func objectStorageEnvironment(key string) bool {
	if !strings.HasPrefix(key, objectStorageEnvironmentPrefix) {
		return false
	}
	field := strings.TrimPrefix(key, objectStorageEnvironmentPrefix)
	typ := reflect.TypeOf(ObjectStorageConfiguration{})
	for i := 0; i < typ.NumField(); i++ {
		if strings.ToUpper(typ.Field(i).Tag.Get("yaml")) == field {
			return true
		}
	}
	return false
}
func (c *Config) applyObjectStorageEnvironment(key, value string) error {
	bad := errors.New("CONFIG_OBJECT_STORAGE_ENVIRONMENT_INVALID")
	if !objectStorageEnvironment(key) || value == "" {
		return bad
	}
	if c.ObjectStorage == nil {
		c.ObjectStorage = &ObjectStorageConfiguration{}
	}
	body := reflect.ValueOf(c.ObjectStorage).Elem()
	typ := body.Type()
	name := strings.TrimPrefix(key, objectStorageEnvironmentPrefix)
	for i := 0; i < typ.NumField(); i++ {
		if strings.ToUpper(typ.Field(i).Tag.Get("yaml")) != name {
			continue
		}
		v := body.Field(i)
		switch v.Kind() {
		case reflect.Bool:
			if value != "true" && value != "false" {
				return bad
			}
			v.SetBool(value == "true")
		case reflect.Int, reflect.Int64:
			for _, r := range value {
				if r < '0' || r > '9' {
					return bad
				}
			}
			n, e := strconv.ParseInt(value, 10, 64)
			if e != nil {
				return bad
			}
			v.SetInt(n)
		case reflect.String:
			v.SetString(value)
		case reflect.Slice:
			if canonicalObjectStorageJSON(value, v.Type()) != nil {
				return bad
			}
			target := reflect.New(v.Type())
			decoder := json.NewDecoder(bytes.NewBufferString(value))
			decoder.DisallowUnknownFields()
			if decoder.Decode(target.Interface()) != nil || decoder.Decode(new(any)) != io.EOF || target.Elem().IsNil() {
				return bad
			}
			v.Set(target.Elem())

		default:
			return bad
		}
		return nil
	}
	return bad
}

// Check canonical field spelling and duplicates before decoding typed values.
// encoding/json alone accepts case aliases and duplicate object members.
func canonicalObjectStorageJSON(raw string, typ reflect.Type) error {
	d := json.NewDecoder(strings.NewReader(raw))
	var walk func(reflect.Type) error
	bad := errors.New("CONFIG_OBJECT_STORAGE_ENVIRONMENT_INVALID")
	walk = func(t reflect.Type) error {
		token, e := d.Token()
		if e != nil {
			return bad
		}
		switch t.Kind() {
		case reflect.Slice:
			if token != json.Delim('[') {
				return bad
			}
			for d.More() {
				if walk(t.Elem()) != nil {
					return bad
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return bad
			}
		case reflect.Struct:
			if token != json.Delim('{') {
				return bad
			}
			fields := map[string]reflect.Type{}
			seen := map[string]bool{}
			for i := 0; i < t.NumField(); i++ {
				fields[t.Field(i).Tag.Get("json")] = t.Field(i).Type
			}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return bad
				}
				name, ok := key.(string)
				field, known := fields[name]
				if !ok || !known || seen[name] {
					return bad
				}
				seen[name] = true
				if walk(field) != nil {
					return bad
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return bad
			}
		case reflect.String:
			if _, ok := token.(string); !ok {
				return bad
			}
		default:
			return bad
		}
		return nil
	}
	if walk(typ) != nil {
		return bad
	}
	if _, e := d.Token(); e != io.EOF {
		return bad
	}
	return nil
}
