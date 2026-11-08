package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	format      = "ownsolid.audit.v1"
	scopeFormat = "ownsolid.audit.v2"
	timestamp   = "2006-01-02T15:04:05.000000Z"
	// This bounds one record, independent of the size of the export.
	maxRecordBytes = 16 * 1024
)

var (
	zeroHash = strings.Repeat("0", sha256.Size*2)
	fields   = []string{
		"position", "id", "occurred_at", "actor_user_id", "subject_user_id", "tenant_id", "workspace_id",
		"audience", "action", "outcome", "target_type", "target_id", "request_id",
		"reason_sha256", "identifier_digest_sha256", "metadata_sha256",
	}
	scopeFields = append(slices.Clone(fields), "site_id", "cell_id")
)

type report struct {
	Valid    bool   `json:"valid"`
	Records  int64  `json:"records"`
	From     string `json:"from,omitempty"`
	Through  string `json:"through,omitempty"`
	LastHash string `json:"last_hash,omitempty"`
	Line     int64  `json:"line,omitempty"`
	Field    string `json:"field,omitempty"`
	Error    string `json:"error,omitempty"`
}

type record map[string]*string

func (r record) value(field string) string {
	if v := r[field]; v != nil {
		return *v
	}
	return ""
}

// decodeRecord checks exact field names and uniqueness. The default JSON
// struct decoder accepts case variants and silently takes the last duplicate.
func decodeRecord(line []byte) (record, string) {
	if !utf8.Valid(line) {
		return nil, "record is not valid UTF-8"
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, "expected one JSON object"
	}
	row := make(record)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, "malformed JSON object"
		}
		field, ok := key.(string)
		if !ok {
			return nil, "invalid field name"
		}
		if _, duplicate := row[field]; duplicate {
			return nil, "duplicate field"
		}
		var value *string
		if err := decoder.Decode(&value); err != nil {
			return nil, "expected a string or null field value"
		}
		row[field] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, "unterminated JSON object"
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, "unexpected content after the JSON object"
	}
	ordered := recordFields(row.value("format"))
	if ordered == nil {
		return nil, "unsupported format"
	}
	expectedFields := len(ordered) + 3
	_, hasActorKind := row["actor_kind"]
	_, hasActorID := row["actor_id"]
	if hasActorKind != hasActorID || hasActorKind && row.value("format") != scopeFormat {
		return nil, "assistant actor disclosure requires both fields on a V2 record"
	}
	if hasActorKind {
		expectedFields += 2
	}
	if len(row) != expectedFields {
		return nil, "unexpected number of fields"
	}
	for _, field := range ordered {
		if _, exists := row[field]; !exists {
			return nil, "missing required field"
		}
	}
	for _, field := range []string{"id", "audience", "action", "outcome", "request_id"} {
		if row[field] == nil {
			return nil, "required field is null"
		}
	}
	if row.value("format") == scopeFormat && (row["site_id"] == nil || row["cell_id"] == nil) {
		return nil, "site and cell identifiers are required"
	}
	return row, ""
}

func recordFields(version string) []string {
	switch version {
	case format:
		return fields
	case scopeFormat:
		return scopeFields
	default:
		return nil
	}
}

func hashBytes(value string) ([]byte, bool) {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return nil, false
	}
	hash, err := hex.DecodeString(value)
	return hash, err == nil
}

func calculateHash(row record, predecessor []byte) string {
	digest := sha256.New()
	digest.Write([]byte(row.value("format") + "\x00"))
	digest.Write(predecessor)
	var length [4]byte
	for _, field := range recordFields(row.value("format")) {
		value := row[field]
		if value == nil {
			binary.BigEndian.PutUint32(length[:], ^uint32(0))
			digest.Write(length[:])
			continue
		}
		binary.BigEndian.PutUint32(length[:], uint32(len(*value)))
		digest.Write(length[:])
		io.WriteString(digest, *value)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func verify(input io.Reader, previousHash, expectedLastHash string, requireAssistantActors bool) report {
	var result report
	fail := func(line int64, field, problem string) report {
		result.Line, result.Field, result.Error = line, field, problem
		return result
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), maxRecordBytes)
	var position int64
	var scoped bool
	lastHash := previousHash
	for scanner.Scan() {
		line := result.Records + 1
		row, problem := decodeRecord(scanner.Bytes())
		if problem != "" {
			return fail(line, "record", problem)
		}

		if scoped && row.value("format") != scopeFormat {
			return fail(line, "format", "record format moved backwards")
		}
		scoped = row.value("format") == scopeFormat

		rawPosition := row.value("position")
		current, err := strconv.ParseInt(rawPosition, 10, 64)
		if err != nil || current < 1 || strconv.FormatInt(current, 10) != rawPosition {
			return fail(line, "position", "expected a positive canonical decimal position")
		}
		if line == 1 && previousHash != zeroHash {
			if current == 1 {
				return fail(line, "position", "a retained export must start after genesis")
			}
		} else if current-1 != position {
			return fail(line, "position", "position is not consecutive")
		}

		occurred := row.value("occurred_at")
		at, err := time.Parse(timestamp, occurred)
		if err != nil || at.Year() < 1 || at.Format(timestamp) != occurred {
			return fail(line, "occurred_at", "expected a UTC timestamp with six fractional digits")
		}
		predecessor, ok := hashBytes(row.value("prev_hash"))
		if !ok {
			return fail(line, "prev_hash", "expected a lowercase SHA-256 digest")
		}
		if row.value("prev_hash") != lastHash {
			return fail(line, "prev_hash", "predecessor hash does not match")
		}
		for _, field := range []string{"hash", "metadata_sha256", "reason_sha256", "identifier_digest_sha256"} {
			if (field == "reason_sha256" || field == "identifier_digest_sha256") && row[field] == nil {
				continue
			}
			if _, ok := hashBytes(row.value(field)); !ok {
				return fail(line, field, "expected a lowercase SHA-256 digest")
			}
		}
		lastHash = row.value("hash")
		if calculateHash(row, predecessor) != lastHash {
			return fail(line, "hash", "record content hash does not match")
		}
		if field, problem := assistantDisclosureProblem(row, requireAssistantActors); problem != "" {
			return fail(line, field, problem)
		}

		if result.From == "" || occurred < result.From {
			result.From = occurred
		}
		if occurred > result.Through {
			result.Through = occurred
		}
		position = current
		result.Records++
	}
	if scanner.Err() != nil {
		return fail(result.Records+1, "record", "cannot read record or record exceeds the size limit")
	}
	if result.Records == 0 {
		return fail(1, "record", "export contains no records")
	}
	if expectedLastHash != "" && lastHash != expectedLastHash {
		return fail(result.Records, "hash", "last hash does not match the trusted checkpoint")
	}
	result.Valid, result.LastHash = true, lastHash
	return result
}
