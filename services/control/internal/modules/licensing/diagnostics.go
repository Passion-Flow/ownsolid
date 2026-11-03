package licensing

import (
	"context"
	"encoding/json"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/audit"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	wire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

// MaximumDiagnosticsBytes is the fixed bounded JSON download contract.
const MaximumDiagnosticsBytes = httpserver.MaxJSONBytes

// VerifyStored checks integrity without making expiration a liveness failure.
func (s *Service) VerifyStored(ctx context.Context) error {
	_, e := s.observe(ctx, nil, false)
	return e
}

func (s *Service) Diagnostics(ctx context.Context, a identity.Auth, request string) (httpserver.MemoryDownload, error) {
	var out httpserver.MemoryDownload
	e := s.within(ctx, func(ctx context.Context, tx transaction.Executor) error {
		if e := s.actor(ctx, tx, a, true); e != nil {
			return e
		}
		state, _, _, e := s.readLocked(ctx, tx)
		if e != nil {
			return e
		}
		raw, e := json.Marshal(struct {
			FormatVersion int   `json:"format_version"`
			State         State `json:"state"`
		}{wire.FormatVersion, state})
		if e != nil || len(raw) > MaximumDiagnosticsBytes {
			return fail("LICENSE_STORED_STATE_INVALID")
		}
		if e := audit.WriteWithin(ctx, tx, a.Me.User.ID, "", "admin", "license.diagnostics_exported", "success", "platform_license", "", "", request); e != nil {
			return e
		}
		out = httpserver.MemoryDownload{Bytes: raw, ContentType: "application/json", FileName: "license-diagnostics.json", SHA256: wire.Digest(raw)}
		return nil
	})
	if e != nil {
		return httpserver.MemoryDownload{}, e
	}
	return out, nil
}
