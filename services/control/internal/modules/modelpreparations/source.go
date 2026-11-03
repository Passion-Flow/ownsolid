package modelpreparations

import (
	"bufio"
	"context"
	"errors"
	"io"
	"reflect"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

type trainingReference struct{ DatasetID, VersionID, ManifestSHA256 string }

func (s *Service) trainingReferences(ctx context.Context, q rowQuery, tenant, workspace string) ([]trainingReference, error) {
	rows, e := q.Query(ctx, `SELECT DISTINCT dataset_id::text,dataset_version_id::text,dataset_manifest_sha256 FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 ORDER BY 1,2,3 LIMIT $3`, tenant, workspace, s.policy().ScanMaximumVersions+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	return s.scanTrainingReferences(rows)
}

type rowQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (s *Service) trainingReferencesWithin(ctx context.Context, q transaction.Executor, tenant, workspace string) ([]trainingReference, error) {
	if q.Dialect() == transaction.Dameng {
		return s.nativeTrainingReferences(ctx, q, tenant, workspace)
	}
	rows, e := preparationQuery(ctx, q, preparationSQL(q, `SELECT DISTINCT dataset_id::text,dataset_version_id::text,dataset_manifest_sha256 FROM training_jobs WHERE tenant_id=$1 AND workspace_id=$2 ORDER BY 1,2,3 LIMIT $3`, `SELECT DISTINCT dataset_id,dataset_version_id,dataset_manifest_sha256 FROM training_jobs WHERE tenant_id=? AND workspace_id=? ORDER BY 1,2,3 LIMIT ?`, tenant, workspace, s.policy().ScanMaximumVersions+1))
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	return s.scanTrainingReferences(rows)
}
func (s *Service) scanTrainingReferences(rows transaction.Rows) ([]trainingReference, error) {
	values := []trainingReference{}
	for rows.Next() {
		var v trainingReference
		if e := rows.Scan(&v.DatasetID, &v.VersionID, &v.ManifestSHA256); e != nil {
			return nil, e
		}
		if !datawire.ValidID(v.DatasetID) || !datawire.ValidID(v.VersionID) || !datawire.ValidDigest(v.ManifestSHA256) {
			return nil, fail("MODEL_HOLDOUT_DATA_INVALID")
		}
		values = append(values, v)
	}
	if e := rows.Err(); e != nil {
		return nil, e
	}
	if len(values) > s.policy().ScanMaximumVersions {
		return nil, fail("MODEL_HOLDOUT_ANALYSIS_LIMIT")
	}
	return values, nil
}

type analysisBudget struct{ bytes, rows int64 }
type sourceSnapshot struct {
	version  datasets.Version
	size     int64
	rowCount int64
	labels   LabelCounts
	groups   map[string]bool
	prompts  map[string]bool
}

func parserCeilings() datawire.Limits {
	return datawire.Limits{MaximumBytes: datawire.MaximumBytesCeiling, MaximumRows: datawire.MaximumRowsCeiling, MaximumLineBytes: datawire.MaximumLineBytesCeiling, MaximumTextBytes: datawire.MaximumTextBytesCeiling, MaximumSourceBytes: datawire.MaximumSourceBytesCeiling, MaximumErrors: datawire.MaximumErrorsCeiling}
}
func (s *Service) scanSource(ctx context.Context, a identity.Auth, workspace, dataset, version string, retained, holdout bool, budget *analysisBudget) (sourceSnapshot, error) {
	var result sourceSnapshot
	value, e := s.Datasets.PublishedSourceSnapshot(ctx, a, workspace, dataset, version, retained)
	if e != nil {
		return result, e
	}
	remaining := s.policy().ScanMaximumBytes - budget.bytes
	if remaining < 1 {
		return result, fail("MODEL_HOLDOUT_ANALYSIS_LIMIT")
	}
	size, e := s.Datasets.PublishedSourceSize(value)
	if e != nil {
		return result, e
	}
	if size > remaining {
		return result, fail("MODEL_HOLDOUT_ANALYSIS_LIMIT")
	}
	file, actualSize, e := s.Datasets.OpenPublishedSource(ctx, value, min(remaining, datawire.MaximumBytesCeiling))
	if e != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, e
	}
	defer file.Close()
	if size != actualSize {
		return result, fail("MODEL_HOLDOUT_SOURCE_CHANGED")
	}
	budget.bytes += size
	result = sourceSnapshot{version: value, size: size, groups: map[string]bool{}, prompts: map[string]bool{}}
	scanner := bufio.NewScanner(io.LimitReader(file, size+1))
	scanner.Buffer(make([]byte, 1024), int(datawire.MaximumLineBytesCeiling)+1)
	rawPrompts := map[string]bool{}
	for scanner.Scan() {
		if e := ctx.Err(); e != nil {
			return sourceSnapshot{}, e
		}
		budget.rows++
		result.rowCount++
		if budget.rows > s.policy().ScanMaximumRows || result.rowCount > datawire.MaximumRowsCeiling || holdout && result.rowCount > wire.MaximumRows {
			return sourceSnapshot{}, fail("MODEL_HOLDOUT_ANALYSIS_LIMIT")
		}
		row, e := datawire.DecodeRecord(scanner.Bytes(), parserCeilings())
		if e != nil {
			return sourceSnapshot{}, fail("MODEL_HOLDOUT_SOURCE_INVALID")
		}
		raw, e := wire.PromptFingerprint(row.Prompt)
		if e != nil {
			return sourceSnapshot{}, e
		}
		wrapped, e := wire.PromptFingerprint(wire.InstructionPrompt(row.Prompt))
		if e != nil {
			return sourceSnapshot{}, e
		}
		if holdout {
			if !validLabel(row.Completion) || rawPrompts[raw] {
				return sourceSnapshot{}, fail("MODEL_HOLDOUT_LABELS_INVALID")
			}
			rawPrompts[raw] = true
			result.labels.increment(row.Completion)
		}
		result.groups[hash([]byte(row.SourceID))] = true
		result.prompts[raw] = true
		result.prompts[wrapped] = true
	}
	if scanner.Err() != nil {
		return sourceSnapshot{}, fail("MODEL_HOLDOUT_SOURCE_INVALID")
	}
	if result.rowCount < 1 || holdout && (result.rowCount < wire.MinimumRows || !result.labels.valid(result.rowCount)) {
		return sourceSnapshot{}, fail("MODEL_HOLDOUT_LABELS_INVALID")
	}
	return result, nil
}
func overlaps(a, b map[string]bool) bool {
	for value := range a {
		if b[value] {
			return true
		}
	}
	return false
}
func sameSource(snapshot sourceSnapshot, current datasets.Version) bool {
	return snapshot.version.ID == current.ID && snapshot.version.WorkspaceID == current.WorkspaceID && snapshot.version.DatasetID == current.DatasetID && snapshot.version.AttemptID == current.AttemptID && snapshot.version.ManifestSHA256 == current.ManifestSHA256 && snapshot.version.EffectiveSourceSHA256 == current.EffectiveSourceSHA256 && snapshot.version.RowMappingSHA256 == current.RowMappingSHA256
}
func sameReferences(a, b []trainingReference) bool { return reflect.DeepEqual(a, b) }
func sortedHashes(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
func sourceError(e error) error {
	if errors.Is(e, context.DeadlineExceeded) {
		return fail("MODEL_HOLDOUT_ANALYSIS_TIMEOUT")
	}
	if e != nil {
		switch e.Error() {
		case "DATASET_FILE_INVALID", "DATASET_DATA_INVALID", "DATASET_FILE_CHANGED":
			return fail("MODEL_HOLDOUT_SOURCE_CHANGED")
		case "DATASET_STORAGE_UNAVAILABLE":
			return fail("MODEL_HOLDOUT_UNAVAILABLE")
		}
	}
	return e
}
