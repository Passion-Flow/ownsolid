package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

const RowSchema = "projectone.text-sft.v1"
const ProcessorVersion = "0.8.0"
const SplitAlgorithm = "source-union-sha256.v1"

type Record struct {
	SourceID   string `json:"source_id"`
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

func DecodeRecord(raw []byte, limits Limits) (Record, error) {
	var out Record
	if int64(len(raw)) > limits.MaximumLineBytes || !ValidJSONUnicode(raw) || strictKeys(raw, reflect.TypeFor[Record]()) != nil {
		return out, errors.New("DATASET_FIELDS_INVALID")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if unique(d) != nil {
		return out, errors.New("DATASET_JSON_INVALID")
	}
	if _, e := d.Token(); e != io.EOF {
		return out, errors.New("DATASET_JSON_INVALID")
	}
	if json.Unmarshal(raw, &out) != nil {
		return out, errors.New("DATASET_FIELDS_INVALID")
	}
	return out, out.Validate(limits)
}

func CanonicalStrings(values []string) ([]byte, error) { return canonical(values) }

func textValid(value string, maximum int64, source bool) bool {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || int64(len(value)) > maximum || source && strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if (r < 32 || r >= 127 && r <= 159) && (source || r != '\t' && r != '\r' && r != '\n') {
			return false
		}
	}
	return true
}
func (v Record) Validate(limits Limits) error {
	if !textValid(v.SourceID, limits.MaximumSourceBytes, true) {
		return errors.New("DATASET_SOURCE_INVALID")
	}
	if !textValid(v.Prompt, limits.MaximumTextBytes, false) || !textValid(v.Completion, limits.MaximumTextBytes, false) {
		return errors.New("DATASET_TEXT_INVALID")
	}
	b, e := canonical(v)
	if e != nil || int64(len(b)) > limits.MaximumLineBytes {
		return errors.New("DATASET_LINE_TOO_LARGE")
	}
	return nil
}

// canonical matches Python's compact ensure_ascii=False JSON for these typed records/string arrays.
func canonical(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if e := encoder.Encode(value); e != nil {
		return nil, e
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	out := make([]byte, 0, len(encoded))
	for i := 0; i < len(encoded); {
		if encoded[i] == '\\' && i+1 < len(encoded) {
			if i+6 <= len(encoded) && (string(encoded[i:i+6]) == `\u2028` || string(encoded[i:i+6]) == `\u2029`) {
				r := rune(0x2028)
				if encoded[i+5] == '9' {
					r = 0x2029
				}
				out = utf8.AppendRune(out, r)
				i += 6
				continue
			}
			// Escaped backslashes must remain escaped; a following literal u2028 is user text.
			out = append(out, encoded[i:i+2]...)
			i += 2
			continue
		}
		out = append(out, encoded[i])
		i++
	}
	return out, nil
}
func (v Record) Canonical(limits Limits) ([]byte, error) {
	if e := v.Validate(limits); e != nil {
		return nil, e
	}
	return canonical(v)
}
func (v Record) ContentDigest(limits Limits) (string, error) {
	if e := v.Validate(limits); e != nil {
		return "", e
	}
	b, e := canonical([]string{v.Prompt, v.Completion})
	if e != nil {
		return "", e
	}
	digest := sha256.Sum256(b)
	return hex.EncodeToString(digest[:]), nil
}

// ValidJSONUnicode rejects unpaired UTF-16 escapes before Go JSON v1 can replace them silently.
// Complete JSON syntax, exact fields, duplicates and bounds are checked by the calling decoder.
func ValidJSONUnicode(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	inside := false
	unit := func(start int) (uint64, bool) {
		if start+6 > len(body) || body[start] != '\\' || body[start+1] != 'u' {
			return 0, false
		}
		value, e := strconv.ParseUint(string(body[start+2:start+6]), 16, 16)
		return value, e == nil
	}
	for i := 0; i < len(body); {
		if body[i] == '"' {
			inside = !inside
			i++
			continue
		}
		if inside && body[i] == '\\' {
			if i+1 >= len(body) {
				return false
			}
			if body[i+1] == 'u' {
				first, ok := unit(i)
				if !ok {
					return false
				}
				if first >= 0xD800 && first <= 0xDBFF {
					second, ok := unit(i + 6)
					if !ok || second < 0xDC00 || second > 0xDFFF {
						return false
					}
					i += 12
					continue
				}
				if first >= 0xDC00 && first <= 0xDFFF {
					return false
				}
				i += 6
				continue
			}
			i += 2
			continue
		}
		i++
	}
	return !inside
}
