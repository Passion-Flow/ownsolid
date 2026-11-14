package distribution

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"

	wire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

// AliasCertificateAuthority returns the loaded model gateway CA for a published alias
// whose gateway was activated by its node's agent.
func (s *Service) AliasCertificateAuthority(ctx context.Context, a identity.Auth, workspace, id string) ([]byte, error) {
	if !wire.ValidID(id) {
		return nil, fail("VALIDATION_FAILED")
	}
	err := s.transaction(ctx, a, workspace, false, nil, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if tx.Dialect() != transaction.PostgreSQL {
			return fail("RESOURCE_NOT_FOUND")
		}
		var activated bool
		err := tx.QueryRow(ctx, transaction.SQL(transaction.PostgreSQL, `SELECT EXISTS (
			SELECT 1 FROM model_aliases a
			JOIN gateway_instances g ON g.tenant_id=a.tenant_id AND g.workspace_id=a.workspace_id AND g.id=a.gateway_id
			WHERE a.tenant_id=$1 AND a.workspace_id=$2 AND a.id=$3 AND a.disabled_at IS NULL
				AND g.revoked_at IS NULL AND g.authority_invalidated_at IS NULL
				AND g.delivery='agent' AND g.client_origin IS NOT NULL AND g.certificate_not_after IS NOT NULL
		)`, w.TenantID, w.ID, id)).Scan(&activated)
		if err != nil {
			return err
		}
		if !activated {
			return fail("RESOURCE_NOT_FOUND")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// The agent task owner holds the authority loaded at control startup.
	// The browser distribution owner uses only its public certificates.
	if s.Nodes == nil {
		return nil, fail("GATEWAY_AUTHORITY_UNAVAILABLE")
	}
	issuer, ok := s.Nodes.Gateways.(*Service)
	if !ok || issuer == nil || issuer.Certificates == nil {
		return nil, fail("GATEWAY_AUTHORITY_UNAVAILABLE")
	}
	return publicGatewayCA(issuer.Certificates.Bundle())
}

func publicGatewayCA(bundle string) ([]byte, error) {
	var certificates []byte
	for remaining := bytes.TrimSpace([]byte(bundle)); len(remaining) > 0; {
		block, rest := pem.Decode(remaining)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return nil, fail("GATEWAY_AUTHORITY_UNAVAILABLE")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.BasicConstraintsValid || !certificate.IsCA {
			return nil, fail("GATEWAY_AUTHORITY_UNAVAILABLE")
		}
		certificates = append(certificates, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})...)
		remaining = bytes.TrimSpace(rest)
	}
	if len(certificates) == 0 {
		return nil, fail("GATEWAY_AUTHORITY_UNAVAILABLE")
	}
	return certificates, nil
}
