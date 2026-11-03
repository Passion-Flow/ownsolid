package v1

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const tensorScanBytes = 65536
const bfloat16Bytes int64 = 2
const bfloat16ExponentMask uint16 = 0x7f80

var targetTensorName = regexp.MustCompile(`^model\.layers\.[0-9]+\.self_attn\.(q_proj|v_proj)\.weight$`)

type tensorDescriptor struct {
	Dtype   string  `json:"dtype"`
	Shape   []int64 `json:"shape"`
	Offsets []int64 `json:"data_offsets"`
}
type tensorSection struct {
	Shape  []int64
	Offset int64
	Bytes  int64
}
type WeightVerification struct {
	SHA256         string
	SizeBytes      int64
	ChangedTargets []string
}

func tensorError() error { return errors.New("MODEL_PREPARATION_TENSOR_INVALID") }

func tensorEnvelope(file io.ReaderAt, size int64) (map[string]tensorSection, error) {
	if file == nil || size < 10 || size > MaximumModelBytes {
		return nil, tensorError()
	}
	var prefix [8]byte
	if _, e := file.ReadAt(prefix[:], 0); e != nil {
		return nil, tensorError()
	}
	length := binary.LittleEndian.Uint64(prefix[:])
	if length < 2 || length > uint64(MaximumTensorHeaderBytes) || length >= uint64(size-8) {
		return nil, tensorError()
	}
	raw := make([]byte, int(length))
	if _, e := file.ReadAt(raw, 8); e != nil || raw[0] != '{' {
		return nil, tensorError()
	}
	var first map[string]json.RawMessage
	if json.Unmarshal(raw, &first) != nil || len(first) < 2 {
		return nil, tensorError()
	}
	keys := make([]string, 0, len(first))
	for key := range first {
		keys = append(keys, key)
	}
	values, e := training.ExactObject(raw, keys)
	if e != nil {
		return nil, tensorError()
	}
	var metadata struct {
		Format string `json:"format"`
	}
	if DecodeStrict(values["__metadata__"], &metadata) != nil || metadata.Format != "pt" {
		return nil, tensorError()
	}
	sections := map[string]tensorSection{}
	for name, value := range values {
		if name == "__metadata__" {
			continue
		}
		var item tensorDescriptor
		if DecodeStrict(value, &item) != nil || item.Dtype != "BF16" || len(item.Shape) < 1 || len(item.Shape) > 4 || len(item.Offsets) != 2 || item.Offsets[0] < 0 || item.Offsets[1] < item.Offsets[0] {
			return nil, tensorError()
		}
		count := int64(1)
		for _, dimension := range item.Shape {
			if dimension < 1 || dimension > math.MaxInt64/count {
				return nil, tensorError()
			}
			count *= dimension
		}
		if count > math.MaxInt64/bfloat16Bytes || item.Offsets[1]-item.Offsets[0] != count*bfloat16Bytes || item.Offsets[1] > size-8-int64(length) {
			return nil, tensorError()
		}
		sections[name] = tensorSection{Shape: item.Shape, Offset: 8 + int64(length) + item.Offsets[0], Bytes: count * bfloat16Bytes}
	}
	ordered := make([]tensorSection, 0, len(sections))
	for _, item := range sections {
		ordered = append(ordered, item)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Offset < ordered[j].Offset })
	cursor := 8 + int64(length)
	for _, item := range ordered {
		if item.Offset != cursor {
			return nil, tensorError()
		}
		cursor += item.Bytes
	}
	if cursor != size {
		return nil, tensorError()
	}
	return sections, nil
}

func sectionDigest(ctx context.Context, file io.ReaderAt, offset, size int64, finite bool) (string, error) {
	hash := sha256.New()
	reader := io.NewSectionReader(file, offset, size)
	buffer := make([]byte, tensorScanBytes)
	remaining := size
	for remaining > 0 {
		if e := ctx.Err(); e != nil {
			return "", e
		}
		count, e := io.ReadFull(reader, buffer[:min(int64(len(buffer)), remaining)])
		if e != nil {
			return "", tensorError()
		}
		if finite {
			if count%int(bfloat16Bytes) != 0 {
				return "", tensorError()
			}
			for position := 0; position < count; position += int(bfloat16Bytes) {
				if binary.LittleEndian.Uint16(buffer[position:position+int(bfloat16Bytes)])&bfloat16ExponentMask == bfloat16ExponentMask {
					return "", errors.New("MODEL_PREPARATION_NONFINITE_TENSOR")
				}
			}
		}
		hash.Write(buffer[:count])
		remaining -= int64(count)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// VerifyMergedWeights reads no executable format. expectedBase must come from the owning module's approved immutable catalog.
// The caller supplies actual opened-file sizes, owns protected file descriptors and rechecks current authority before accepting references.
func VerifyMergedWeights(ctx context.Context, base io.ReaderAt, baseSize int64, expectedBase dataset.Asset, candidate io.ReaderAt, candidateSize int64, tied bool) (WeightVerification, error) {
	var result WeightVerification
	if ctx == nil || !tied || expectedBase.Path != "model.safetensors" || expectedBase.SizeBytes != baseSize || !dataset.ValidDigest(expectedBase.SHA256) {
		return result, tensorError()
	}
	if e := ctx.Err(); e != nil {
		return result, e
	}
	original, e := tensorEnvelope(base, baseSize)
	if e != nil {
		return result, e
	}
	merged, e := tensorEnvelope(candidate, candidateSize)
	if e != nil {
		return result, e
	}
	aliases := TiedWeightAliases()
	if len(merged) != len(original)-len(aliases) {
		return result, tensorError()
	}
	for name, section := range merged {
		source, present := original[name]
		if !present || !reflect.DeepEqual(section.Shape, source.Shape) {
			return result, tensorError()
		}
	}
	for alias, source := range aliases {
		a, aExists := original[alias]
		b, bExists := original[source]
		if !aExists || !bExists || !reflect.DeepEqual(a.Shape, b.Shape) {
			return result, tensorError()
		}
		if _, present := merged[alias]; present {
			return result, tensorError()
		}
	}
	actualBase, e := sectionDigest(ctx, base, 0, baseSize, false)
	if e != nil {
		return result, e
	}
	if actualBase != expectedBase.SHA256 {
		return result, errors.New("MODEL_PREPARATION_INPUT_CHANGED")
	}
	originalDigests := map[string]string{}
	for name, section := range original {
		sum, e := sectionDigest(ctx, base, section.Offset, section.Bytes, false)
		if e != nil {
			return result, e
		}
		originalDigests[name] = sum
	}
	for alias, source := range aliases {
		if originalDigests[alias] != originalDigests[source] {
			return result, tensorError()
		}
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		section := merged[name]
		sum, e := sectionDigest(ctx, candidate, section.Offset, section.Bytes, true)
		if e != nil {
			return result, e
		}
		if sum != originalDigests[name] {
			if !targetTensorName.MatchString(name) {
				return result, errors.New("MODEL_PREPARATION_NON_TARGET_CHANGED")
			}
			result.ChangedTargets = append(result.ChangedTargets, name)
		}
	}
	if len(result.ChangedTargets) == 0 {
		return result, errors.New("MODEL_PREPARATION_UNCHANGED_MODEL")
	}
	sum, e := sectionDigest(ctx, candidate, 0, candidateSize, false)
	if e != nil {
		return result, e
	}
	result.SHA256 = sum
	result.SizeBytes = candidateSize
	return result, nil
}
