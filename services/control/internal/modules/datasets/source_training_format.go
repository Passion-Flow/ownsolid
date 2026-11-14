package datasets

import (
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"fmt"
	"strings"
)

// SourceTrainingFormat selects a row schema independently of source file syntax.
type SourceTrainingFormat string

const (
	SourceTrainingText        SourceTrainingFormat = "text"
	SourceTrainingMessages    SourceTrainingFormat = "messages"
	SourceTrainingInstruction SourceTrainingFormat = "instruction"
	SourceTrainingPreference  SourceTrainingFormat = "preference"
)

func (f SourceTrainingFormat) valid() bool {
	switch f {
	case "", SourceTrainingText, SourceTrainingMessages, SourceTrainingInstruction, SourceTrainingPreference:
		return true
	default:
		return false
	}
}

// invalidField returns a fixed schema path without disclosing training text.
func (f SourceTrainingFormat) invalidField(row map[string]jsontext.Value) string {
	switch f {
	case SourceTrainingText:
		if _, ok := nonblankTrainingText(row["text"]); !ok {
			return "text"
		}
	case SourceTrainingMessages:
		return invalidTrainingMessages(row["messages"])
	case SourceTrainingInstruction:
		if _, ok := nonblankTrainingText(row["instruction"]); !ok {
			return "instruction"
		}
		if kind := row["input"].Kind(); kind != jsontext.KindInvalid && kind != jsontext.KindNull && kind != jsontext.KindString {
			return "input"
		}
		if _, ok := nonblankTrainingText(row["output"]); !ok {
			return "output"
		}
	case SourceTrainingPreference:
		if _, ok := nonblankTrainingText(row["prompt"]); !ok {
			return "prompt"
		}
		chosen, ok := nonblankTrainingText(row["chosen"])
		if !ok {
			return "chosen"
		}
		if rejected, ok := nonblankTrainingText(row["rejected"]); !ok || chosen == rejected {
			return "rejected"
		}
	}
	return ""
}

func nonblankTrainingText(raw jsontext.Value) (string, bool) {
	if raw.Kind() != jsontext.KindString {
		return "", false
	}
	var value string
	err := json.Unmarshal(raw, &value)
	return value, err == nil && strings.TrimSpace(value) != ""
}

func invalidTrainingMessages(raw jsontext.Value) string {
	var messages []jsontext.Value
	if raw.Kind() != jsontext.KindBeginArray || json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return "messages"
	}
	var user, assistant bool
	for i, item := range messages {
		var message struct {
			Role    string         `json:"role"`
			Content jsontext.Value `json:"content"`
		}
		if item.Kind() != jsontext.KindBeginObject || json.Unmarshal(item, &message, json.RejectUnknownMembers(true)) != nil {
			return fmt.Sprintf("messages[%d]", i)
		}
		switch message.Role {
		case "system":
			if i != 0 {
				return fmt.Sprintf("messages[%d].role", i)
			}
		case "user":
			user = true
		case "assistant":
			assistant = true
		default:
			return fmt.Sprintf("messages[%d].role", i)
		}
		if _, ok := nonblankTrainingText(message.Content); !ok {
			return fmt.Sprintf("messages[%d].content", i)
		}
	}
	if !user || !assistant {
		return "messages"
	}
	return ""
}
