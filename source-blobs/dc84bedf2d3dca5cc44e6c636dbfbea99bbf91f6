package v1

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type vectors struct {
	ApprovedCatalogSHA256 string `json:"approved_model_catalog_sha256"`
	Fingerprints          []struct {
		Prompt string `json:"prompt"`
		SHA256 string `json:"sha256"`
	} `json:"fingerprints"`
	Configuration       Configuration `json:"configuration"`
	ConfigurationSHA256 string        `json:"configuration_sha256"`
	RecipeSHA256        string        `json:"recipe_sha256"`
	Predictions         []struct {
		Output string  `json:"output"`
		Label  *string `json:"label"`
	} `json:"prediction_labels"`
}

func fixture(t *testing.T) vectors {
	t.Helper()
	raw, e := os.ReadFile("../../../tests/fixtures/model-lifecycle/vectors.json")
	if e != nil {
		t.Fatal(e)
	}
	var out vectors
	if e = json.Unmarshal(raw, &out); e != nil {
		t.Fatal(e)
	}
	return out
}
func TestCrossLanguagePromptRecipeConfigurationAndPredictionVectors(t *testing.T) {
	v := fixture(t)
	if ApprovedModelCatalogSHA256 != v.ApprovedCatalogSHA256 {
		t.Fatal("compiled approved catalog differs from actual recorded producer")
	}
	for _, item := range v.Fingerprints {
		actual, e := PromptFingerprint(item.Prompt)
		if e != nil || actual != item.SHA256 {
			t.Fatal("prompt fingerprint", e)
		}
	}
	if _, e := PromptFingerprint(string([]byte{0xff})); e == nil {
		t.Fatal("invalid UTF8 accepted")
	}
	digest, e := v.Configuration.Digest()
	if e != nil || digest != v.ConfigurationSHA256 {
		t.Fatal("configuration digest", e)
	}
	if RecipeDigest() != v.RecipeSHA256 {
		t.Fatal("recipe digest disagrees with canonical Python fixture")
	}
	for _, item := range v.Predictions {
		value := PredictionLabel(item.Output)
		if (value == nil) != (item.Label == nil) || value != nil && *value != *item.Label {
			t.Fatal("prediction normalization")
		}
	}
	if !strings.HasSuffix(InstructionPrompt("request"), "\n\nrequest") {
		t.Fatal("instruction wrapper")
	}
}
func TestStrictConfigurationRefusesCaseUnknownDuplicateMissingNullBooleanAndFraction(t *testing.T) {
	raw, _ := json.Marshal(DefaultConfiguration())
	mutations := map[string][]byte{
		"case":      []byte(strings.Replace(string(raw), "device", "Device", 1)),
		"unknown":   []byte(`{"unknown":1,` + string(raw[1:])),
		"duplicate": []byte(`{"device":"nvidia_bfloat16",` + string(raw[1:])),
		"missing":   []byte(strings.Replace(string(raw), `"device":"nvidia_bfloat16",`, "", 1)),
		"null":      []byte(strings.Replace(string(raw), `"threads":2`, `"threads":null`, 1)),
		"boolean":   []byte(strings.Replace(string(raw), `"threads":2`, `"threads":true`, 1)),
		"fraction":  []byte(strings.Replace(string(raw), `"threads":2`, `"threads":2.5`, 1)),
		"trailing":  append(append([]byte(nil), raw...), []byte(" {}")...),
	}
	for name, value := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, e := DecodeConfiguration(value); e == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if _, e := DecodeConfiguration(raw); e != nil {
		t.Fatal(e)
	}
}
func TestResourceAndConfigurationBoundsAndExactIntegerGate(t *testing.T) {
	good := DefaultResources()
	if e := good.Validate(); e != nil {
		t.Fatal(e)
	}
	bad := good
	bad.MaximumOutputFiles = 33
	if bad.Validate() == nil {
		t.Fatal("too many output files")
	}
	bad = good
	bad.MemoryBytes = 0
	if bad.Validate() == nil {
		t.Fatal("unbounded memory")
	}
	cfg := DefaultConfiguration()
	cfg.MaximumInputTokens = 1985
	if cfg.Validate() == nil {
		t.Fatal("context overflow")
	}
	cases := []struct {
		base, candidate, rows, minimum, regression int64
		expected                                   bool
	}{
		{2, 2, 3, 6666, 0, true}, {2, 2, 3, 6667, 0, false}, {3, 2, 3, 0, 3333, false}, {3, 2, 3, 0, 3334, true}, {1, 1, 3, 8000, 0, false},
	}
	for _, item := range cases {
		actual, e := QualityGate(item.base, item.candidate, item.rows, item.minimum, item.regression)
		if e != nil || actual != item.expected {
			t.Fatal("integer gate", item, e)
		}
	}
	for _, item := range [][5]int64{{-1, 0, 3, 0, 0}, {0, 4, 3, 0, 0}, {0, 0, 0, 0, 0}, {0, 0, 257, 0, 0}, {0, 0, 3, 10001, 0}} {
		if _, e := QualityGate(item[0], item[1], item[2], item[3], item[4]); e == nil {
			t.Fatal("invalid metric accepted", item)
		}
	}
}
func TestRecipeCollectionsCannotMutateCompiledContract(t *testing.T) {
	labels := Labels()
	labels[0] = "changed"
	if Labels()[0] != "account" {
		t.Fatal("mutable labels")
	}
	aliases := TiedWeightAliases()
	aliases["lm_head.weight"] = "changed"
	if TiedWeightAliases()["lm_head.weight"] != "model.embed_tokens.weight" {
		t.Fatal("mutable aliases")
	}
}
