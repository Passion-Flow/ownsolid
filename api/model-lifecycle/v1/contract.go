package v1

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

//go:embed recipe.json
var recipeBytes []byte

//go:embed processor.json
var processorBytes []byte

//go:embed worker-errors.json
var workerErrors []byte

type recipeDefinition struct {
	Instruction string            `json:"instruction"`
	Separator   string            `json:"instruction_separator"`
	Labels      []string          `json:"labels"`
	Whitespace  []int32           `json:"prediction_whitespace_code_points"`
	TiedAliases map[string]string `json:"serialized_tied_weight_aliases"`
}

func definition() recipeDefinition {
	var value recipeDefinition
	if json.Unmarshal(recipeBytes, &value) != nil || len(value.Labels) != 3 {
		panic("COMPILED_MODEL_LIFECYCLE_RECIPE_INVALID")
	}
	return value
}
func Labels() []string                     { return append([]string(nil), definition().Labels...) }
func TiedWeightAliases() map[string]string { return definition().TiedAliases }
func RecipeDigest() string                 { return canonicalDigest(recipeBytes) }
func ProcessorDigest() string              { return canonicalDigest(processorBytes) }
func canonicalDigest(raw []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		panic("COMPILED_MODEL_LIFECYCLE_CONTRACT_INVALID")
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(value) != nil {
		panic("COMPILED_MODEL_LIFECYCLE_CONTRACT_INVALID")
	}
	sum := sha256.Sum256(bytes.TrimSuffix(output.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])
}
func PromptFingerprint(prompt string) (string, error) {
	if !utf8.ValidString(prompt) {
		return "", errors.New("MODEL_PREPARATION_INPUT_INVALID")
	}
	value := strings.ReplaceAll(strings.ReplaceAll(prompt, "\r\n", "\n"), "\r", "\n")
	sum := sha256.Sum256([]byte(strings.Trim(value, " \t\n")))
	return hex.EncodeToString(sum[:]), nil
}
func InstructionPrompt(prompt string) string {
	d := definition()
	return d.Instruction + d.Separator + prompt
}
func PredictionLabel(raw string) *string {
	d := definition()
	value := strings.TrimFunc(raw, func(r rune) bool {
		for _, point := range d.Whitespace {
			if r == point {
				return true
			}
		}
		return false
	})
	value = strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, value)
	for _, label := range d.Labels {
		if value == label {
			return &value
		}
	}
	return nil
}
func DecodeStrict(raw []byte, out any) error {
	if training.DecodeStrict(raw, out) != nil {
		return errors.New("MODEL_PREPARATION_WIRE_INVALID")
	}
	return nil
}
func DecodeConfiguration(raw []byte) (Configuration, error) {
	var out Configuration
	if DecodeStrict(raw, &out) != nil {
		return out, errors.New("MODEL_PREPARATION_CONFIGURATION_INVALID")
	}
	return out, out.Validate()
}
func QualityGate(base, candidate, rows, minimum, regression int64) (bool, error) {
	if rows < 1 || rows > MaximumRows || base < 0 || base > rows || candidate < 0 || candidate > rows || minimum < 0 || minimum > 10000 || regression < 0 || regression > 10000 {
		return false, errors.New("MODEL_PREPARATION_REPORT_INVALID")
	}
	return candidate*10000 >= minimum*rows && (base-candidate)*10000 <= regression*rows, nil
}

func InstructionDigest() string {
	sum := sha256.Sum256([]byte(definition().Instruction))
	return hex.EncodeToString(sum[:])
}
func WorkerErrorSupported(code string) bool {
	var value struct {
		SchemaVersion int      `json:"schema_version"`
		Codes         []string `json:"codes"`
	}
	if json.Unmarshal(workerErrors, &value) != nil {
		panic("COMPILED_MODEL_LIFECYCLE_ERRORS_INVALID")
	}
	for _, allowed := range value.Codes {
		if code == allowed {
			return true
		}
	}
	return false
}
