package config

import (
	"errors"
	"go.yaml.in/yaml/v3"
	"reflect"
)

type WorkflowConfiguration struct {
	Enabled bool `yaml:"enabled"`
}
type WorkflowPolicy struct {
	GraphBytes           int `yaml:"graph_bytes"`
	NameCharacters       int `yaml:"name_characters"`
	InstructionsBytes    int `yaml:"instructions_bytes"`
	QuestionBytes        int `yaml:"question_bytes"`
	AnswerBytes          int `yaml:"answer_bytes"`
	RunSeconds           int `yaml:"run_seconds"`
	RetrievalSeconds     int `yaml:"retrieval_seconds"`
	AnswerSeconds        int `yaml:"answer_seconds"`
	Concurrency          int `yaml:"concurrency"`
	WorkspaceConcurrency int `yaml:"workspace_concurrency"`
	LeaseSeconds         int `yaml:"lease_seconds"`
	HeartbeatSeconds     int `yaml:"heartbeat_seconds"`
	CommitSeconds        int `yaml:"commit_seconds"`
	MaximumWorkflows     int `yaml:"maximum_workflows"`
	MaximumVersions      int `yaml:"maximum_versions"`
	MaximumConversations int `yaml:"maximum_conversations"`
	MaximumRuns          int `yaml:"maximum_runs"`
	MaximumTurns         int `yaml:"maximum_turns"`
	RetentionDays        int `yaml:"retention_days"`
	MaintenanceBatch     int `yaml:"maintenance_batch"`
	MaintenanceSeconds   int `yaml:"maintenance_seconds"`
}

func DefaultWorkflowPolicy() WorkflowPolicy {
	return WorkflowPolicy{GraphBytes: 65536, NameCharacters: 120, InstructionsBytes: 4096, QuestionBytes: 4096, AnswerBytes: 65536, RunSeconds: 180, RetrievalSeconds: 60, AnswerSeconds: 60, Concurrency: 2, WorkspaceConcurrency: 2, LeaseSeconds: 240, HeartbeatSeconds: 5, CommitSeconds: 10, MaximumWorkflows: 100, MaximumVersions: 100, MaximumConversations: 100, MaximumRuns: 1000, MaximumTurns: 1000, RetentionDays: 30, MaintenanceBatch: 100, MaintenanceSeconds: 60}
}
func (p WorkflowPolicy) Validate() error {
	bad := errors.New("CONFIG_WORKFLOW_POLICY_INVALID")
	if p.GraphBytes < 4096 || p.GraphBytes > 1048576 || p.NameCharacters < 1 || p.NameCharacters > 120 || p.InstructionsBytes < 64 || p.InstructionsBytes > 16384 || p.QuestionBytes < 32 || p.QuestionBytes > 32768 || p.AnswerBytes < 1024 || p.AnswerBytes > 1048576 {
		return bad
	}
	if p.RetrievalSeconds < 5 || p.RetrievalSeconds > 120 || p.AnswerSeconds < 5 || p.AnswerSeconds > 120 || p.CommitSeconds < 1 || p.CommitSeconds > 30 || p.RunSeconds < p.RetrievalSeconds+p.AnswerSeconds+p.CommitSeconds || p.RunSeconds > 300 {
		return bad
	}
	if p.Concurrency < 1 || p.Concurrency > 4 || p.WorkspaceConcurrency < 1 || p.WorkspaceConcurrency > 8 || p.LeaseSeconds < p.RunSeconds+p.CommitSeconds || p.LeaseSeconds > 900 || p.HeartbeatSeconds < 1 || p.HeartbeatSeconds > 30 || p.HeartbeatSeconds*3 >= p.LeaseSeconds {
		return bad
	}
	if p.MaximumRuns < 1 || p.MaximumRuns > 10000 || p.MaximumWorkflows < 1 || p.MaximumWorkflows > 1000 || p.MaximumVersions < 1 || p.MaximumVersions > 1000 || p.MaximumConversations < 1 || p.MaximumConversations > 1000 || p.MaximumTurns < 1 || p.MaximumTurns > 10000 || p.RetentionDays < 1 || p.RetentionDays > 365 || p.MaintenanceBatch < 1 || p.MaintenanceBatch > 1000 || p.MaintenanceSeconds < 1 || p.MaintenanceSeconds > 3600 {
		return bad
	}
	return nil
}
func (p *WorkflowPolicy) UnmarshalYAML(node *yaml.Node) error {
	if e := strictKnowledgeYAML(node, reflect.TypeOf(*p)); e != nil {
		return errors.New("CONFIG_WORKFLOW_POLICY_INVALID")
	}
	type plain WorkflowPolicy
	value := plain(*p)
	if e := node.Decode(&value); e != nil {
		return errors.New("CONFIG_WORKFLOW_POLICY_INVALID")
	}
	*p = WorkflowPolicy(value)
	return nil
}
func (p *WorkflowConfiguration) UnmarshalYAML(node *yaml.Node) error {
	if e := strictKnowledgeYAML(node, reflect.TypeOf(*p)); e != nil {
		return errors.New("CONFIG_WORKFLOW_INVALID")
	}
	type plain WorkflowConfiguration
	var value plain
	if e := node.Decode(&value); e != nil {
		return errors.New("CONFIG_WORKFLOW_INVALID")
	}
	*p = WorkflowConfiguration(value)
	return nil
}

func validateWorkflowYAML(raw []byte) error {
	var document yaml.Node
	if yaml.Unmarshal(raw, &document) != nil || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("CONFIG_WORKFLOW_INVALID")
	}
	root := document.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		name, value := root.Content[i].Value, root.Content[i+1]
		if name == "workflows" {
			if e := strictKnowledgeYAML(value, reflect.TypeOf(WorkflowConfiguration{})); e != nil {
				return errors.New("CONFIG_WORKFLOW_INVALID")
			}
		}
		if name == "runtime" && value.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(value.Content); j += 2 {
				if value.Content[j].Value == "workflows" {
					if e := strictKnowledgeYAML(value.Content[j+1], reflect.TypeOf(WorkflowPolicy{})); e != nil {
						return errors.New("CONFIG_WORKFLOW_POLICY_INVALID")
					}
				}
			}
		}
	}
	return nil
}
