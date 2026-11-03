package datasets

import (
	wire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	"time"
)

type Dataset struct {
	ID             string     `json:"id"`
	WorkspaceID    string     `json:"workspace_id"`
	Name           string     `json:"name"`
	Origin         string     `json:"origin"`
	License        string     `json:"license"`
	RightsAsserted bool       `json:"rights_asserted"`
	CreatedBy      string     `json:"created_by"`
	Revision       int64      `json:"revision"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	RetiredAt      *time.Time `json:"retired_at"`
	RetireReason   *string    `json:"retire_reason"`
}
type CreateRequest struct {
	WorkspaceID    string `json:"workspace_id"`
	Name           string `json:"name"`
	Origin         string `json:"origin"`
	License        string `json:"license"`
	RightsAsserted bool   `json:"rights_asserted"`
	Reason         string `json:"reason"`
}
type DraftRequest struct {
	SourceName    string `json:"source_name"`
	ExpectedBytes int64  `json:"expected_bytes"`
	SourceSHA256  string `json:"source_sha256"`
	Reason        string `json:"reason"`
}
type Draft struct {
	ErrorLineNumber       *int64           `json:"error_line_number"`
	ID                    string           `json:"id"`
	DatasetID             string           `json:"dataset_id"`
	WorkspaceID           string           `json:"workspace_id"`
	CreatedBy             string           `json:"created_by"`
	ParentVersionID       *string          `json:"parent_version_id"`
	SourceName            string           `json:"source_name"`
	SourceSHA256          string           `json:"source_sha256"`
	ExpectedBytes         int64            `json:"expected_bytes"`
	SourceReceivedAt      *time.Time       `json:"source_received_at"`
	State                 string           `json:"state"`
	Revision              int64            `json:"revision"`
	TotalRows             *int64           `json:"total_rows"`
	InvalidRows           *int64           `json:"invalid_rows"`
	ValidationCompletedAt *time.Time       `json:"validation_completed_at"`
	FatalSourceError      bool             `json:"fatal_source_error"`
	ValidationErrors      []wire.LineError `json:"validation_errors"`
	ErrorsTruncated       bool             `json:"errors_truncated"`
	ErrorCode             *string          `json:"error_code"`
	CurrentJobID          *string          `json:"current_job_id"`
	PublishedVersionID    *string          `json:"published_version_id"`
	CreatedAt             time.Time        `json:"created_at"`
	UpdatedAt             time.Time        `json:"updated_at"`
}
type Row struct {
	ID              string  `json:"id"`
	LineNumber      int64   `json:"line_number"`
	Revision        int64   `json:"revision"`
	SourceID        *string `json:"source_id"`
	Prompt          *string `json:"prompt"`
	Completion      *string `json:"completion"`
	ErrorCode       *string `json:"error_code"`
	RawPreview      string  `json:"raw_preview"`
	PreviewReplaced bool    `json:"preview_replaced"`
}
type RowRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	RowRevision      int64  `json:"row_revision"`
	SourceID         string `json:"source_id"`
	Prompt           string `json:"prompt"`
	Completion       string `json:"completion"`
	Reason           string `json:"reason"`
}

func (r RowRequest) Record() wire.Record {
	return wire.Record{SourceID: r.SourceID, Prompt: r.Prompt, Completion: r.Completion}
}

type PublicationRequest struct {
	ExpectedRevision      int64  `json:"expected_revision"`
	Seed                  string `json:"seed"`
	ValidationBasisPoints int    `json:"validation_basis_points"`
	Reason                string `json:"reason"`
}
type Job struct {
	ErrorLineNumber       *int64     `json:"error_line_number"`
	Reason                string     `json:"reason"`
	ID                    string     `json:"id"`
	DatasetID             string     `json:"dataset_id"`
	DraftID               string     `json:"draft_id"`
	WorkspaceID           string     `json:"workspace_id"`
	Kind                  string     `json:"kind"`
	RequestedBy           string     `json:"requested_by"`
	ExpectedRevision      int64      `json:"expected_revision"`
	State                 string     `json:"state"`
	VersionID             *string    `json:"version_id"`
	InputSHA256           *string    `json:"input_sha256"`
	Seed                  *string    `json:"seed"`
	ValidationBasisPoints *int       `json:"validation_basis_points"`
	AttemptCount          int        `json:"attempt_count"`
	AttemptID             *string    `json:"attempt_id"`
	LeaseExpiresAt        *time.Time `json:"lease_expires_at"`
	CancelRequestedAt     *time.Time `json:"cancel_requested_at"`
	ErrorCode             *string    `json:"error_code"`
	Retryable             bool       `json:"retryable"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	CompletedAt           *time.Time `json:"completed_at"`
}
type Version struct {
	ID                    string        `json:"id"`
	DatasetID             string        `json:"dataset_id"`
	WorkspaceID           string        `json:"workspace_id"`
	DraftID               string        `json:"draft_id"`
	JobID                 string        `json:"job_id"`
	AttemptID             string        `json:"attempt_id"`
	Ordinal               int64         `json:"ordinal"`
	ParentVersionID       *string       `json:"parent_version_id"`
	PublishedBy           string        `json:"published_by"`
	PublishedAt           time.Time     `json:"published_at"`
	RawSourceSHA256       string        `json:"raw_source_sha256"`
	EffectiveSourceSHA256 string        `json:"effective_source_sha256"`
	ManifestSHA256        string        `json:"manifest_sha256"`
	Manifest              wire.Manifest `json:"manifest"`
	RowMappingSHA256      string        `json:"row_mapping_sha256"`
	RetiredAt             *time.Time    `json:"retired_at"`
	RetireReason          *string       `json:"retire_reason"`
}
