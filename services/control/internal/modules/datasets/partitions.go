package datasets

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type assignment struct {
	LineNumber    int64  `json:"line_number"`
	SourceID      string `json:"source_id"`
	ContentSHA256 string `json:"content_sha256"`
	Split         string `json:"split"`
}
type duplicate struct {
	LineNumber     int64  `json:"line_number"`
	SourceID       string `json:"source_id"`
	KeptLineNumber int64  `json:"kept_line_number"`
	ContentSHA256  string `json:"content_sha256"`
	MatchRule      string `json:"match_rule,omitempty"`
}
type retainedRow struct {
	record wire.Record
	line   int64
	digest string
}
type sourceUnion map[string]string

func (u sourceUnion) find(s string) string {
	parent, ok := u[s]
	if !ok {
		u[s] = s
		return s
	}
	if parent != s {
		u[s] = u.find(parent)
	}
	return u[s]
}
func (u sourceUnion) union(a, b string) {
	a = u.find(a)
	b = u.find(b)
	if a > b {
		a, b = b, a
	}
	u[b] = a
}

func readLines(path string, maximumLine, maximumRows int64, accept func(int64, []byte) error) error {
	file, e := os.Open(path)
	if e != nil {
		return failure("DATASET_FILE_INVALID")
	}
	defer file.Close()
	return scanLines(file, maximumLine, maximumRows, accept)
}

func scanLines(input io.Reader, maximumLine, maximumRows int64, accept func(int64, []byte) error) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), int(maximumLine)+2)
	line := int64(0)
	for scanner.Scan() {
		line++
		if line > maximumRows || int64(len(scanner.Bytes())) > maximumLine {
			return failure("DATASET_FILE_INVALID")
		}
		if e := accept(line, scanner.Bytes()); e != nil {
			return failure("DATASET_FILE_INVALID")
		}
	}
	if scanner.Err() != nil {
		return failure("DATASET_FILE_INVALID")
	}
	return nil
}

func verifyPartitions(request wire.ProcessorRequest, manifest wire.Manifest, directory string) error {
	retained := []retainedRow{}
	byContent := map[string]retainedRow{}
	duplicates := []duplicate{}
	sources := sourceUnion{}
	plan, e := readNearPlan(request)
	if e != nil || request.TestBasisPoints != 0 && plan == nil {
		return failure("DATASET_FILE_INVALID")
	}
	var rowSources []string
	var total int64
	e = readLines(request.InputFile, request.Limits.MaximumLineBytes, request.Limits.MaximumRows, func(line int64, raw []byte) error {
		record, e := wire.DecodeRecord(raw, request.Limits)
		if e != nil {
			return e
		}
		digest, e := record.ContentDigest(request.Limits)
		if e != nil {
			return e
		}
		sources.find(record.SourceID)
		total++
		if plan != nil {
			if line > int64(len(plan)) {
				return failure("DATASET_FILE_INVALID")
			}
			entry := plan[line-1]
			rowSources = append(rowSources, record.SourceID)
			sources.union(record.SourceID, rowSources[entry.Group-1])
			if entry.Match != 0 {
				duplicates = append(duplicates, duplicate{line, record.SourceID, int64(entry.Match), digest, NearPromptV1})
				return nil
			}
		}
		if prior, ok := byContent[digest]; ok {
			sources.union(prior.record.SourceID, record.SourceID)
			duplicates = append(duplicates, duplicate{line, record.SourceID, prior.line, digest, ""})
		} else {
			row := retainedRow{record, line, digest}
			byContent[digest] = row
			retained = append(retained, row)
		}
		return nil
	})
	if e != nil || plan != nil && int64(len(plan)) != total {
		return failure("DATASET_FILE_INVALID")
	}
	groups := map[string][]string{}
	sourceIDs := []string{}
	for s := range sources {
		sourceIDs = append(sourceIDs, s)
	}
	sort.Strings(sourceIDs)
	for _, s := range sourceIDs {
		root := sources.find(s)
		groups[root] = append(groups[root], s)
	}
	requiredGroups := 2
	domain := "projectone.text-sft.v1.split.v1\x00"
	if request.TestBasisPoints != 0 {
		requiredGroups = 3
		domain = "projectone.text-sft.v1.split.v2\x00"
	}
	if len(groups) < requiredGroups {
		return failure("DATASET_FILE_INVALID")
	}
	prefix := []byte(domain + *request.Seed + "\x00")
	type ranked struct {
		root, digest string
		encoded      []byte
	}
	ordered := []ranked{}
	for root, values := range groups {
		raw, e := wire.CanonicalStrings(values)
		if e != nil {
			return failure("DATASET_FILE_INVALID")
		}
		digest := sha256.Sum256(append(prefix, raw...))
		ordered = append(ordered, ranked{root, hex.EncodeToString(digest[:]), raw})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].digest == ordered[j].digest {
			return bytes.Compare(ordered[i].encoded, ordered[j].encoded) < 0
		}
		return ordered[i].digest < ordered[j].digest
	})
	validationGroups := min(len(ordered)-requiredGroups+1, max(1, (len(ordered)**request.ValidationBasisPoints+5000)/10000))
	testGroups := 0
	partitions := map[string][]wire.Record{"samples": nil, "train": nil, "validation": nil}
	if request.TestBasisPoints != 0 {
		testGroups = min(len(ordered)-validationGroups-1, max(1, (len(ordered)*request.TestBasisPoints+5000)/10000))
		partitions["test"] = nil
	}
	partitionByRoot := make(map[string]string, len(ordered))
	for i, group := range ordered {
		switch {
		case i < validationGroups:
			partitionByRoot[group.root] = "validation"
		case i < validationGroups+testGroups:
			partitionByRoot[group.root] = "test"
		default:
			partitionByRoot[group.root] = "train"
		}
	}
	assignments := []assignment{}
	for _, row := range retained {
		partition := partitionByRoot[sources.find(row.record.SourceID)]
		partitions[partition] = append(partitions[partition], row.record)
		partitions["samples"] = append(partitions["samples"], row.record)
		assignments = append(assignments, assignment{row.line, row.record.SourceID, row.digest, partition})
	}
	if total != manifest.TotalRows || int64(len(partitions["samples"])) != manifest.UniqueRows ||
		int64(len(duplicates)) != manifest.DuplicateRows || int64(len(groups)) != manifest.SourceGroups ||
		int64(validationGroups) != manifest.ValidationGroups || int64(testGroups) != manifest.TestGroups ||
		int64(len(partitions["train"])) != manifest.TrainRows || int64(len(partitions["validation"])) != manifest.ValidationRows ||
		int64(len(partitions["test"])) != manifest.TestRows {
		return failure("DATASET_FILE_INVALID")
	}
	for name, expected := range partitions {
		var seen int64
		e = readLines(filepath.Join(directory, name+".jsonl"), request.Limits.MaximumLineBytes, request.Limits.MaximumRows, func(line int64, raw []byte) error {
			record, e := wire.DecodeRecord(raw, request.Limits)
			if e != nil || line > int64(len(expected)) || record != expected[line-1] {
				return failure("DATASET_FILE_INVALID")
			}
			seen = line
			return nil
		})
		if e != nil || seen != int64(len(expected)) {
			return failure("DATASET_FILE_INVALID")
		}
	}
	if e = verifyMetadataLines(filepath.Join(directory, "assignments.jsonl"), request, assignments); e != nil {
		return e
	}
	return verifyMetadataLines(filepath.Join(directory, "duplicates.jsonl"), request, duplicates)
}

func verifyMetadataLines[T assignment | duplicate](path string, request wire.ProcessorRequest, expected []T) error {
	index := 0
	e := readLines(path, request.Limits.MaximumLineBytes, request.Limits.MaximumRows, func(_ int64, raw []byte) error {
		if index >= len(expected) || !wire.ValidJSONUnicode(raw) {
			return failure("DATASET_FILE_INVALID")
		}
		var actual T
		if wire.DecodeStrict(raw, &actual) != nil || !reflect.DeepEqual(actual, expected[index]) {
			return failure("DATASET_FILE_INVALID")
		}
		index++
		return nil
	})
	if e != nil || index != len(expected) {
		return failure("DATASET_FILE_INVALID")
	}
	return nil
}
