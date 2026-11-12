package databaseaccess

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAliasDescriptionUpgradeResumesExistingRuntimePrincipal(t *testing.T) {
	_, runtime := resumeTableGrantUpgrade(t, 58, "model_alias_description_intents")
	ctx := t.Context()
	// The normal scoped constraints remain active for the serving principal.
	// No matching alias exists, so INSERT must pass its ACL and fail its FK.
	_, err := runtime.Exec(ctx, `INSERT INTO model_alias_description_intents
		(tenant_id,workspace_id,actor_user_id,idempotency_key,alias_id,result_revision,fingerprint)
		SELECT tenant_id,workspace_id,user_id,$1,$2,1,$3 FROM workspace_memberships LIMIT 1`,
		uuid.NewString(), uuid.NewString(), make([]byte, 32))
	var foreignKey *pgconn.PgError
	if !errors.As(err, &foreignKey) || foreignKey.Code != "23503" {
		t.Fatal("runtime insert did not reach the scoped foreign key", err)
	}
}
