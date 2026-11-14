package datasets

import (
	"bytes"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"maps"
	"slices"
	"unicode/utf8"
)

const (
	cleaningPreviewRowCeiling  = 100
	cleaningPreviewByteCeiling = 1 << 20
)

// SourceCleaningPolicy selects exact top-level fields. Trimming affects only
// strings. The blank filter also excludes absent and null values.
// PreviewBytes bounds the total before/after JSON payload. Set both preview
// limits to zero to disable samples.
type SourceCleaningPolicy struct {
	TrimFields         []string
	KeepNonBlankFields []string
	PreviewRows        int
	PreviewBytes       int
}

// SourceCleaningSample pairs a complete source row with the normalized output.
// Filtered rows have no After value. Samples contain private source values.
type SourceCleaningSample struct {
	Line   int64          `json:"line,omitempty"`
	Row    int64          `json:"row,omitempty"`
	Before jsontext.Value `json:"before"`
	After  jsontext.Value `json:"after,omitempty"`
}

// SourceCleaningReport separates filtering from validation rejection. Samples
// are available only on success and require a separate authorized projection.
type SourceCleaningReport struct {
	ChangedRows      int64                  `json:"changed_rows"`
	FilteredRows     int64                  `json:"filtered_rows"`
	SamplesTruncated bool                   `json:"samples_truncated"`
	Samples          []SourceCleaningSample `json:"-"`
}

func (p *SourceCleaningPolicy) valid(fields int) bool {
	if p == nil {
		return true
	}
	if (len(p.TrimFields) == 0 && len(p.KeepNonBlankFields) == 0) ||
		p.PreviewRows < 0 || p.PreviewRows > cleaningPreviewRowCeiling ||
		p.PreviewBytes < 0 || p.PreviewBytes > cleaningPreviewByteCeiling ||
		(p.PreviewRows == 0) != (p.PreviewBytes == 0) {
		return false
	}
	for _, names := range [][]string{p.TrimFields, p.KeepNonBlankFields} {
		if len(names) > fields {
			return false
		}
		seen := make(map[string]struct{}, len(names))
		for _, name := range names {
			if name == "" || len(name) > importFieldNameBytes || !utf8.ValidString(name) {
				return false
			}
			if _, exists := seen[name]; exists {
				return false
			}
			seen[name] = struct{}{}
		}
	}
	return true
}

type sourceCleaningState struct {
	policy      SourceCleaningPolicy
	report      SourceCleaningReport
	sampleBytes int
}

func newSourceCleaning(policy *SourceCleaningPolicy) *sourceCleaningState {
	if policy == nil {
		return nil
	}
	selected := *policy
	selected.TrimFields = slices.Clone(policy.TrimFields)
	selected.KeepNonBlankFields = slices.Clone(policy.KeepNonBlankFields)
	return &sourceCleaningState{policy: selected}
}

func (s *sourceCleaningState) keep(row map[string]jsontext.Value) (bool, error) {
	for _, name := range s.policy.KeepNonBlankFields {
		value := row[name]
		switch value.Kind() {
		case jsontext.KindInvalid, jsontext.KindNull:
			return false, nil
		case jsontext.KindString:
			text, err := jsontext.AppendUnquote(nil, value)
			if err != nil {
				return false, err
			}
			if len(bytes.TrimSpace(text)) == 0 {
				return false, nil
			}
		}
	}
	return true, nil
}

func (s *sourceCleaningState) trim(row map[string]jsontext.Value) (map[string]jsontext.Value, error) {
	var changed map[string]jsontext.Value
	for _, name := range s.policy.TrimFields {
		value := row[name]
		if value.Kind() != jsontext.KindString {
			continue
		}
		text, err := jsontext.AppendUnquote(nil, value)
		if err != nil {
			return nil, err
		}
		trimmed := bytes.TrimSpace(text)
		if len(trimmed) == len(text) {
			continue
		}
		if changed == nil {
			changed = maps.Clone(row)
		}
		changed[name], err = jsontext.AppendQuote(nil, string(trimmed))
		if err != nil {
			return nil, err
		}
	}
	return changed, nil
}

func (s *sourceCleaningState) sample(position SourceIssue, row map[string]jsontext.Value, after []byte) {
	if s.policy.PreviewRows == 0 {
		return
	}
	remaining := s.policy.PreviewBytes - s.sampleBytes
	if len(s.report.Samples) == s.policy.PreviewRows || len(after) >= remaining {
		s.report.SamplesTruncated = true
		return
	}
	before, err := json.Marshal(row, json.Deterministic(true))
	if err != nil || len(before) > remaining-len(after) {
		s.report.SamplesTruncated = true
		return
	}
	s.report.Samples = append(s.report.Samples, SourceCleaningSample{
		Line: position.Line, Row: position.Row, Before: before, After: after,
	})
	s.sampleBytes += len(before) + len(after)
}
