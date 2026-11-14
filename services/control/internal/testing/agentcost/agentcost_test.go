package agentcost

import (
	"strings"
	"testing"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

const samplePlan = `{
  "Query Text": "SELECT 1 FROM managed_nodes n JOIN node_tasks t ON t.node_id=n.id WHERE n.id=$1",
  "Plan": {
    "Node Type": "Nested Loop",
    "Actual Rows": 1,
    "Actual Loops": 1,
    "Rows Removed by Join Filter": 2,
    "Shared Hit Blocks": 7,
    "Shared Read Blocks": 2,
    "Plans": [
      {"Node Type": "Index Scan", "Relation Name": "managed_nodes", "Actual Rows": 1, "Actual Loops": 1, "Shared Hit Blocks": 3},
      {"Node Type": "Seq Scan", "Relation Name": "node_tasks", "Actual Rows": 2, "Actual Loops": 3, "Rows Removed by Filter": 10, "Shared Hit Blocks": 4}
    ]
  }
}`

func TestParsePlanCountsWhatTheStatementTouched(t *testing.T) {
	s, e := parsePlan(samplePlan)
	if e != nil {
		t.Fatal(e)
	}
	if s.Blocks != 9 {
		t.Errorf("blocks %d: the top node sums its children, so only it counts", s.Blocks)
	}
	if s.Rows != 1+(2+10)*3+2 {
		t.Errorf("rows %d: returned and filtered rows count on every loop", s.Rows)
	}
	if strings.Join(s.SequentialScans, ",") != "node_tasks" {
		t.Errorf("scans %v", s.SequentialScans)
	}
	if _, e = parsePlan("not json"); e == nil {
		t.Fatal("an unreadable plan was accepted")
	}
}

func TestRecorderKeepsOnlyPlansOfTheRecordedSpan(t *testing.T) {
	var r Recorder
	r.notice("duration: 0.1 ms  plan:\n" + samplePlan)
	cost, e := r.Record(func() {
		r.notice("an unrelated notice")
		r.notice("duration: 0.1 ms  plan:\n" + samplePlan)
	})
	if e != nil || len(cost.Statements) != 1 {
		t.Fatal(e, len(cost.Statements))
	}
	r.notice("duration: 0.1 ms  plan:\n" + samplePlan)
	if _, e = r.Record(func() { r.notice("duration: 0.1 ms  plan:\nnot json") }); e == nil {
		t.Fatal("an unreadable plan did not fail the recording")
	}
}

func TestViolationsNameEveryBrokenRule(t *testing.T) {
	statement := func(rows, blocks int64, scans ...string) Statement {
		return Statement{Query: "SELECT", Rows: rows, Blocks: blocks, SequentialScans: scans}
	}
	declared := wire.Cost{Statements: 2, Growth: wire.GrowthFixed}
	small := Cost{Statements: []Statement{statement(10, 10)}}
	for name, c := range map[string]struct {
		large Cost
		want  string
	}{
		"same cost":     {Cost{Statements: []Statement{statement(10, 10)}}, ""},
		"small growth":  {Cost{Statements: []Statement{statement(36, 10)}}, ""},
		"cold cache":    {Cost{Statements: []Statement{statement(10, 500)}}, ""},
		"fixed table":   {Cost{Statements: []Statement{statement(10, 10, "cells")}}, ""},
		"over declared": {Cost{Statements: []Statement{statement(1, 1), statement(1, 1), statement(1, 1)}}, "declares 2"},
		"more work":     {Cost{Statements: []Statement{statement(1, 1), statement(1, 1)}}, "on the large one"},
		"rows grew":     {Cost{Statements: []Statement{statement(37, 10)}}, "scanned"},
		"no index":      {Cost{Statements: []Statement{statement(10, 10, "node_tasks")}}, "no index"},
	} {
		got := strings.Join(Violations(declared, small, c.large), "; ")
		if c.want == "" && got != "" || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestALinearCostExceedsTheAllowedGrowth(t *testing.T) {
	// A call that scans every node of a fleet of ten must fail at FleetRatio times the
	// fleet, whatever the constant cost around it.
	const fleet, fixed = 10, 20
	small := Cost{Statements: []Statement{{Rows: fleet + fixed}}}
	large := Cost{Statements: []Statement{{Rows: fleet*FleetRatio + fixed}}}
	if len(Violations(wire.Cost{Statements: 1}, small, large)) != 1 {
		t.Fatal(Violations(wire.Cost{Statements: 1}, small, large))
	}
}
