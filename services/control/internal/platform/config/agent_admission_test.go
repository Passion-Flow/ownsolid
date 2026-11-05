package config

import (
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func TestAgentAdmissionPolicyBounds(t *testing.T) {
	if e := DefaultAgentAdmissionPolicy().Validate(); e != nil {
		t.Fatal(e)
	}
	for name, mutate := range map[string]func(*AgentAdmissionPolicy){
		"no seats":              func(p *AgentAdmissionPolicy) { p.Seats = 0 },
		"too many seats":        func(p *AgentAdmissionPolicy) { p.Seats = maximumAgentSeats + 1 },
		"cap above seats":       func(p *AgentAdmissionPolicy) { p.Heartbeat.MaxSeats = p.Seats + 1 },
		"no seat for a class":   func(p *AgentAdmissionPolicy) { p.Quarantine.MaxSeats = 0 },
		"negative queue":        func(p *AgentAdmissionPolicy) { p.Bulk.QueueLength = -1 },
		"queue too long":        func(p *AgentAdmissionPolicy) { p.Bulk.QueueLength = maximumAgentQueueLength + 1 },
		"wait too long":         func(p *AgentAdmissionPolicy) { p.Telemetry.WaitMilliseconds = maximumAgentWaitMilliseconds + 1 },
		"no retry hint":         func(p *AgentAdmissionPolicy) { p.Heartbeat.RetrySeconds = 0 },
		"retry hint too long":   func(p *AgentAdmissionPolicy) { p.Heartbeat.RetrySeconds = agentwire.MaxRetryAfterSeconds + 1 },
		"no address rate":       func(p *AgentAdmissionPolicy) { p.Address.PerMinute = 0 },
		"no address burst":      func(p *AgentAdmissionPolicy) { p.Address.Burst = 0 },
		"call rate too high":    func(p *AgentAdmissionPolicy) { p.Calls.Download.PerMinute = maximumAgentCallsPerMinute + 1 },
		"call burst too high":   func(p *AgentAdmissionPolicy) { p.Calls.Renew.Burst = maximumAgentCallBurst + 1 },
		"missing call limit":    func(p *AgentAdmissionPolicy) { p.Calls.Stop = AgentCallLimit{} },
		"too few tracked keys":  func(p *AgentAdmissionPolicy) { p.TrackedKeys = minimumAgentTrackedKeys - 1 },
		"too many tracked keys": func(p *AgentAdmissionPolicy) { p.TrackedKeys = maximumAgentTrackedKeys + 1 },
		"lower classes fill every seat": func(p *AgentAdmissionPolicy) {
			p.Telemetry.MaxSeats, p.Bulk.MaxSeats = p.Seats/2, p.Seats-p.Seats/2
		},
	} {
		p := DefaultAgentAdmissionPolicy()
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestEveryNodeCallHasALimitAndEnrollmentHasNone(t *testing.T) {
	p := DefaultAgentAdmissionPolicy()
	for _, c := range agentwire.Calls() {
		limit, ok := p.Call(c)
		if c == agentwire.CallEnroll {
			if ok {
				t.Fatal("enrollment has a per-node limit")
			}
			continue
		}
		if !ok || !limit.valid() {
			t.Errorf("%s: limit %+v %v", c, limit, ok)
		}
	}
	for _, priority := range agentwire.Priorities() {
		if p.Priority(priority).MaxSeats == 0 {
			t.Errorf("%s has no bounds", priority)
		}
	}
}
