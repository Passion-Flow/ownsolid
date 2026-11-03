package modelpreparations

import (
	"context"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"strconv"
	"strings"
)

// One lookup batch is bounded by the immutable maximum rows of a reservation.
// Only fixed repository SQL and bound digest bytes enter the statement.
const admissionFingerprintBatchMaximum = int(wire.MaximumRows)

func (s *Service) admissionExecutor(ctx context.Context, q transaction.Executor, w workspaces.Workspace) error {
	if ctx == nil || q == nil {
		return fail("STORAGE_UNAVAILABLE")
	}
	if e := ctx.Err(); e != nil {
		return preparationRepositoryError(e)
	}
	if !datawire.ValidID(w.TenantID) || !datawire.ValidID(w.ID) {
		return fail("ACCESS_DENIED")
	}
	store, e := s.reviewRepository()
	if e != nil {
		return e
	}
	if store.Dialect() != q.Dialect() {
		return fail("MODEL_PREPARATION_STORAGE_MISMATCH")
	}
	if q.Dialect() == transaction.Dameng {
		owner, ok := store.(interface {
			OwnsDamengExecutor(transaction.Executor) bool
		})
		if !ok || !owner.OwnsDamengExecutor(q) {
			return fail("MODEL_PREPARATION_STORAGE_MISMATCH")
		}
	}
	return nil
}

// HasReservationsWithin borrows the current training owner's authority fences.
func (s *Service) HasReservationsWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace) (bool, error) {
	if e := s.admissionExecutor(ctx, q, w); e != nil {
		return false, e
	}
	var present, unsupported bool
	e := preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationDM(`SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_HOLDOUTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?) THEN 1 ELSE 0 END,CASE WHEN EXISTS(SELECT 1 FROM "MODEL_HOLDOUTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "FINGERPRINT_ALGORITHM"<>?) THEN 1 ELSE 0 END FROM DUAL`), `SELECT EXISTS(SELECT 1 FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2),EXISTS(SELECT 1 FROM model_holdouts WHERE tenant_id=$1 AND workspace_id=$2 AND fingerprint_algorithm<>$3)`, `SELECT EXISTS(SELECT 1 FROM model_holdouts WHERE tenant_id=? AND workspace_id=?),EXISTS(SELECT 1 FROM model_holdouts WHERE tenant_id=? AND workspace_id=? AND fingerprint_algorithm<>?)`, presenceArguments(q, w)...)).Scan(&present, &unsupported)
	if e != nil {
		return false, preparationRepositoryError(e)
	}
	if unsupported {
		return false, fail("MODEL_HOLDOUT_ALGORITHM_UNSUPPORTED")
	}
	return present, nil
}
func presenceArguments(q transaction.Executor, w workspaces.Workspace) []any {
	if q.Dialect() == transaction.MySQL || q.Dialect() == transaction.Dameng {
		return []any{w.TenantID, w.ID, w.TenantID, w.ID, wire.PromptFingerprintAlgorithm}
	}
	return []any{w.TenantID, w.ID, wire.PromptFingerprintAlgorithm}
}

// ValidateWithin neither commits nor opens files. The immutable private check
// is scoped to its original authorized analysis; the caller owns current fences.
func (check *trainingAdmission) ValidateWithin(ctx context.Context, q transaction.Executor, w workspaces.Workspace, input datasets.TrainingInputs) error {
	if check == nil || check.owner == nil {
		return fail("MODEL_HOLDOUT_DATA_INVALID")
	}
	if e := check.owner.admissionExecutor(ctx, q, w); e != nil {
		return e
	}
	if check.tenant != w.TenantID || check.workspace != w.ID || !sameSource(check.snapshot, input.Version) || input.Version.WorkspaceID != w.ID {
		return fail("MODEL_HOLDOUT_SOURCE_CHANGED")
	}
	groups, e := rawHashes(check.snapshot.groups)
	if e != nil {
		return e
	}
	prompts, e := rawHashes(check.snapshot.prompts)
	if e != nil {
		return e
	}
	if len(groups) == 0 || len(prompts) == 0 {
		return fail("MODEL_HOLDOUT_DATA_INVALID")
	}
	if _, e = check.owner.HasReservationsWithin(ctx, q, w); e != nil {
		return e
	}
	for _, set := range []struct {
		table, column string
		hashes        [][]byte
	}{{"model_holdout_sources", "source_sha256", groups}, {"model_holdout_prompts", "prompt_sha256", prompts}} {
		for offset := 0; offset < len(set.hashes); offset += admissionFingerprintBatchMaximum {
			end := min(offset+admissionFingerprintBatchMaximum, len(set.hashes))
			args := []any{w.TenantID, w.ID}
			pg, my := []string{}, []string{}
			for _, digest := range set.hashes[offset:end] {
				args = append(args, digest)
				pg = append(pg, "$"+strconv.Itoa(len(args)))
				my = append(my, "?")
			}
			prefix := "SELECT EXISTS(SELECT 1 FROM " + set.table + " WHERE tenant_id="
			var overlap bool
			e = preparationQueryRow(ctx, q, preparationSelectedSQL(q, preparationNativeOverlap(set.table, set.column, end-offset), prefix+"$1 AND workspace_id=$2 AND "+set.column+" IN ("+strings.Join(pg, ",")+"))", prefix+"? AND workspace_id=? AND "+set.column+" IN ("+strings.Join(my, ",")+"))", args...)).Scan(&overlap)
			if e != nil {
				return preparationRepositoryError(e)
			}
			if overlap {
				return fail("MODEL_HOLDOUT_OVERLAP")
			}
		}
	}
	return nil
}
