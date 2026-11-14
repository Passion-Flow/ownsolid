package workflows

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

func TestWorkflowToolOnlyProfileGraphAndQuestionBoundary(t *testing.T) {
	s := &Service{Identity: &identity.Service{}}
	s.Identity.Config.Workflows.Enabled = true
	s.Identity.Config.Plugins.Enabled = true
	p := s.Identity.Config.Policy()
	p.Workflows.QuestionBytes = 128
	p.Knowledge.QueryBytes = 32
	s.Identity.Config.Runtime = &p
	graph, e := s.initialGraph()
	if e != nil || !graph.IsTool() || graph.ValidateExecutable().Valid || graph.Tool.PluginVersionID != "" {
		t.Fatal("new tool draft must require an explicit installed version selection", e)
	}
	if len(graph.Graph.Nodes) != 3 || len(graph.Graph.Edges) != 2 || graph.ResultKind() != "model_list" {
		t.Fatal("tool-only template changed the bounded graph contract")
	}
	configuration := s.configurationFor(identity.Auth{}, workspaces.Workspace{})
	if !slices.Equal(configuration.NodeKinds, []string{StartNode, ToolNode, EndNode}) || configuration.Limits.QuestionBytes != 128 || configuration.CanExecute {
		t.Fatal("profile projected unsupported nodes, wrong limit or unstarted admission")
	}
	// A long tool question is valid but an unstarted service still cannot admit
	// it. A question beyond the configured workflow bound fails before storage.
	for _, test := range []struct {
		size int
		code string
	}{{80, "WORKFLOW_UNAVAILABLE"}, {129, "VALIDATION_FAILED"}} {
		_, e := s.Debug(context.Background(), identity.Auth{}, "", "", DebugRequest{ExpectedRevision: 1, Question: strings.Repeat("q", test.size), Reason: "Profile boundary"}, "", "")
		if e == nil || e.Error() != test.code {
			t.Fatal("tool-only question/admission boundary", test.size, e)
		}
	}
}

func TestWorkflowConfiguredKnowledgeNeverFallsBackToTools(t *testing.T) {
	s := &Service{Identity: &identity.Service{}}
	s.Identity.Config.Workflows.Enabled = true
	s.Identity.Config.Plugins.Enabled = true
	s.Identity.Config.Knowledge.Enabled = true
	s.Knowledge = &knowledge.Service{Identity: s.Identity}
	s.Plugins = &plugins.Service{Identity: s.Identity}
	p := s.Identity.Config.Policy()
	p.Workflows.QuestionBytes = 128
	p.Knowledge.QueryBytes = 32
	s.Identity.Config.Runtime = &p
	graph, e := s.initialGraph()
	if e != nil || graph.IsTool() || len(graph.Graph.Nodes) != 4 || s.questionBytes() != 32 {
		t.Fatal("enabled retrieval configuration was silently converted", e)
	}
	if e = s.Start(context.Background()); e == nil || e.Error() != "WORKFLOW_UNAVAILABLE" {
		t.Fatal("missing configured encoder admitted a fallback lifecycle", e)
	}
	s.Identity.Config.Knowledge.Enabled = false
	s.Identity.Config.Plugins.Enabled = false
	if e = s.Start(context.Background()); e == nil || e.Error() != "WORKFLOW_UNAVAILABLE" {
		t.Fatal("workflow without any selected executor started", e)
	}
}

func TestWorkflowDisabledKnowledgeRejectsAnswerReferences(t *testing.T) {
	s := &Service{Identity: &identity.Service{}}
	s.Identity.Config.Plugins.Enabled = true
	// No SQL executor or knowledge/distribution service is supplied. Refusing the
	// old graph family must precede every dependency dereference or SQL call.
	_, e := s.references(context.Background(), nil, identity.Auth{}, workspaces.Workspace{}, ParsedGraph{})
	if e == nil || e.Error() != "KNOWLEDGE_UNAVAILABLE" {
		t.Fatal("disabled knowledge references were accepted", e)
	}
}
