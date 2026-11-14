package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/monitoringmetrics"
)

func TestMonitoringPreservesInstallationLeaseAdmission(t *testing.T) {
	for _, endpoint := range []string{"primary", "machine", "primary_without_machine"} {
		t.Run(endpoint, func(t *testing.T) {
			// The latched loss rejects before SQL; no database is opened.
			pool := new(pgxpool.Pool)
			store, err := transaction.FromPostgreSQL(pool)
			if err != nil {
				t.Fatal(err)
			}
			selected := &selectedDatabase{postgres: pool, store: store, verify: make(chan struct{}, 1)}
			selected.leaseLost.Store(true)
			lifecycle := newRuntimeLifecycle(t.Context())
			t.Cleanup(func() { lifecycle.cancel(context.Canceled) })
			entered := false
			business := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entered = true
				w.WriteHeader(http.StatusOK)
			})
			fence := installationLeaseMiddleware(lifecycle, selected, config.DefaultHTTPPolicy())
			primary := fence(business)
			var machine http.Handler
			if endpoint != "primary_without_machine" {
				machine = fence(business)
			}
			recorder, err := monitoringmetrics.New(monitoringmetrics.Defaults(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			primary, machine = monitoringServingHandlers(recorder, primary, machine)
			if endpoint == "primary_without_machine" && machine != nil {
				t.Fatal("disabled machine entrance allocated")
			}
			handler := primary
			if endpoint == "machine" {
				handler = machine
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://admin.example.invalid/api/v1/me", nil))
			if response.Code != http.StatusServiceUnavailable || entered || lifecycle.Failure() == nil {
				t.Fatal("monitoring bypassed the lost installation lease", response.Code, entered, lifecycle.Failure())
			}
			var recorded uint64
			for _, series := range recorder.Snapshot().Series {
				recorded += series.Count
			}
			if recorded != 1 {
				t.Fatal("lease refusal was not observed exactly once", recorded)
			}
		})
	}
}

func TestMonitoringCompositionRequiresActualSelectedOwner(t *testing.T) {
	c := config.Config{}
	if h, recorder, e := openMonitoring(context.Background(), c, nil, nil, nil); e != nil || h != nil || recorder != nil {
		t.Fatal("disabled module allocated", e)
	}
	c.MonitoringConfigFile = "relative"
	if h, recorder, e := openMonitoring(context.Background(), c, nil, nil, nil); e == nil || h != nil || recorder != nil || e.Error() != "CONFIG_MONITORING_INVALID" {
		t.Fatal("invalid reference allocated", e)
	}
	c.MonitoringConfigFile = "/private/monitoring.json"
	for _, ctx := range []context.Context{nil, context.Background()} {
		if h, recorder, e := openMonitoring(ctx, c, nil, nil, nil); e == nil || h != nil || recorder != nil || e.Error() != "MONITORING_OWNER_REQUIRED" {
			t.Fatal("foreign composition allocated", e)
		}
	}
}
