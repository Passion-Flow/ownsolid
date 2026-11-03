package workflows

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

type Limits struct {
	GraphBytes           int `json:"graph_bytes"`
	NameCharacters       int `json:"name_characters"`
	InstructionsBytes    int `json:"instructions_bytes"`
	QuestionBytes        int `json:"question_bytes"`
	AnswerBytes          int `json:"answer_bytes"`
	MaximumTopK          int `json:"maximum_top_k"`
	MaximumRuns          int `json:"maximum_runs"`
	MaximumTurns         int `json:"maximum_turns"`
	WorkspaceConcurrency int `json:"workspace_concurrency"`
	RunSeconds           int `json:"run_seconds"`
	RetentionDays        int `json:"retention_days"`
	IdempotencySeconds   int `json:"idempotency_seconds"`
}
type Configuration struct {
	Enabled    bool     `json:"enabled"`
	CanEdit    bool     `json:"can_edit"`
	CanExecute bool     `json:"can_execute"`
	NodeKinds  []string `json:"node_kinds"`
	Limits     Limits   `json:"limits"`
}

// Published only while lifecycleMu is held. The immutable context preserves
// immediate parent cancellation without a database -> lifecycleMu lock edge.
type configurationReadiness struct {
	context   context.Context
	available bool
}

func (s *Service) publishConfigurationLocked() {
	s.configurationState.Store(&configurationReadiness{context: s.lifecycleContext, available: s.started && !s.closing && s.maintenanceError == ""})
}
func (s *Service) configurationAvailable() bool {
	state := s.configurationState.Load()
	return state != nil && state.available && state.context != nil && state.context.Err() == nil
}
func (s *Service) configurationFor(a identity.Auth, w workspaces.Workspace) Configuration {
	p := s.policy()
	maximum := s.questionBytes()
	out := Configuration{Enabled: s.Identity.Config.Workflows.Enabled, NodeKinds: []string{StartNode, RetrievalNode, AnswerNode, EndNode}, Limits: Limits{GraphBytes: p.GraphBytes, NameCharacters: p.NameCharacters, InstructionsBytes: p.InstructionsBytes, QuestionBytes: maximum, AnswerBytes: p.AnswerBytes, MaximumTopK: s.Identity.Config.Policy().Knowledge.MaximumTopK, MaximumRuns: p.MaximumRuns, MaximumTurns: p.MaximumTurns, WorkspaceConcurrency: p.WorkspaceConcurrency, RunSeconds: p.RunSeconds, RetentionDays: p.RetentionDays, IdempotencySeconds: s.Identity.Config.Policy().Identity.IdempotencySeconds}}
	if s.toolOnly() {
		out.NodeKinds = []string{StartNode, ToolNode, EndNode}
	} else if s.Identity.Config.Plugins.Enabled {
		out.NodeKinds = append(out.NodeKinds, ToolNode)
	}
	out.CanEdit = editable(a, w)
	out.CanExecute = out.Enabled && s.configurationAvailable() && w.Role != nil
	return out
}
func (s *Service) Configuration(ctx context.Context, a identity.Auth, workspace string) (Configuration, error) {
	var out Configuration
	e := s.transaction(ctx, a, workspace, false, func(_ context.Context, _ transaction.Executor, w workspaces.Workspace) error {
		out = s.configurationFor(a, w)
		return nil
	})
	if e != nil {
		return Configuration{}, e
	}
	return out, nil
}

// ConfigurationWithin borrows the caller's actual transaction and repeats
// current actor/scope checks. It neither waits on lifecycleMu nor grants admission.
func (s *Service) ConfigurationWithin(ctx context.Context, q transaction.Executor, a identity.Auth, workspace string) (Configuration, error) {
	store, e := s.repository()
	if e != nil {
		return Configuration{}, workflowRepositoryError(e)
	}
	if ctx == nil || q == nil || q.Dialect() != store.Dialect() {
		return Configuration{}, failure("WORKFLOW_STORAGE_MISMATCH")
	}
	if _, pool := q.(transaction.Store); pool {
		return Configuration{}, failure("WORKFLOW_TRANSACTION_REQUIRED")
	}
	if a.Me.Audience != "portal" && !admin(a) {
		return Configuration{}, failure("ACCESS_DENIED")
	}
	w, e := s.scopeWithin(ctx, q, a, workspace, false)
	if e != nil {
		return Configuration{}, workflowRepositoryError(e)
	}
	return s.configurationFor(a, w), nil
}
