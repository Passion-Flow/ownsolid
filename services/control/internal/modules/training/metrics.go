package training

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/training/v1"
)

const maximumCollectionPage = 100
const maximumMetricCursorBytes = 2048

type MetricRecord struct {
	AttemptID  string            `json:"attempt_id"`
	Sequence   int64             `json:"sequence"`
	GlobalStep int64             `json:"global_step"`
	Values     wire.MetricValues `json:"values"`
	RecordedAt time.Time         `json:"recorded_at"`
}
type metricCursor struct {
	SchemaVersion int    `json:"schema_version"`
	Scope         string `json:"scope"`
	Sequence      int64  `json:"sequence"`
}

func (s *Service) Metrics(ctx context.Context, a identity.Auth, workspace, job, attempt string, query url.Values) (Page[MetricRecord], error) {
	out := Page[MetricRecord]{Items: []MetricRecord{}}
	if _, e := s.trainingRepository(); e != nil {
		return out, trainingRepositoryError(e)
	}
	if !datawire.ValidID(attempt) {
		return out, fail("VALIDATION_FAILED")
	}
	limit := s.Identity.Config.Policy().UI.PageSize
	cursorValue := ""
	for key, values := range query {
		if len(values) != 1 {
			return out, fail("VALIDATION_FAILED")
		}
		switch key {
		case "limit":
			n, e := strconv.Atoi(values[0])
			if e != nil || n < 1 || n > maximumCollectionPage {
				return out, fail("VALIDATION_FAILED")
			}
			limit = n
		case "cursor":
			cursorValue = values[0]
		default:
			return out, fail("VALIDATION_FAILED")
		}
	}
	sum := sha256.Sum256([]byte("training:metrics:" + a.Me.Audience + ":" + a.Me.User.ID + ":" + workspace + ":" + job + ":" + attempt))
	scope := hex.EncodeToString(sum[:])
	after := int64(0)
	if cursorValue != "" {
		if len(cursorValue) > maximumMetricCursorBytes {
			return out, fail("INVALID_CURSOR")
		}
		raw, e := base64.RawURLEncoding.DecodeString(cursorValue)
		var cursor metricCursor
		if e != nil || wire.DecodeStrict(raw, &cursor) != nil || cursor.SchemaVersion != 1 || cursor.Scope != scope || cursor.Sequence < 1 || cursor.Sequence > wire.MaximumEventSequence {
			return out, fail("INVALID_CURSOR")
		}
		after = cursor.Sequence
	}
	e := s.trainingActorTransaction(ctx, a, workspace, false, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace) error {
		if _, e := lockedJobWithin(ctx, tx, w, job); e != nil {
			return e
		}
		var found bool
		if e := tx.QueryRow(ctx, acceptedAdapterSQL(tx, "SELECT EXISTS(SELECT 1 FROM training_attempts WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND id=$4)", "SELECT EXISTS(SELECT 1 FROM training_attempts WHERE tenant_id=? AND workspace_id=? AND job_id=? AND id=?)", w.TenantID, w.ID, job, attempt)).Scan(&found); e != nil {
			return e
		}
		if !found {
			return fail("TRAINING_NOT_FOUND")
		}
		rows, e := tx.Query(ctx, acceptedAdapterSQL(tx, "SELECT sequence,global_step,payload->'values',recorded_at FROM training_events WHERE tenant_id=$1 AND workspace_id=$2 AND job_id=$3 AND attempt_id=$4 AND kind='metric' AND sequence>$5 ORDER BY sequence LIMIT $6", "SELECT sequence,global_step,JSON_EXTRACT(payload,'$.values'),recorded_at FROM training_events WHERE tenant_id=? AND workspace_id=? AND job_id=? AND attempt_id=? AND kind='metric' AND sequence>? ORDER BY sequence LIMIT ?", w.TenantID, w.ID, job, attempt, after, limit+1))
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var item MetricRecord
			var raw []byte
			item.AttemptID = attempt
			if e = rows.Scan(&item.Sequence, &item.GlobalStep, &raw, &item.RecordedAt); e != nil {
				return e
			}
			if wire.DecodeStrict(raw, &item.Values) != nil || item.Values.Validate() != nil {
				return fail("TRAINING_DATA_INVALID")
			}
			item.RecordedAt = item.RecordedAt.UTC()
			out.Items = append(out.Items, item)
		}
		if e = rows.Err(); e != nil {
			return e
		}
		if len(out.Items) > limit {
			out.Items = out.Items[:limit]
			raw, _ := json.Marshal(metricCursor{SchemaVersion: 1, Scope: scope, Sequence: out.Items[len(out.Items)-1].Sequence})
			encoded := base64.RawURLEncoding.EncodeToString(raw)
			out.NextCursor = &encoded
		}
		return nil
	})
	if e != nil {
		return Page[MetricRecord]{Items: []MetricRecord{}}, e
	}
	return out, nil
}
