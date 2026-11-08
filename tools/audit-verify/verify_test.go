package main

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func testChain(t *testing.T) []record {
	t.Helper()
	rows := make([]record, 3)
	last := zeroHash
	for i, occurred := range []string{"2026-10-31T23:59:59.999999Z", "2026-11-01T00:00:00.000000Z", "2026-11-01T00:00:00.000001Z"} {
		row := make(record)
		for _, field := range fields {
			row[field] = nil
		}
		for field, value := range map[string]string{
			"format": format, "position": strconv.Itoa(i + 1), "occurred_at": occurred,
			"id": "00000000-0000-4000-8000-000000000001", "request_id": "00000000-0000-4000-8000-000000000002",
			"audience": "system", "action": "fixture.audit", "outcome": "success", "metadata_sha256": strings.Repeat("a", 64),
			"prev_hash": last,
		} {
			row[field] = &value
		}
		predecessor, _ := hashBytes(last)
		last = calculateHash(row, predecessor)
		hash := last
		row["hash"] = &hash
		rows[i] = row
	}
	return rows
}

func encodeRows(t *testing.T, rows []record) string {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
	return output.String()
}

func TestVerifyIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		edit      func([]record) []record
		wantLine  int64
		wantField string
	}{
		{name: "intact"},
		{name: "middle changed", edit: func(rows []record) []record {
			action := "fixture.changed"
			rows[1]["action"] = &action
			return rows
		}, wantLine: 2, wantField: "hash"},
		{name: "middle deleted", edit: func(rows []record) []record { return []record{rows[0], rows[2]} }, wantLine: 2, wantField: "position"},
		{name: "last deleted", edit: func(rows []record) []record { return rows[:2] }, wantLine: 2, wantField: "hash"},
		{name: "timestamp offset", edit: func(rows []record) []record {
			offset := "2026-11-01T08:00:00.000000+08:00"
			rows[1]["occurred_at"] = &offset
			return rows
		}, wantLine: 2, wantField: "occurred_at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := testChain(t)
			tip := rows[2].value("hash")
			if tc.edit != nil {
				rows = tc.edit(rows)
			}
			result := verify(strings.NewReader(encodeRows(t, rows)), zeroHash, tip)
			if result.Valid != (tc.wantLine == 0) || result.Line != tc.wantLine || result.Field != tc.wantField {
				t.Fatalf("unexpected result: %+v", result)
			}
			if result.Valid && (result.Records != 3 || result.From != rows[0].value("occurred_at") || result.Through != rows[2].value("occurred_at")) {
				t.Fatalf("incorrect coverage: %+v", result)
			}
		})
	}
}

func TestVerifyMalformedTransport(t *testing.T) {
	rows := testChain(t)
	encoded := strings.TrimSpace(encodeRows(t, rows[:1]))
	for _, input := range []string{
		"", encoded + " {}", strings.Replace(encoded, `"action":`, `"Action":`, 1),
		`{"action":"replacement",` + encoded[1:], strings.Repeat("x", maxRecordBytes+1),
	} {
		result := verify(strings.NewReader(input), zeroHash, "")
		if result.Valid || result.Line != 1 || result.Field != "record" {
			t.Errorf("accepted malformed transport: %+v", result)
		}
	}
}

func TestRunOutputsJSONFailure(t *testing.T) {
	rows := testChain(t)
	var output, stderr bytes.Buffer
	args := []string{"--file", "-", "--output", "json"}
	exit := run(args, strings.NewReader(encodeRows(t, rows[1:])), &output, &stderr)
	var result report
	if err := json.Unmarshal(output.Bytes(), &result); err != nil || exit != 1 || result.Valid || result.Line != 1 || result.Field != "position" {
		t.Fatalf("exit=%d, JSON=%s, error=%v", exit, output.String(), err)
	}
}

func TestVerifyRejectsReturnToV1AfterV2(t *testing.T) {
	rows := testChain(t)
	newFormat, site, cell := scopeFormat, "fixture-site", "fixture-cell"
	rows[1]["format"], rows[1]["site_id"], rows[1]["cell_id"] = &newFormat, &site, &cell
	previous, _ := hashBytes(rows[1].value("prev_hash"))
	scopedHash := calculateHash(rows[1], previous)
	rows[1]["hash"], rows[2]["prev_hash"] = &scopedHash, &scopedHash
	previous, _ = hashBytes(scopedHash)
	downgradedHash := calculateHash(rows[2], previous)
	rows[2]["hash"] = &downgradedHash
	result := verify(strings.NewReader(encodeRows(t, rows)), zeroHash, downgradedHash)
	if result.Valid || result.Line != 3 || result.Field != "format" {
		t.Fatalf("accepted a format downgrade: %+v", result)
	}
}
