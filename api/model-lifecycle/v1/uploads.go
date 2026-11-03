package v1

import (
	"encoding/json"
	"errors"
	"time"

	dataset "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	training "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const MaximumUploadSubmissionBytes = MaximumResultBytes + 4096
const MaximumUploadDescriptorBytes int64 = 32768

// MaximumChunkDirectoryEntries bounds retained canonical and failed partial entries; excess requires explicit recovery.
const MaximumChunkDirectoryEntries = 1024

type UploadSubmission struct {
	LeaseToken string          `json:"lease_token"`
	Metadata   json.RawMessage `json:"metadata"`
}

func DecodeUploadSubmission(raw []byte) (UploadSubmission, error) {
	var out UploadSubmission
	bad := errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
	if int64(len(raw)) > MaximumUploadSubmissionBytes {
		return out, bad
	}
	fields, e := training.ExactObject(raw, []string{"lease_token", "metadata"})
	if e != nil || json.Unmarshal(fields["lease_token"], &out.LeaseToken) != nil || !machineToken.MatchString(out.LeaseToken) {
		return out, bad
	}
	out.Metadata = fields["metadata"]
	if len(out.Metadata) < 2 || int64(len(out.Metadata)) > MaximumResultBytes || out.Metadata[0] != '{' {
		return UploadSubmission{}, bad
	}
	return out, nil
}

type Upload struct {
	ID             string          `json:"id"`
	PreparationID  string          `json:"preparation_id"`
	AttemptID      string          `json:"attempt_id"`
	ManifestSHA256 string          `json:"manifest_sha256"`
	State          string          `json:"state"`
	Files          []dataset.Asset `json:"files"`
	ChunkBytes     int64           `json:"chunk_bytes"`
	ReceivedBytes  int64           `json:"received_bytes"`
	CreatedAt      time.Time       `json:"created_at"`
}

type ChunkRequest struct {
	Asset  string `json:"asset"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
	SHA256 string `json:"sha256"`
}

func (r ChunkRequest) Validate() error {
	if !ModelAssetAllowed(r.Asset) || r.Offset < 0 || r.Offset%TransferChunkBytes != 0 || r.Length < 1 || r.Length > TransferChunkBytes || !dataset.ValidDigest(r.SHA256) {
		return errors.New("MODEL_PREPARATION_UPLOAD_INVALID")
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
	UploadID       string `json:"upload_id"`
	PreparationID  string `json:"preparation_id"`
	AttemptID      string `json:"attempt_id"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Duplicate      bool   `json:"duplicate"`
}
