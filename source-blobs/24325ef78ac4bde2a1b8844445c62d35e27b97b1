package main

import (
	"strings"
	"testing"
)

func TestAssistantMetadataCommitment(t *testing.T) {
	// Independently reproduced from real PostgreSQL command events.
	const expected = "07194d3ad82be4e46581d9e1ac0f405574baceaba4e282d02b1e4bbac65628d4"
	if assistantMetadataHash != expected {
		t.Fatalf("assistant metadata digest = %s, want %s", assistantMetadataHash, expected)
	}
}

func TestVerifyAssistantDisclosure(t *testing.T) {
	const subject = "00000000-0000-4000-8000-000000000003"
	for _, tc := range []struct {
		name     string
		required bool
		edit     func(record)
		want     string
	}{
		{name: "complete", required: true},
		{name: "legacy redaction", edit: func(row record) {
			delete(row, "actor_kind")
			delete(row, "actor_id")
		}},
		{name: "missing disclosure", required: true, edit: func(row record) {
			delete(row, "actor_kind")
			delete(row, "actor_id")
		}, want: "actor_kind"},
		{name: "missing half", edit: func(row record) { delete(row, "actor_id") }, want: "record"},
		{name: "null kind", edit: func(row record) { row["actor_kind"] = nil }, want: "actor_kind"},
		{name: "unexpected extra field", edit: func(row record) { row["extra"] = nil }, want: "record"},
		{name: "unrelated metadata", edit: func(row record) {
			other := strings.Repeat("a", 64)
			row["metadata_sha256"] = &other
		}, want: "metadata_sha256"},
		{name: "human actor", edit: func(row record) { row["actor_user_id"] = new(subject) }, want: "actor_user_id"},
		{name: "missing subject", edit: func(row record) { row["subject_user_id"] = nil }, want: "subject_user_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows := testChain(t)[:2]
			row := rows[1]
			row["format"] = new(scopeFormat)
			row["site_id"] = new("fixture-site")
			row["cell_id"] = new("fixture-cell")
			row["actor_kind"] = new(assistantActorKind)
			row["actor_id"] = new(assistantActorID)
			row["subject_user_id"] = new(subject)
			row["metadata_sha256"] = &assistantMetadataHash
			if tc.edit != nil {
				tc.edit(row)
			}
			predecessor, _ := hashBytes(row.value("prev_hash"))
			tip := calculateHash(row, predecessor)
			row["hash"] = &tip
			result := verify(strings.NewReader(encodeRows(t, rows)), zeroHash, tip, tc.required)
			if result.Valid != (tc.want == "") || result.Field != tc.want || tc.want != "" && result.Line != 2 {
				t.Fatalf("unexpected verification: %+v", result)
			}
		})
	}
}

func TestVerifyRejectsAssistantDisclosureOnV1(t *testing.T) {
	rows := testChain(t)[:1]
	rows[0]["actor_kind"] = new(assistantActorKind)
	rows[0]["actor_id"] = new(assistantActorID)
	result := verify(strings.NewReader(encodeRows(t, rows)), zeroHash, rows[0].value("hash"), false)
	if result.Valid || result.Line != 1 || result.Field != "record" {
		t.Fatalf("accepted a V1 assistant disclosure: %+v", result)
	}
}
