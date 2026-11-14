package workflows

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"time"
)

type RunRequest struct {
	Question string `json:"question"`
	Reason   string `json:"reason"`
}
type DebugRequest struct {
	Question         string `json:"question"`
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
}
type TraceResult struct {
	ToolVersionID    string `json:"plugin_version_id,omitempty"`
	ModelCount       *int   `json:"model_count,omitempty"`
	QuestionBytes    int    `json:"question_bytes,omitempty"`
	SourceCount      int    `json:"source_count,omitempty"`
	GenerationID     string `json:"generation_id,omitempty"`
	AliasID          string `json:"alias_id,omitempty"`
	AnswerBytes      int    `json:"answer_bytes,omitempty"`
	PromptTokens     int64  `json:"prompt_tokens,omitempty"`
	CompletionTokens int64  `json:"completion_tokens,omitempty"`
	TotalTokens      int64  `json:"total_tokens,omitempty"`
}
type NodeTrace struct {
	NodeID     string      `json:"node_id"`
	Kind       string      `json:"kind"`
	State      string      `json:"state"`
	Result     TraceResult `json:"result"`
	ErrorCode  *string     `json:"error_code"`
	StartedAt  *time.Time  `json:"started_at"`
	FinishedAt *time.Time  `json:"finished_at"`
}
type Run struct {
	ResultKind              string                           `json:"result_kind"`
	ToolResult              *plugins.ModelList               `json:"tool_result"`
	ID                      string                           `json:"id"`
	WorkflowID              string                           `json:"workflow_id"`
	ConversationID          *string                          `json:"conversation_id"`
	VersionID               *string                          `json:"version_id"`
	DraftRevision           *int64                           `json:"draft_revision"`
	GraphSHA256             string                           `json:"graph_sha256"`
	State                   string                           `json:"state"`
	Question                string                           `json:"question"`
	Answer                  *string                          `json:"answer"`
	Usage                   *knowledge.Usage                 `json:"usage"`
	Sources                 []knowledge.AnswerSourceEvidence `json:"sources"`
	ResultAvailable         bool                             `json:"result_available"`
	ResultUnavailableCode   *string                          `json:"result_unavailable_code"`
	ErrorCode               *string                          `json:"error_code"`
	CancellationRequestedAt *time.Time                       `json:"cancellation_requested_at"`
	CreatedAt               time.Time                        `json:"created_at"`
	StartedAt               *time.Time                       `json:"started_at"`
	FinishedAt              *time.Time                       `json:"finished_at"`
	DeadlineAt              time.Time                        `json:"deadline_at"`
	LeaseExpiresAt          *time.Time                       `json:"lease_expires_at"`
	Nodes                   []NodeTrace                      `json:"nodes"`
}
type runRecord struct {
	Run
	owner, audience, authority, membership string
	graph, evidence, alias, usage          []byte
	toolResult, toolProof                  []byte
	answer                                 *string
	processOwner, leaseID                  *string
}
type modelReference struct {
	ID                 string `json:"id"`
	WorkspaceID        string `json:"workspace_id"`
	GatewayID          string `json:"gateway_id"`
	NodeID             string `json:"node_id"`
	Name               string `json:"name"`
	DeploymentID       string `json:"deployment_id"`
	DeploymentRevision int64  `json:"deployment_revision"`
	Revision           int64  `json:"revision"`
	ClientOrigin       string `json:"client_origin"`
}

func modelSnapshot(a distribution.Alias) modelReference {
	return modelReference{ID: a.ID, WorkspaceID: a.WorkspaceID, GatewayID: a.GatewayID, NodeID: a.NodeID, Name: a.Name, DeploymentID: a.DeploymentID, DeploymentRevision: a.DeploymentRevision, Revision: a.Revision, ClientOrigin: a.ClientOrigin}
}
func (a modelReference) binding() distribution.Alias {
	return distribution.Alias{ID: a.ID, WorkspaceID: a.WorkspaceID, GatewayID: a.GatewayID, NodeID: a.NodeID, Name: a.Name, DeploymentID: a.DeploymentID, DeploymentRevision: a.DeploymentRevision, Revision: a.Revision, ClientOrigin: a.ClientOrigin}
}
