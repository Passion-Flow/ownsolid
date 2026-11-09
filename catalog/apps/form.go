package apps

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	valueTextRunes   = 4096
	licenseNoteRunes = 2048
)

// Messages contains display text for one locale.
type Messages struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Fields      map[string]FieldText `json:"fields"`
	// LicenseNote is the publisher's optional plain-text release summary.
	LicenseNote *string `json:"license_note,omitempty"`
}

// FieldText supplies plain-text labels for a form field and its choices.
type FieldText struct {
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Choices     map[string]string `json:"choices,omitempty"`
}

func (m Messages) valid(form []Field) bool {
	if !validText(m.Title, 120) || !validText(m.Description, 1024) || m.Fields == nil || len(m.Fields) != len(form) {
		return false
	}
	for _, field := range form {
		msg, ok := m.Fields[field.ID]
		if !ok || !validText(msg.Label, 120) || !validText(msg.Description, 1024) || len(msg.Choices) != len(field.Choices) {
			return false
		}
		for _, choice := range field.Choices {
			if !validText(msg.Choices[choice], 120) {
				return false
			}
		}
	}
	return true
}

func validText(text string, maxRunes int) bool {
	if text == "" || strings.TrimSpace(text) != text || !utf8.ValidString(text) || utf8.RuneCountInString(text) > maxRunes {
		return false
	}
	return !strings.ContainsFunc(text, unicode.IsControl)
}

// DecodeValues validates the configuration for a release and returns only declared
// scalars (string, int64 or bool). Omitted optional fields stay absent.
func (m Manifest) DecodeValues(raw []byte) (map[string]any, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	var submitted map[string]jsontext.Value
	if len(raw) > documentBytes || json.Unmarshal(raw, &submitted) != nil || submitted == nil {
		return nil, fmt.Errorf("%w: submit one bounded JSON object", ErrValues)
	}
	values := make(map[string]any, len(submitted))
	for _, field := range m.Form {
		value, present := submitted[field.ID]
		if !present {
			if *field.Required {
				return nil, fmt.Errorf("%w: supply %s", ErrValues, field.ID)
			}
			continue
		}
		decoded, ok := field.decode(value)
		if !ok {
			return nil, fmt.Errorf("%w: supply a valid %s for %s", ErrValues, field.Type, field.ID)
		}
		values[field.ID] = decoded
	}
	if len(values) != len(submitted) {
		return nil, fmt.Errorf("%w: submit only declared form fields", ErrValues)
	}
	return values, nil
}

func (f Field) decode(raw jsontext.Value) (any, bool) {
	switch f.Type {
	case "text", "choice":
		var value string
		if raw.Kind() != '"' || json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		if f.Type == "choice" {
			return value, slices.Contains(f.Choices, value)
		}
		return value, validText(value, valueTextRunes)
	case "integer":
		var value int64
		if raw.Kind() != '0' || json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		return value, true
	case "boolean":
		switch raw.Kind() {
		case 't':
			return true, true
		case 'f':
			return false, true
		}
	}
	return nil, false
}
