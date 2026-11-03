package main

import (
	"bytes"
	"testing"
)

func TestPostgresMaintenanceStrictArgumentsRefuseBeforePrivateInputs(t *testing.T) {
	for _, args := range [][]string{nil, {"--config", ""}, {"--config=/private/one", "--config=/private/two"}, {"--config=/private/one", "-config=/private/two"}, {"--config=/private/one", "--env-file="}, {"--config=relative"}, {"--config=/private/../one"}, {"--config=/private/one", "--reason=unused"}, {"--config=/private/one", "--backup-root=/private/unused"}, {"--config=/private/one", "positional"}, {"--config=/private/one", "--env-file=/private/a", "--env-file=/private/b"}} {
		var output bytes.Buffer
		e := runPostgresMaintenanceCheck(args, &output)
		if e == nil || e.Error() != "POSTGRES_MAINTENANCE_ARGUMENT_INVALID" || output.Len() != 0 {
			t.Fatal("unsafe maintenance inputs", e)
		}
	}
}
func TestPostgresMaintenanceExplicitCanonicalPathsRetainOriginalOverlay(t *testing.T) {
	configuration, environment, e := postgresMaintenancePaths([]string{"--config", "/private/one", "--env-file=/private/policy.env"})
	if e != nil || configuration != "/private/one" || environment != "/private/policy.env" {
		t.Fatal(configuration, environment, e)
	}
	configuration, environment, e = postgresMaintenancePaths([]string{"--config=/private/one"})
	if e != nil || configuration != "/private/one" || environment != "" {
		t.Fatal(configuration, environment, e)
	}
}
