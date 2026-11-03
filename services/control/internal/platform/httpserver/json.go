package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"
)

// MaxJSONBytes is the shared versioned API request envelope limit.
const MaxJSONBytes = 16 * 1024

func DecodeJSON(r *http.Request, v any) error {
	media, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return errors.New("UNSUPPORTED_MEDIA_TYPE")
	}
	b, e := io.ReadAll(io.LimitReader(r.Body, MaxJSONBytes+1))
	if e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if len(b) > MaxJSONBytes {
		return errors.New("PAYLOAD_TOO_LARGE")
	}
	if !utf8.Valid(b) {
		return errors.New("VALIDATION_FAILED")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if e = unique(d); e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	if _, e = d.Token(); e != io.EOF {
		return errors.New("VALIDATION_FAILED")
	}
	strict := json.NewDecoder(bytes.NewReader(b))
	strict.DisallowUnknownFields()
	if e = strict.Decode(v); e != nil {
		return errors.New("VALIDATION_FAILED")
	}
	return nil
}
func unique(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		if t == nil {
			return errors.New("NULL_REJECTED")
		}
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := k.(string)
			if !ok || keys[s] {
				return errors.New("DUPLICATE_JSON_KEY")
			}
			keys[s] = true
			if e = unique(d); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = unique(d); e != nil {
				return e
			}
		}
	default:
		return errors.New("JSON_INVALID")
	}
	_, e = d.Token()
	return e
}
