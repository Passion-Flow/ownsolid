package v1

import (
	"bytes"
	"strings"
	"testing"
)

func TestPreparationUploadEnvelopePreservesOriginalUnicodeAndWhitespaceBytes(t *testing.T) {
	metadata := []byte("{\n \"synthetic_fixture\": \"\u4e8b\u4ef6<&>\"\n}")
	body := append([]byte(`{"lease_token":"`+strings.Repeat("a", 43)+`","metadata":`), metadata...)
	body = append(body, '}')
	submission, e := DecodeUploadSubmission(body)
	if e != nil || !bytes.Equal(submission.Metadata, metadata) {
		t.Fatal("original metadata bytes were reserialized", e)
	}
	for _, bad := range []string{strings.Replace(string(body), `"lease_token":`, `"Lease_token":`, 1), strings.Replace(string(body), `"metadata":`+string(metadata), `"metadata":null`, 1), strings.Replace(string(body), `"synthetic_fixture":`, `"synthetic_fixture":0,"synthetic_fixture":`, 1), string(body) + "{}"} {
		if _, e := DecodeUploadSubmission([]byte(bad)); e == nil {
			t.Fatal("untrusted upload envelope accepted")
		}
	}
}

func TestPreparationOutputChunkAcceptsOnlyDeclaredBaseModelFilenamesAndAlignedBounds(t *testing.T) {
	for _, asset := range ApprovedBaseFiles() {
		request := ChunkRequest{Asset: asset.Path, Offset: 0, Length: min(TransferChunkBytes, asset.SizeBytes), SHA256: strings.Repeat("a", 64)}
		if request.Validate() != nil {
			t.Fatal("approved model filename", asset.Path)
		}
	}
	for _, name := range []string{"../model.safetensors", "result.json", "publication.json", "optimizer.pt", "pytorch_model.bin", "unsafe.py"} {
		if (ChunkRequest{Asset: name, Offset: 0, Length: 1, SHA256: strings.Repeat("a", 64)}).Validate() == nil {
			t.Fatal("unapproved output filename accepted", name)
		}
	}
	for _, request := range []ChunkRequest{{Asset: "model.safetensors", Offset: 1, Length: 1, SHA256: strings.Repeat("a", 64)}, {Asset: "model.safetensors", Offset: 0, Length: TransferChunkBytes + 1, SHA256: strings.Repeat("a", 64)}, {Asset: "model.safetensors", Offset: 0, Length: 1, SHA256: "invalid"}} {
		if request.Validate() == nil {
			t.Fatal("invalid chunk bounds/digest accepted")
		}
	}
}
