package deployments

import (
	"context"
	"log/slog"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
)

func (s *Service) admitModelUse(ctx context.Context, workspace, kind, digest string) error {
	err := models.CheckOnSiteUse(kind, digest, s.ModelUse)
	if err != nil {
		slog.WarnContext(ctx, "model_license_admission_denied",
			"workspace_id", workspace, "catalog_digest", digest, "reason_code", err.Error())
	}
	return err
}
