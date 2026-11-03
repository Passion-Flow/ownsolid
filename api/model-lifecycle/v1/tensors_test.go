package v1

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"testing"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// Tiny byte fixtures test only parser/verification invariants, never model executability or hardware support.
func weightFixture(values map[string][]uint16) []byte {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	header := map[string]any{"__metadata__": map[string]string{"format": "pt"}}
	var payload bytes.Buffer
	for _, name := range names {
		start := payload.Len()
		for _, value := range values[name] {
			binary.Write(&payload, binary.LittleEndian, value)
		}
		header[name] = tensorDescriptor{Dtype: "BF16", Shape: []int64{int64(len(values[name]))}, Offsets: []int64{int64(start), int64(payload.Len())}}
	}
	raw, _ := json.Marshal(header)
	var result bytes.Buffer
	binary.Write(&result, binary.LittleEndian, uint64(len(raw)))
	result.Write(raw)
	result.Write(payload.Bytes())
	return result.Bytes()
}
func digestBytes(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func TestIndependentBF16VerifierRejectsNonfiniteNonTargetAliasAndUnchangedOutput(t *testing.T) {
	baseValues := map[string][]uint16{"lm_head.weight": {0x3f80, 0x4000}, "model.embed_tokens.weight": {0x3f80, 0x4000}, "model.layers.0.self_attn.q_proj.weight": {0x3f80}, "model.norm.weight": {0x3f80}}
	base := weightFixture(baseValues)
	expected := dataset.Asset{Path: "model.safetensors", SizeBytes: int64(len(base)), SHA256: digestBytes(base)}
	candidateValues := func() map[string][]uint16 {
		return map[string][]uint16{"model.embed_tokens.weight": {0x3f80, 0x4000}, "model.layers.0.self_attn.q_proj.weight": {0x4000}, "model.norm.weight": {0x3f80}}
	}
	check := func(values map[string][]uint16) (WeightVerification, error) {
		candidate := weightFixture(values)
		return VerifyMergedWeights(context.Background(), bytes.NewReader(base), int64(len(base)), expected, bytes.NewReader(candidate), int64(len(candidate)), true)
	}
	result, e := check(candidateValues())
	if e != nil || len(result.ChangedTargets) != 1 || result.ChangedTargets[0] != "model.layers.0.self_attn.q_proj.weight" {
		t.Fatal("valid tiny byte fixture", e)
	}
	for name, mutation := range map[string]func(map[string][]uint16){
		"nonfinite":  func(v map[string][]uint16) { v["model.norm.weight"][0] = 0x7f80 },
		"non-target": func(v map[string][]uint16) { v["model.norm.weight"][0] = 0x4000 },
		"unchanged":  func(v map[string][]uint16) { v["model.layers.0.self_attn.q_proj.weight"][0] = 0x3f80 },
		"missing":    func(v map[string][]uint16) { delete(v, "model.norm.weight") },
		"extra":      func(v map[string][]uint16) { v["unknown.weight"] = []uint16{0x3f80} },
		"alias":      func(v map[string][]uint16) { v["lm_head.weight"] = []uint16{0x3f80, 0x4000} },
		"shape":      func(v map[string][]uint16) { v["model.norm.weight"] = []uint16{0x3f80, 0x3f80} },
	} {
		t.Run(name, func(t *testing.T) {
			v := candidateValues()
			mutation(v)
			if _, e := check(v); e == nil {
				t.Fatal("mutated output accepted")
			}
		})
	}
	wrong := expected
	wrong.SHA256 = digestBytes([]byte("wrong"))
	candidate := weightFixture(candidateValues())
	if _, e := VerifyMergedWeights(context.Background(), bytes.NewReader(base), int64(len(base)), wrong, bytes.NewReader(candidate), int64(len(candidate)), true); e == nil {
		t.Fatal("changed source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := VerifyMergedWeights(ctx, bytes.NewReader(base), int64(len(base)), expected, bytes.NewReader(candidate), int64(len(candidate)), true); !errors.Is(e, context.Canceled) {
		t.Fatal("context ignored", e)
	}
}
func TestBF16EnvelopeRejectsDuplicateFieldsGapOverlapWrongTypeAndOverflow(t *testing.T) {
	headers := []string{
		`{"__metadata__":{"format":"pt"},"a":{"dtype":"BF16","shape":[1],"data_offsets":[0,2]},"a":{"dtype":"BF16","shape":[1],"data_offsets":[0,2]}}`,
		`{"__metadata__":{"format":"pt"},"a":{"dtype":"BF16","shape":[true],"data_offsets":[0,2]}}`,
		`{"__metadata__":{"format":"pt"},"a":{"dtype":"F32","shape":[1],"data_offsets":[0,2]}}`,
		`{"__metadata__":{"format":"pt"},"a":{"dtype":"BF16","shape":[1],"data_offsets":[1,3]}}`,
		`{"__metadata__":{"format":"pt"},"a":{"dtype":"BF16","shape":[1],"data_offsets":[0,2]},"b":{"dtype":"BF16","shape":[1],"data_offsets":[0,2]}}`,
		`{"__metadata__":{"format":"pt"},"a":{"dtype":"BF16","shape":[9223372036854775807,2],"data_offsets":[0,2]}}`,
		`{"__metadata__":{"format":"pt"},"a":{"dtype":"BF16","shape":[1],"data_offsets":[0,2],"unknown":0}}`,
	}
	for index, header := range headers {
		var raw bytes.Buffer
		binary.Write(&raw, binary.LittleEndian, uint64(len(header)))
		raw.WriteString(header)
		raw.Write([]byte{0, 0, 0, 0})
		if _, e := tensorEnvelope(bytes.NewReader(raw.Bytes()), int64(raw.Len())); e == nil {
			t.Fatal("invalid envelope", index)
		}
	}
}
