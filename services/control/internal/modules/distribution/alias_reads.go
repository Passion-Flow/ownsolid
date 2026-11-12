package distribution

import (
	"context"
	"net/url"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// AliasReadCapabilities describes verified generation evidence, not enabled API adapters.
// The live tokenizer can enforce a smaller context than ContextTokenCeiling.
type AliasReadCapabilities struct {
	TextGenerationVerified bool `json:"text_generation_verified"`
	ContextTokenCeiling    int  `json:"context_token_ceiling"`
}

// AliasReadView keeps optional read metadata out of machine and mutation responses.
type AliasReadView struct {
	Alias
	Capabilities *AliasReadCapabilities `json:"capabilities,omitempty"`
}

func scanAliasRead(row transaction.Row) (AliasReadView, error) {
	alias, err := scanAlias(row)
	return AliasReadView{Alias: alias}, err
}

func (s *Service) prepareAliasRead(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, view *AliasReadView) error {
	if err := s.publicAliasAvailability(ctx, tx, w, &view.Alias); err != nil {
		return err
	}
	if !s.PublishAliasCapabilities {
		return nil
	}
	metadata, err := s.aliasCapabilitiesWithin(ctx, tx, w, view.Alias)
	if err != nil {
		return err
	}
	if metadata != nil {
		view.Capabilities = &AliasReadCapabilities{
			TextGenerationVerified: metadata.VerifiedTextGeneration,
			ContextTokenCeiling:    metadata.ContextTokenCeiling,
		}
	}
	return nil
}

// ReadAliasPage returns human alias reads with their exact scoped evidence in one transaction.
// Capability fields remain omitted until PublishAliasCapabilities is enabled at composition.
func (s *Service) ReadAliasPage(ctx context.Context, a identity.Auth, workspace string, values url.Values) (Page[AliasReadView], error) {
	return listPage(ctx, s, a, workspace, values, "", "aliases", "SELECT "+aliasFields+" FROM model_aliases a", "SELECT "+mysqlAliasFields+" FROM model_aliases a", "a", "a.created_at", "a.id", scanAliasRead, func(v AliasReadView) (time.Time, string) { return v.CreatedAt, v.ID }, s.prepareAliasRead)
}

// ReadAlias returns the visible human alias and optional evidence from one authorized transaction.
func (s *Service) ReadAlias(ctx context.Context, a identity.Auth, workspace, id string) (AliasReadView, error) {
	if !wire.ValidID(id) {
		return AliasReadView{}, fail("VALIDATION_FAILED")
	}
	var out AliasReadView
	err := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		alias, err := visibleAliasWithin(ctx, tx, a, w, id)
		if err != nil {
			return err
		}
		out.Alias = alias
		return s.prepareAliasRead(ctx, tx, w, &out)
	})
	if err != nil {
		return AliasReadView{}, err
	}
	return out, nil
}
