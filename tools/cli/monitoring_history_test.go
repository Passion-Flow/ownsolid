package cli

import (
	"encoding/json"
	"encoding/pem"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestMonitoringHistoryArgumentsBeforeIO(t *testing.T) {
	t.Setenv("OWNSOLID_CONFIG_DIR", "unusable")
	for _, pair := range [][2]string{
		{"", "2026-11-01T01:00:00Z"},
		{"2026-11-01T00:00:00Z", ""},
		{"2026-11-01T00:00:00+00:00", "2026-11-01T01:00:00Z"},
		{"2026-11-01T00:00:00.0Z", "2026-11-01T01:00:00Z"},
		{"2026-02-30T00:00:00Z", "2026-11-01T01:00:00Z"},
		{"1969-12-31T23:59:59Z", "2026-11-01T01:00:00Z"},
		{"2026-11-01T01:00:00Z", "2026-11-01T01:00:00Z"},
		{"2026-11-01T02:00:00Z", "2026-11-01T01:00:00Z"},
	} {
		out, _, err := run(t, "monitoring", "history", "--from", pair[0], "--to", pair[1])
		if out != "" || err == nil || !strings.Contains(err.Error(), "--") {
			t.Fatalf("invalid interval reached settings or produced output: %q, %v", out, err)
		}
	}
	for _, args := range [][]string{{"monitoring", "--help"}, {"monitoring", "history", "--help"}} {
		if out, _, err := run(t, args...); err != nil || !strings.Contains(out, "Examples:") {
			t.Fatalf("missing local history help: %q, %v", out, err)
		}
	}
}

func TestMonitoringHistoryPreservesSparseUnsignedCountsAndRetries(t *testing.T) {
	const from, to = "2026-11-01T00:00:00Z", "2026-11-01T01:00:00Z"
	at, _ := parseHistoryTime(from, "--from")
	end, _ := parseHistoryTime(to, "--to")
	history := fleetHistory{Tier: "raw", StepSeconds: 15, From: at, To: end,
		Cells: []fleetHistoryCell{{SiteID: "site-1", CellID: "cell-other",
			Points: []fleetHistoryPoint{{At: at, Observed: math.MaxUint64, Reachable: 0}}}}}
	session := token(t)
	var failure, empty atomic.Bool
	var reads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("__Host-admin_session")
		if err != nil || cookie.Value != session || r.Method != http.MethodGet ||
			r.URL.Path != adminPath+"monitoring/fleet-history" ||
			r.URL.Query().Encode() != "from=2026-11-01T00%3A00%3A00Z&to=2026-11-01T01%3A00%3A00Z&workspace_id="+workspaceID {
			t.Error("history request changed its session, scope or canonical interval")
			w.WriteHeader(400)
			return
		}
		reads.Add(1)
		if failure.Load() {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"code":"MONITORING_LONG_TIER_UNAVAILABLE"}`))
			return
		}
		value := history
		if empty.Load() {
			value.Cells = []fleetHistoryCell{}
		}
		respond(w, value)
	}))
	defer server.Close()
	dir := privateDir(t)
	t.Setenv("OWNSOLID_CONFIG_DIR", dir)
	t.Setenv("OWNSOLID_SERVER", "")
	ca := filepath.Join(dir, "ca.pem")
	writeFile(t, ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	writeConfig(t, dir, fileConfig{Server: server.URL, CAFile: ca, SiteID: "site-a", CellID: "cell-b", WorkspaceID: workspaceID})
	cfg, err := loadSettings("", false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := openSessionStore(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	if err := store.save(session); err != nil {
		t.Fatal(err)
	}
	args := []string{"monitoring", "history", "--from", from, "--to", to}
	for _, format := range []string{"json", "yaml", "table"} {
		out, stderr, err := run(t, append(args, "-o", format)...)
		if err != nil || stderr != "" || !strings.Contains(out, "18446744073709551615") {
			t.Fatalf("%s lost unsigned count: %q, %s, %v", format, out, stderr, err)
		}
		if format == "table" {
			if !strings.Contains(out, "STEP 15s") || !strings.Contains(out, "cell-other") || !strings.Contains(out, "REACHABLE") {
				t.Fatal("table lost grid or cell metadata", out)
			}
			continue
		}
		var got fleetHistory
		if format == "json" {
			err = json.Unmarshal([]byte(out), &got)
		} else {
			err = yaml.Unmarshal([]byte(out), &got)
		}
		if err != nil || len(got.Cells) != 1 || len(got.Cells[0].Points) != 1 ||
			got.Cells[0].Points[0].Observed != math.MaxUint64 || got.Cells[0].Points[0].Reachable != 0 {
			t.Fatalf("sparse zero or unsigned count changed in %s: %q, %v", format, out, err)
		}
	}
	empty.Store(true)
	out, _, err := run(t, append(args, "-o", "json")...)
	if err != nil || !strings.Contains(out, `"cells": []`) {
		t.Fatalf("empty history is not a success: %q, %v", out, err)
	}
	empty.Store(false)
	failure.Store(true)
	out, _, err = run(t, append(args, "-o", "json")...)
	if out != "" || err == nil || !strings.Contains(err.Error(), "MONITORING_LONG_TIER_UNAVAILABLE") {
		t.Fatalf("store failure projected success: %q, %v", out, err)
	}
	failure.Store(false)
	out, _, err = run(t, append(args, "--workspace", workspaceID, "-o", "json")...)
	if err != nil || out == "" || reads.Load() != 6 {
		t.Fatalf("retry failed: %q, %v, reads %d", out, err, reads.Load())
	}
}
