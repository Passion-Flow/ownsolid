package main

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

func TestSchemaUpgradeCommandsAdmitOnlyExplicitContextFlags(t *testing.T) {
	previous := os.Args
	defer func() { os.Args = previous }()
	for _, command := range []string{databaseaccess.UpgradeCommand, databaseaccess.UpgradeInspectCommand} {
		for _, args := range [][]string{
			{"--config", "relative", "--runtime-config", "/private/runtime.yaml"},
			{"--config", "/private/operator.yaml", "--config=/private/other.yaml", "--runtime-config", "/private/runtime.yaml"},
			{"--runtime-config", "/private/runtime.yaml", "--runtime-config=/private/other.yaml"},
			{"--env-file", "/private/policy.env", "--env-file=/private/other.env"},
			{"--config=", "--runtime-config", "/private/runtime.yaml"},
			{"--owner-uid=0"},
			{"--env-file", "/private/../alias.env"},
		} {
			os.Args = append([]string{"control", command}, args...)
			if e := run(); e == nil || e.Error() != "SCHEMA_UPGRADE_ARGUMENT_INVALID" {
				t.Fatal("ambiguous/foreign context admitted", command, e)
			}
		}
		os.Args = []string{"control", command, "--env-file", "/private/policy.env", "--runtime-config", "/private/runtime.yaml"}
		if e := run(); e == nil || e.Error() != "CONFIG_REQUIRED" {
			t.Fatal("valid explicit env flag rejected before required config", e)
		}
		os.Args = []string{"control", command, "--config", "/unavailable/operator.yaml", "--env-file", "/private/policy.env"}
		if e := run(); e == nil || e.Error() != "RUNTIME_ACCESS_CONFIGURATION_REQUIRED" {
			t.Fatal("missing runtime context reached configuration/database", e)
		}
	}
}

func TestServingAdmissionRetainsLostOriginalLeaseBeforeRuntimeQueries(t *testing.T) {
	c := config.Config{}
	if e := verifyServingDatabase(context.Background(), c, nil); e == nil || e.Error() != "SELECTED_DATABASE_REQUIRED" {
		t.Fatal("missing selected owner admitted", e)
	}
	pool := new(pgxpool.Pool)
	store, e := transaction.FromPostgreSQL(pool)
	if e != nil {
		t.Fatal(e)
	}
	selected := &selectedDatabase{postgres: pool, store: store, verify: make(chan struct{}, 1)}
	selected.leaseLost.Store(true)
	if e = verifyServingDatabase(t.Context(), c, selected); e == nil || e.Error() != "INSTALLATION_LOCK_LOST" {
		t.Fatal("runtime/schema query bypassed original lost lease", e)
	}
	if config.DefaultRuntimePolicy().HTTP.InstallationLeaseIntervalSeconds <= 0 {
		t.Fatal("full activation disabled mandatory observer")
	}
}
