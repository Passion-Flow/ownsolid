package database_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/migrations"
)

var (
	createdTable = regexp.MustCompile(`(?m)^CREATE TABLE(?: IF NOT EXISTS)? ([a-z][a-z0-9_]*)`)
	droppedTable = regexp.MustCompile(`(?m)^DROP TABLE(?: IF EXISTS)? ([a-z][a-z0-9_]*)`)
	// telemetryName matches what a table of logs, metrics or traces would be called.
	telemetryName = regexp.MustCompile(`(^|_)(logs?|metrics?|telemetry|samples?|series|traces?|spans?)(_|$)`)
)

// recordTables are bounded records of one run or job that happen to match telemetryName.
// A table of unbounded readings belongs in the stores of ADR 0009, not on this list.
var recordTables = []string{
	"workflow_node_traces", // One row per node kind of a run: the run's own state.
}

// liveTelemetryTables returns the tables left by the scripts, in order, that are named
// like telemetry and are not known records.
func liveTelemetryTables(scripts []string) []string {
	live := map[string]bool{}
	for _, script := range scripts {
		for _, m := range createdTable.FindAllStringSubmatch(script, -1) {
			live[m[1]] = true
		}
		for _, m := range droppedTable.FindAllStringSubmatch(script, -1) {
			delete(live, m[1])
		}
	}
	found := []string{}
	for name := range live {
		if telemetryName.MatchString(name) && !slices.Contains(recordTables, name) {
			found = append(found, name)
		}
	}
	slices.Sort(found)
	return found
}

// ADR 0012: telemetry stays out of PostgreSQL. This fails a migration that brings it back.
func TestTelemetryStaysOutOfTheDatabase(t *testing.T) {
	entries, e := migrations.Files.ReadDir("postgresql")
	if e != nil {
		t.Fatal(e)
	}
	scripts := []string{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		raw, e := migrations.Files.ReadFile("postgresql/" + entry.Name())
		if e != nil {
			t.Fatal(e)
		}
		scripts = append(scripts, string(raw))
	}
	if len(scripts) == 0 {
		t.Fatal("no migrations found")
	}
	if found := liveTelemetryTables(scripts); len(found) != 0 {
		t.Fatalf("telemetry tables in the control-plane database: %v; use the stores of ADR 0009", found)
	}
}

func TestLiveTelemetryTablesRecognizesWhatItGuards(t *testing.T) {
	for _, name := range []string{"node_metric_samples", "gpu_metrics", "component_log_snapshots", "app_logs", "request_traces", "telemetry_events", "span_links"} {
		if got := liveTelemetryTables([]string{"CREATE TABLE " + name + " (id int);"}); len(got) != 1 || got[0] != name {
			t.Errorf("%s passed the guard: %v", name, got)
		}
		if got := liveTelemetryTables([]string{"CREATE TABLE " + name + " (id int);", "DROP TABLE " + name + ";"}); len(got) != 0 {
			t.Errorf("%s was dropped but still reported: %v", name, got)
		}
	}
	for _, name := range []string{"audit_events", "catalog_entries", "dialogs", "workflow_node_traces"} {
		if got := liveTelemetryTables([]string{"CREATE TABLE " + name + " (id int);"}); len(got) != 0 {
			t.Errorf("%s was flagged: %v", name, got)
		}
	}
}
