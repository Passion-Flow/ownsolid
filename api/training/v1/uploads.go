package v1

import (
	"encoding/json"
	"errors"
	"time"

	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

// Envelope overhead is a fixed wire ceiling, separate from configurable deployment time/resource policies.
const MaximumUploadSubmissionBytes = MaximumCheckpointMetadataBytes + 4096

type UploadSubmission struct {
	LeaseToken string          `json:"lease_token"`
	Kind       string          `json:"kind"`
	Metadata   json.RawMessage `json:"metadata"`
}

// DecodeUploadSubmission preserves original metadata bytes; owning service decodes the status-specific schema against its frozen command.
func DecodeUploadSubmission(raw []byte) (UploadSubmission, error) {
	var out UploadSubmission
	bad := errors.New("TRAINING_UPLOAD_INVALID")
	if len(raw) > MaximumUploadSubmissionBytes {
		return out, bad
	}
	fields, e := ExactObject(raw, []string{"lease_token", "kind", "metadata"})
	if e != nil || json.Unmarshal(fields["lease_token"], &out.LeaseToken) != nil || !machineToken.MatchString(out.LeaseToken) || json.Unmarshal(fields["kind"], &out.Kind) != nil || out.Kind != "checkpoint" && out.Kind != "artifact" {
		return out, bad
	}
	out.Metadata = fields["metadata"]
	if len(out.Metadata) < 2 || len(out.Metadata) > MaximumCheckpointMetadataBytes || out.Metadata[0] != '{' {
		return UploadSubmission{}, bad
	}
	return out, nil
}

type Upload struct {
	ID             string           `json:"id"`
	JobID          string           `json:"job_id"`
	AttemptID      string           `json:"attempt_id"`
	Kind           string           `json:"kind"`
	GlobalStep     int64            `json:"global_step"`
	ManifestSHA256 string           `json:"manifest_sha256"`
	State          string           `json:"state"`
	Files          []datawire.Asset `json:"files"`
	ChunkBytes     int64            `json:"chunk_bytes"`
	ReceivedBytes  int64            `json:"received_bytes"`
	CreatedAt      time.Time        `json:"created_at"`
}
type ChunkRequest struct {
	Asset  string `json:"asset"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}

func (r ChunkRequest) Validate() error {
	if !flatAsset.MatchString(r.Asset) || r.Asset == "." || r.Asset == ".." || r.Offset < 0 || r.Offset%TransferChunkBytes != 0 || r.Length < 1 || r.Length > TransferChunkBytes || !datawire.ValidDigest(r.SHA256) {
		return errors.New("TRAINING_UPLOAD_INVALID")
	}
	return nil
}

type ChunkAcceptance struct {
	UploadID      string `json:"upload_id"`
	Asset         string `json:"asset"`
	Offset        int64  `json:"offset"`
	SizeBytes     int64  `json:"size_bytes"`
	SHA256        string `json:"sha256"`
	Duplicate     bool   `json:"duplicate"`
	ReceivedBytes int64  `json:"received_bytes"`
}
type OutputAcceptance struct {
	UploadID    string `json:"upload_id"`
	ReferenceID string `json:"reference_id"`
	Kind        string `json:"kind"`
	GlobalStep  int64  `json:"global_step"`
	Duplicate   bool   `json:"duplicate"`
}
