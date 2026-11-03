//go:build repository_integration

package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/monitoring"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
)

func TestSelectedPostgreSQLMonitoringAssemblyUsesCurrentPool(t *testing.T) {
	accounts := fixture.New(t)
	fixture.Bootstrap(t, accounts)
	store, e := transaction.FromPostgreSQL(accounts.DB)
	if e != nil {
		t.Fatal(e)
	}
	selected := &selectedDatabase{postgres: accounts.DB, store: store}
	c := accounts.Config
	root := t.TempDir()
	if e = os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	c.MonitoringConfigFile = filepath.Join(root, "monitoring.json")
	raw, e := json.Marshal(monitoring.Defaults())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(c.MonitoringConfigFile, raw, 0600); e != nil {
		t.Fatal(e)
	}
	h := identityhttp.New(accounts)
	if e = h.InitializationError(); e != nil {
		t.Fatal(e)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	configured, recorder, e := openMonitoring(t.Context(), c, selected, h, logger)
	if e != nil || configured == nil || recorder == nil {
		t.Fatal("actual selected pool failed monitoring assembly", e)
	}
	foreign := releaseViewPool(t)
	wrong := selectedDatabase{postgres: foreign, store: store}
	if out, metrics, e := openMonitoring(t.Context(), c, &wrong, h, nil); e == nil || out != nil || metrics != nil || e.Error() != "MONITORING_OWNER_REQUIRED" {
		t.Fatal("foreign pool admitted monitoring", e)
	}
}
