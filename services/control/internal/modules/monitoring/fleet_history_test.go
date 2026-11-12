package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
)

func TestFleetHistoryConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitoring.json")
	const valid = `{"fleet_history":{"raw_origin":"https://raw.example.invalid","long_origin":"https://long.example.invalid","raw_horizon_seconds":7200,"max_range_seconds":7776000,"settling_seconds":60}}`
	for _, tc := range []struct {
		input string
		valid bool
	}{
		{"{}", true},
		{valid, true},
		{strings.Replace(valid, "https://long.example.invalid", "https://RAW.example.invalid.:443", 1), false},
		{strings.Replace(valid, "https://raw.example.invalid", "http://raw.example.invalid", 1), false},
		{strings.Replace(valid, "https://raw.example.invalid", "https://user@raw.example.invalid", 1), false},
		{strings.Replace(valid, "https://raw.example.invalid", "https://raw.example.invalid:65536", 1), false},
		{strings.Replace(valid, "7200", "90", 1), false},
		{strings.Replace(valid, "7200", "null", 1), false},
		{strings.Replace(valid, "settling_seconds", "unknown_setting", 1), false},
		{`{"fleet_history":null}`, false},
	} {
		if e := os.WriteFile(path, []byte(tc.input), 0600); e != nil {
			t.Fatal(e)
		}
		_, e := Load(path)
		if (e == nil) != tc.valid {
			t.Fatalf("unexpected result loading %s: %v", tc.input, e)
		}
	}
}

func TestFleetHistoryValidatesNativeSeries(t *testing.T) {
	workspace := workspaces.Workspace{ID: "fixture-workspace", TenantID: "fixture-tenant"}
	now := time.Unix(12_000, 0).UTC()
	start, end := now.Add(-90*time.Second), now.Add(-60*time.Second)
	row := func(kind, scope, values string) string {
		return fmt.Sprintf(`{"metric":{"tenant_id":%q,"workspace_id":"fixture-workspace","site_id":"s","cell_id":"c","measure":%q},"values":%s}`, scope, kind, values)
	}
	observed := row("observed", workspace.TenantID, `[[11910,"1"],[11940,"1"]]`)
	reachable := row("reachable", workspace.TenantID, `[[11910,"0"],[11940,"1"]]`)
	for _, tc := range []struct {
		name, rows string
		partial    bool
		ok         bool
	}{
		{"observed zero and missing interval", observed + "," + reachable, false, true},
		{"empty result", "", false, true},
		{"missing measure", reachable, false, false},
		{"excess reachable", observed + "," + strings.Replace(reachable, `"0"`, `"2"`, 1), false, false},
		{"foreign series", observed + "," + reachable + "," + row("observed", "foreign", `[[11910,"1"]]`), false, false},
		{"duplicate timestamp", observed + "," + strings.Replace(reachable, "11940", "11910", 1), false, false},
		{"off-grid", observed + "," + strings.Replace(reachable, "11910", "11911", 1), false, false},
		{"partial result", observed + "," + reachable, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if r.URL.Path != "/api/v1/query_range" || q.Get("latency_offset") != "1ms" || q.Get("deny_partial_response") != "1" ||
					strings.Join(q["extra_label"], ",") != "tenant_id=fixture-tenant,workspace_id=fixture-workspace" {
					t.Error("scope or query safety options missing", r.URL)
				}
				fmt.Fprintf(w, `{"status":"success","isPartial":%t,"data":{"resultType":"matrix","result":[%s]}}`, tc.partial, tc.rows)
			}))
			t.Cleanup(store.Close)
			r := newFleetHistoryReader(FleetHistoryPolicy{RawOrigin: store.URL, RawHorizonSeconds: 3600, MaxRangeSeconds: 7200, SettlingSeconds: 30})
			out, e := r.read(t.Context(), workspace, now, start, end)
			if tc.ok {
				if e != nil || out.StepSeconds != 15 || out.Cells == nil {
					t.Fatalf("unexpected result: %+v %v", out, e)
				}
				if tc.rows != "" {
					if len(out.Cells) != 1 || len(out.Cells[0].Points) != 2 {
						t.Fatalf("wrong cell or points: %+v", out)
					}
					points := out.Cells[0].Points
					if points[0].Reachable != 0 || points[0].Observed != 1 || !points[1].At.Equal(end) {
						t.Fatalf("missing and zero are conflated: %+v", points)
					}
				}
				return
			}
			if e == nil || e.Error() != "MONITORING_RAW_TIER_UNAVAILABLE" || out.Cells != nil {
				t.Fatalf("invalid store data published: %+v %v", out, e)
			}
		})
	}
}

func TestFleetHistoryDoesNotFollowStoreRedirects(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("store redirected a scoped query")
	}))
	defer redirect.Close()
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirect.URL, http.StatusFound)
	}))
	defer store.Close()
	r := newFleetHistoryReader(FleetHistoryPolicy{RawOrigin: store.URL, RawHorizonSeconds: 3600, MaxRangeSeconds: 7200, SettlingSeconds: 30})
	now := time.Unix(12_000, 0)
	_, e := r.read(context.Background(), workspaces.Workspace{}, now, now.Add(-90*time.Second), now.Add(-60*time.Second))
	if e == nil || e.Error() != "MONITORING_RAW_TIER_UNAVAILABLE" {
		t.Fatal("unexpected redirect result", e)
	}
}

func TestFleetHistoryJSONIncludesServingEnvelopeInBudget(t *testing.T) {
	// Isolate serialization size from the reader's separate label validation.
	// This makes a one-byte wire-format change observable at the exact cap.
	out := FleetHistory{Cells: []FleetHistoryCell{{SiteID: "site", CellID: "cell"}}}
	base, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	out.Cells[0].SiteID += strings.Repeat("x", maximumHistoryJSONBytes-historyJSONEnvelopeBytes-len(base))
	body, err := encodeFleetHistoryJSON(out)
	if err != nil {
		t.Fatal(err)
	}
	var served bytes.Buffer
	if err := json.NewEncoder(&served).Encode(map[string]any{"data": body}); err != nil {
		t.Fatal(err)
	}
	if served.Len() != maximumHistoryJSONBytes || !bytes.HasSuffix(served.Bytes(), []byte("}\n")) {
		t.Fatalf("served body has %d bytes, want %d", served.Len(), maximumHistoryJSONBytes)
	}
	out.Cells[0].SiteID += "x"
	body, err = encodeFleetHistoryJSON(out)
	if body != nil || err == nil || err.Error() != "MONITORING_HISTORY_RESPONSE_LIMIT" {
		t.Fatalf("oversized served body was accepted: %d bytes, %v", len(body), err)
	}
}
