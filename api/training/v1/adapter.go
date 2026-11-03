package v1

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
)

//go:embed adapter-template.json
var adapterTemplate []byte

func adapterError() error { return errors.New("TRAINING_ADAPTER_INVALID") }

// AdapterReadme contains only the public repository and manufacturer-owned qualification wording.
func AdapterReadme(repository string) []byte {
	return []byte(strings.ReplaceAll(AdapterReadmeTemplate, "{repository}", repository))
}

// ValidateAdapterConfiguration accepts only the pinned PEFT LoRA recipe, public frozen base and exact hyperparameters.
func ValidateAdapterConfiguration(raw []byte, c Configuration, repository string) error {
	if len(raw) > MaximumCheckpointMetadataBytes || c.Validate() != nil || repository == "" {
		return adapterError()
	}
	var expected map[string]json.RawMessage
	if json.Unmarshal(adapterTemplate, &expected) != nil {
		return adapterError()
	}
	var pinned string
	if json.Unmarshal(expected["peft_version"], &pinned) != nil || pinned != PinnedPackages().Peft {
		return adapterError()
	}
	keys := make([]string, 0, len(expected))
	for key := range expected {
		keys = append(keys, key)
	}
	values, e := ExactObject(raw, keys)
	if e != nil {
		return adapterError()
	}
	for name, value := range values {
		switch name {
		case "base_model_name_or_path":
			var actual string
			if json.Unmarshal(value, &actual) != nil || actual != repository {
				return adapterError()
			}
		case "r", "lora_alpha":
			var actual int64
			if json.Unmarshal(value, &actual) != nil {
				return adapterError()
			}
			wanted := c.Rank
			if name == "lora_alpha" {
				wanted = c.Alpha
			}
			if actual != wanted {
				return adapterError()
			}
		case "lora_dropout":
			var actual float64
			wanted, e := strconv.ParseFloat(c.Dropout, 64)
			if e != nil || json.Unmarshal(value, &actual) != nil || math.IsNaN(actual) || math.IsInf(actual, 0) || actual != wanted {
				return adapterError()
			}
		case "target_modules":
			var actual []string
			if json.Unmarshal(value, &actual) != nil || len(actual) != 2 {
				return adapterError()
			}
			sort.Strings(actual)
			if actual[0] != "q_proj" || actual[1] != "v_proj" {
				return adapterError()
			}
		default:
			var actual, wanted bytes.Buffer
			if json.Compact(&actual, value) != nil || json.Compact(&wanted, expected[name]) != nil || !bytes.Equal(actual.Bytes(), wanted.Bytes()) {
				return adapterError()
			}
		}
	}
	return nil
}
