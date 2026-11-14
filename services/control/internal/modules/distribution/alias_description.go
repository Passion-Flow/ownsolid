package distribution

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

const maximumAliasDescriptionRunes = 500

// AliasDescriptionEdit changes catalogue text using its independent revision.
type AliasDescriptionEdit struct {
	Description                 string `json:"description"`
	ExpectedDescriptionRevision int64  `json:"expected_description_revision"`
	Reason                      string `json:"reason"`
}

// AliasDescriptionResult separates the current state from the revision applied by the intent.
type AliasDescriptionResult struct {
	Description                string `json:"description"`
	DescriptionRevision        int64  `json:"description_revision"`
	AppliedDescriptionRevision int64  `json:"applied_description_revision"`
	Replayed                   bool   `json:"replayed"`
}

func normalizeAliasDescription(text string) (string, bool) {
	if !utf8.ValidString(text) {
		return "", false
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", false
		}
	}
	text = strings.TrimSpace(text)
	return text, utf8.RuneCountInString(text) <= maximumAliasDescriptionRunes
}

func requireAliasDescription(ctx context.Context, tx transaction.Executor) error {
	if tx.Dialect() != transaction.PostgreSQL {
		return fail("SERVICE_NOT_READY")
	}
	var supported bool
	if err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
		"SELECT to_regclass(format('%I.model_alias_description_intents',current_schema())) IS NOT NULL")).Scan(&supported); err != nil {
		return err
	}
	if !supported {
		return fail("SERVICE_NOT_READY")
	}
	return nil
}

// publicAliasAvailability keeps optional description columns out of machine and answer authority reads.
func (s *Service) publicAliasAvailability(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, alias *Alias) error {
	if tx.Dialect() != transaction.PostgreSQL {
		return s.aliasAvailability(ctx, tx, w, alias)
	}
	if err := s.aliasReadiness(ctx, tx, w, alias); err != nil {
		return err
	}
	// The predecessor row has neither key. The scoped projection stays valid before migration.
	err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, "SELECT ("+heldAliasRequests+`), d.description,d.description_revision
		FROM model_aliases a CROSS JOIN LATERAL jsonb_to_record(to_jsonb(a)) AS d(description text,description_revision bigint)
		WHERE a.tenant_id=$1 AND a.workspace_id=$2 AND a.id=$3`, w.TenantID, w.ID, alias.ID)).
		Scan(&alias.HeldRequests, &alias.Description, &alias.DescriptionRevision)
	if err != nil {
		return err
	}
	if (alias.Description == nil) != (alias.DescriptionRevision == nil) {
		return fail("STORAGE_UNAVAILABLE")
	}
	return nil
}

// EditAliasDescription records an authorized catalogue edit and its intent atomically.
func (s *Service) EditAliasDescription(ctx context.Context, a identity.Auth, workspace, id string, in AliasDescriptionEdit, key, request string) (AliasDescriptionResult, error) {
	var out AliasDescriptionResult
	if !admin(a) {
		return out, fail("ACCESS_DENIED")
	}
	var valid bool
	in.Description, valid = normalizeAliasDescription(in.Description)
	if !valid || !wire.ValidID(id) || !wire.ValidID(key) || !reason(in.Reason) ||
		in.ExpectedDescriptionRevision < 1 || in.ExpectedDescriptionRevision >= maximumRevision {
		return out, fail("VALIDATION_FAILED")
	}
	err := s.transaction(ctx, a, workspace, true, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if err := requireAliasDescription(ctx, tx); err != nil {
			return err
		}
		expected := fingerprint(a.Me.User.ID, struct {
			Alias string
			Input AliasDescriptionEdit
		}{id, in})
		var storedAlias string
		var saved []byte
		err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
			`SELECT alias_id::text,result_revision,fingerprint FROM model_alias_description_intents
			WHERE tenant_id=$1 AND workspace_id=$2 AND actor_user_id=$3 AND idempotency_key=$4
			AND site_id=local_site_id() AND cell_id=local_cell_id()`,
			w.TenantID, w.ID, a.Me.User.ID, key)).Scan(&storedAlias, &out.AppliedDescriptionRevision, &saved)
		switch {
		case err == nil:
			if storedAlias != id || !same(expected, saved) {
				return fail("IDEMPOTENCY_CONFLICT")
			}
			out.Replayed = true
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		err = tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL,
			`SELECT description,description_revision FROM model_aliases
			WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3
			AND site_id=local_site_id() AND cell_id=local_cell_id() FOR UPDATE`, w.TenantID, w.ID, id)).
			Scan(&out.Description, &out.DescriptionRevision)
		if errors.Is(err, sql.ErrNoRows) {
			return fail("MODEL_ALIAS_NOT_FOUND")
		}
		if err != nil || out.Replayed {
			return err
		}
		if out.DescriptionRevision != in.ExpectedDescriptionRevision {
			return fail("VERSION_CONFLICT")
		}
		if _, err = distributionExecOne(ctx, tx, transaction.SQL(transaction.PostgreSQL,
			`UPDATE model_aliases SET description=$4,description_revision=description_revision+1
			WHERE tenant_id=$1 AND workspace_id=$2 AND id=$3`, w.TenantID, w.ID, id, in.Description)); err != nil {
			return err
		}
		out.Description = in.Description
		out.DescriptionRevision++
		out.AppliedDescriptionRevision = out.DescriptionRevision
		if _, err = distributionExecOne(ctx, tx, transaction.SQL(transaction.PostgreSQL,
			`INSERT INTO model_alias_description_intents(tenant_id,workspace_id,actor_user_id,idempotency_key,alias_id,result_revision,fingerprint)
			VALUES($1,$2,$3,$4,$5,$6,$7)`, w.TenantID, w.ID, a.Me.User.ID, key, id, out.AppliedDescriptionRevision, expected)); err != nil {
			return err
		}
		return s.audit(ctx, tx, a, w, "model.alias_description_updated", "model_alias", id, in.Reason, request)
	})
	if err != nil {
		return AliasDescriptionResult{}, err
	}
	return out, nil
}
