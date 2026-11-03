package knowledge

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"sync"
	"time"
)

type Service struct {
	maintenanceMu    sync.Mutex
	maintenanceAfter string
	DB               *pgxpool.Pool
	Identity         *identity.Service
	Workspaces       *workspaces.Service
	Distribution     *distribution.Service
	Processor        *Processor
}
type Generation struct {
	ID                 string     `json:"id"`
	CatalogSHA256      string     `json:"catalog_sha256"`
	Dimension          int        `json:"dimension"`
	ChunkTokens        int        `json:"chunk_tokens"`
	OverlapTokens      int        `json:"overlap_tokens"`
	State              string     `json:"state"`
	ExpectedDocuments  int        `json:"expected_documents"`
	CompletedDocuments int        `json:"completed_documents"`
	FailedDocuments    int        `json:"failed_documents"`
	ErrorCode          *string    `json:"error_code"`
	CreatedAt          time.Time  `json:"created_at"`
	ActivatedAt        *time.Time `json:"activated_at"`
	FinishedAt         *time.Time `json:"finished_at"`
}
type Base struct {
	ID                 string      `json:"id"`
	WorkspaceID        string      `json:"workspace_id"`
	Name               string      `json:"name"`
	Enabled            bool        `json:"enabled"`
	Revision           int64       `json:"revision"`
	ActiveGeneration   Generation  `json:"active_generation"`
	BuildingGeneration *Generation `json:"building_generation"`
	DocumentCount      int         `json:"document_count"`
	ReadyDocuments     int         `json:"ready_documents"`
	CreatedBy          string      `json:"created_by"`
	CreatedAt          time.Time   `json:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
}
type SourceMetadata struct {
	ID            string    `json:"id"`
	SourceVersion int64     `json:"source_version"`
	SHA256        string    `json:"sha256"`
	SizeBytes     int       `json:"size_bytes"`
	CreatedAt     time.Time `json:"created_at"`
}
type Attempt struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	State         string     `json:"state"`
	AttemptNumber int        `json:"attempt_number"`
	ChunkCount    int        `json:"chunk_count"`
	ErrorCode     *string    `json:"error_code"`
	RequestedBy   string     `json:"requested_by"`
	CreatedAt     time.Time  `json:"created_at"`
	StartedAt     *time.Time `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at"`
}
type Document struct {
	ID             string          `json:"id"`
	BaseID         string          `json:"base_id"`
	WorkspaceID    string          `json:"workspace_id"`
	Name           string          `json:"name"`
	Revision       int64           `json:"revision"`
	State          string          `json:"state"`
	VisibleSource  *SourceMetadata `json:"visible_source"`
	PendingSource  *SourceMetadata `json:"pending_source"`
	CurrentAttempt *Attempt        `json:"current_attempt"`
	CreatedBy      string          `json:"created_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	DeletedAt      *time.Time      `json:"deleted_at"`
}
type Source struct {
	DocumentID    string    `json:"document_id"`
	SourceID      string    `json:"source_id"`
	SourceVersion int64     `json:"source_version"`
	Name          string    `json:"name"`
	Content       string    `json:"content"`
	SHA256        string    `json:"sha256"`
	SizeBytes     int       `json:"size_bytes"`
	CreatedAt     time.Time `json:"created_at"`
}
type CreateBase struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	Reason      string `json:"reason"`
}
type UpdateBase struct {
	Name             string `json:"name"`
	Enabled          bool   `json:"enabled"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type UploadDocument struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
	Reason  string `json:"reason"`
}
type ReplaceSource struct {
	Content          string `json:"content"`
	SHA256           string `json:"sha256"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type RevisionReason struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type SearchRequest struct {
	Query        string  `json:"query"`
	TopK         int     `json:"top_k"`
	MinimumScore float64 `json:"minimum_score"`
}
type AnswerRequest struct {
	Question     string  `json:"question"`
	AliasID      string  `json:"alias_id"`
	TopK         int     `json:"top_k"`
	MinimumScore float64 `json:"minimum_score"`
}
type Match struct {
	CitationID       string  `json:"citation_id"`
	DocumentID       string  `json:"document_id"`
	SourceID         string  `json:"source_id"`
	SourceVersion    int64   `json:"source_version"`
	Name             string  `json:"name"`
	SourceSHA256     string  `json:"source_sha256"`
	StartByte        int     `json:"start_byte"`
	EndByte          int     `json:"end_byte"`
	ChunkOrdinal     int     `json:"chunk_ordinal"`
	Text             string  `json:"text"`
	Score            float64 `json:"score"`
	ExcerptTruncated bool    `json:"excerpt_truncated"`
}
type SearchResult struct {
	BaseID               string  `json:"base_id"`
	GenerationID         string  `json:"generation_id"`
	EncoderCatalogSHA256 string  `json:"encoder_catalog_sha256"`
	Matches              []Match `json:"matches"`
}
type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}
type Answer struct {
	BaseID       string  `json:"base_id"`
	GenerationID string  `json:"generation_id"`
	AliasID      string  `json:"alias_id"`
	Answer       string  `json:"answer"`
	Sources      []Match `json:"sources"`
	Usage        Usage   `json:"usage"`
}
type Page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
type Configuration struct {
	ChunkBytes           int     `json:"chunk_bytes"`
	Enabled              bool    `json:"enabled"`
	EncoderAvailable     bool    `json:"encoder_available"`
	CanWrite             bool    `json:"can_write"`
	CanAsk               bool    `json:"can_ask"`
	EncoderCatalogSHA256 string  `json:"encoder_catalog_sha256"`
	Dimension            int     `json:"dimension"`
	ChunkTokens          int     `json:"chunk_tokens"`
	OverlapTokens        int     `json:"overlap_tokens"`
	SourceBytes          int     `json:"source_bytes"`
	QueryBytes           int     `json:"query_bytes"`
	QueryTokens          int     `json:"query_tokens"`
	TopK                 int     `json:"top_k"`
	MaximumTopK          int     `json:"maximum_top_k"`
	MinimumScore         float64 `json:"minimum_score"`
	MaximumDocuments     int     `json:"maximum_documents"`
}
