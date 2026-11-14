package config

import (
	"go.yaml.in/yaml/v3"
	"testing"
)

func TestWorkflowPolicyBoundsAndStrictYAML(t *testing.T) {
	defaults := DefaultWorkflowPolicy()
	if defaults.Validate() != nil {
		t.Fatal("owned safe defaults invalid")
	}
	for _, change := range []func(*WorkflowPolicy){func(p *WorkflowPolicy) { p.Concurrency = 0 }, func(p *WorkflowPolicy) { p.LeaseSeconds = p.RunSeconds }, func(p *WorkflowPolicy) { p.RunSeconds = p.RetrievalSeconds }, func(p *WorkflowPolicy) { p.GraphBytes = 1 }, func(p *WorkflowPolicy) { p.RetentionDays = 0 }, func(p *WorkflowPolicy) { p.MaximumRuns = 0 }, func(p *WorkflowPolicy) { p.HeartbeatSeconds = p.LeaseSeconds }} {
		p := defaults
		change(&p)
		if p.Validate() == nil {
			t.Fatal("unsafe workflow policy accepted")
		}
	}
	for _, raw := range []string{"concurrency: null", "concurrency: '2'", "concurrency: 2\nconcurrency: 3", "unknown: 1"} {
		p := defaults
		if yaml.Unmarshal([]byte(raw), &p) == nil {
			t.Fatal("implicit or unknown policy accepted", raw)
		}
	}
	for _, raw := range []string{"workflows: null", "workflows: {enabled: 'true'}", "runtime: {workflows: null}"} {
		if validateWorkflowYAML([]byte(raw)) == nil {
			t.Fatal("invalid workflow configuration accepted")
		}
	}
}
