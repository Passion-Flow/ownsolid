package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
)

const (
	assistantActorKind = "product_assistant"
	assistantActorID   = "ownsolid.product-assistant"
)

var assistantMetadataHash = hashAssistantMetadata()

func hashAssistantMetadata() string {
	// The writer commits to exactly these two strings in UTF-8 key order.
	encoded := binary.BigEndian.AppendUint32([]byte{'o'}, 2)
	for _, field := range [][2]string{{"actor_id", assistantActorID}, {"actor_kind", assistantActorKind}} {
		encoded = appendMetadataFrame(encoded, field[0])
		encoded = append(encoded, 's')
		encoded = appendMetadataFrame(encoded, field[1])
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func appendMetadataFrame(dst []byte, value string) []byte {
	return append(binary.BigEndian.AppendUint32(dst, uint32(len(value))), value...)
}

func assistantDisclosureProblem(row record, required bool) (field, problem string) {
	if _, disclosed := row["actor_kind"]; !disclosed {
		if required && row.value("format") == scopeFormat &&
			row["actor_user_id"] == nil && row.value("subject_user_id") != "" &&
			row.value("metadata_sha256") == assistantMetadataHash {
			return "actor_kind", "assistant actor disclosure is required"
		}
		return "", ""
	}
	if row.value("actor_kind") != assistantActorKind {
		return "actor_kind", "expected the product assistant actor kind"
	}
	if row.value("actor_id") != assistantActorID {
		return "actor_id", "expected the built-in product assistant identifier"
	}
	if row["actor_user_id"] != nil {
		return "actor_user_id", "an assistant event must have a null human actor"
	}
	if row.value("subject_user_id") == "" {
		return "subject_user_id", "an assistant event requires a user subject"
	}
	if row.value("metadata_sha256") != assistantMetadataHash {
		return "metadata_sha256", "assistant actor does not match the metadata commitment"
	}
	return "", ""
}
