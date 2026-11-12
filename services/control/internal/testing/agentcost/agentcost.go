// Package agentcost measures what one agent call costs PostgreSQL. It records the executed
// plan of every statement through auto_explain, so a measurement counts what the database
// really did, including statements run by triggers (ADR 0020).
package agentcost

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// planMarker ends the prefix auto_explain writes before the plan document.
const planMarker = "plan:\n"

// Statement is one executed statement and what it touched.
type Statement struct {
	Query string
	// Blocks counts the shared buffers the statement hit or read.
	Blocks int64
	// Rows counts the rows its scans returned or filtered out.
	Rows int64
	// SequentialScans lists the tables it read start to end.
	SequentialScans []string
}

// Cost is what a span of statements cost.
type Cost struct {
	Statements []Statement
}

// Blocks sums the shared buffers of every statement.
func (c Cost) Blocks() int64 {
	var total int64
	for _, s := range c.Statements {
		total += s.Blocks
	}
	return total
}

// Rows sums the rows of every statement.
func (c Cost) Rows() int64 {
	var total int64
	for _, s := range c.Statements {
		total += s.Rows
	}
	return total
}

// fixedTables hold one row per cell or site, however large the fleet is, so a sequential
// scan of them costs nothing that grows.
var fixedTables = map[string]bool{"cells": true, "sites": true}

// Limits of a measurement. Plans may differ a little between a small and a large fleet, so
// rows may rise a little; a fleet FleetRatio times larger that scans no more than this has
// not grown with the fleet. Blocks are only reported: a connection with a cold catalog
// cache reads a hundred more of them, which says nothing about the fleet.
const (
	// FleetRatio is the least ratio of the large to the small fleet. A cost linear in
	// the fleet exceeds the allowed growth at this ratio.
	FleetRatio = 30
	rowsFactor = 2
	rowsMargin = 16
)

// Violations compares one exchange measured on a small and on a large fleet with what its
// call declares. It returns one sentence per broken rule.
func Violations(declared wire.Cost, small, large Cost) []string {
	out := []string{}
	if n := len(large.Statements); n > declared.Statements {
		out = append(out, fmt.Sprintf("ran %d statements, declares %d", n, declared.Statements))
	}
	if a, b := len(small.Statements), len(large.Statements); a != b {
		out = append(out, fmt.Sprintf("ran %d statements on the small fleet and %d on the large one", a, b))
	}
	if a, b := small.Rows(), large.Rows(); b > rowsFactor*a+rowsMargin {
		out = append(out, fmt.Sprintf("scanned %d rows on the small fleet and %d on the large one", a, b))
	}
	for _, s := range large.Statements {
		for _, table := range s.SequentialScans {
			if !fixedTables[table] {
				out = append(out, fmt.Sprintf("has no index for its read of %s: %s", table, s.Query))
			}
		}
	}
	return out
}

// Recorder collects executed plans from the connections of a pool it configured.
type Recorder struct {
	mu        sync.Mutex
	recording bool
	cost      Cost
	failure   error
}

// Configure turns on auto_explain for every connection of the pool. The connecting role
// must be a superuser, which every test database role is. Sequential scans are priced out,
// so one that still runs means no index serves the read, even on a table this test left
// small.
func (r *Recorder) Configure(c *pgxpool.Config) {
	params := c.ConnConfig.RuntimeParams
	params["session_preload_libraries"] = "auto_explain"
	params["enable_seqscan"] = "off"
	params["auto_explain.log_min_duration"] = "0"
	params["auto_explain.log_parameter_max_length"] = "0"
	params["auto_explain.log_analyze"] = "on"
	params["auto_explain.log_buffers"] = "on"
	params["auto_explain.log_timing"] = "off"
	params["auto_explain.log_nested_statements"] = "on"
	params["auto_explain.log_format"] = "json"
	params["auto_explain.log_level"] = "notice"
	c.ConnConfig.OnNotice = func(_ *pgconn.PgConn, notice *pgconn.Notice) { r.notice(notice.Message) }
}

// Record runs fn and returns the cost of the statements it ran.
func (r *Recorder) Record(fn func()) (Cost, error) {
	r.mu.Lock()
	r.recording, r.cost, r.failure = true, Cost{}, nil
	r.mu.Unlock()
	fn()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recording = false
	return r.cost, r.failure
}

func (r *Recorder) notice(message string) {
	_, document, found := strings.Cut(message, planMarker)
	if !found {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.recording {
		return
	}
	statement, e := parsePlan(document)
	if e != nil {
		r.failure = e
		return
	}
	r.cost.Statements = append(r.cost.Statements, statement)
}

type planNode struct {
	Type         string     `json:"Node Type"`
	Relation     string     `json:"Relation Name"`
	Rows         float64    `json:"Actual Rows"`
	Loops        float64    `json:"Actual Loops"`
	Filtered     float64    `json:"Rows Removed by Filter"`
	JoinFiltered float64    `json:"Rows Removed by Join Filter"`
	RecheckLoss  float64    `json:"Rows Removed by Index Recheck"`
	Hit          int64      `json:"Shared Hit Blocks"`
	Read         int64      `json:"Shared Read Blocks"`
	Children     []planNode `json:"Plans"`
}

func parsePlan(document string) (Statement, error) {
	var parsed struct {
		Query string   `json:"Query Text"`
		Plan  planNode `json:"Plan"`
	}
	if e := json.Unmarshal([]byte(document), &parsed); e != nil {
		return Statement{}, fmt.Errorf("auto_explain plan: %w", e)
	}
	out := Statement{Query: parsed.Query, Blocks: parsed.Plan.Hit + parsed.Plan.Read}
	parsed.Plan.collect(&out)
	return out, nil
}

func (n planNode) collect(out *Statement) {
	if n.Relation != "" {
		out.Rows += int64((n.Rows + n.Filtered + n.RecheckLoss) * n.Loops)
		if n.Type == "Seq Scan" {
			out.SequentialScans = append(out.SequentialScans, n.Relation)
		}
	}
	out.Rows += int64(n.JoinFiltered * n.Loops)
	for _, child := range n.Children {
		child.collect(out)
	}
}
