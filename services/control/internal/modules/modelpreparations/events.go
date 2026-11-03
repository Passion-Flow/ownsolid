package modelpreparations

import (
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"

	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

func (s *Service) RecordEvents(ctx context.Context, node, credential, task, token string, raw [][]byte) ([]wire.EventAcceptance, error) {
	out := []wire.EventAcceptance{}
	if len(raw) < 1 || len(raw) > wire.MaximumEventBatch {
		return out, fail("MODEL_PREPARATION_EVENT_INVALID")
	}
	e := s.withMachineLease(ctx, node, credential, task, token, func(ctx context.Context, tx transaction.Executor, w workspaces.Workspace, p Preparation, command wire.Command) error {
		var sequence, bytes int64
		if e := preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "LAST_EVENT_SEQUENCE","EVENT_BYTES" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=? FOR UPDATE`), "SELECT last_event_sequence,event_bytes FROM model_preparation_attempts WHERE id=$1 FOR UPDATE", `SELECT last_event_sequence,event_bytes FROM model_preparation_attempts WHERE id=? FOR UPDATE`, command.AttemptID)).Scan(&sequence, &bytes); e != nil {
			return e
		}
		for _, item := range raw {
			event, e := wire.DecodeEvent(item, p.ID, command.AttemptID, p.inputs.Heldout.RowCount)
			if e != nil {
				return e
			}
			canonical, _ := json.Marshal(event)
			sha := hash(canonical)
			accepted := wire.EventAcceptance{PreparationID: p.ID, AttemptID: command.AttemptID, Sequence: event.Sequence, State: p.State}
			if event.Sequence <= sequence {
				var previous string
				if e = preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "CANONICAL_SHA256" FROM "MODEL_PREPARATION_EVENTS" WHERE "ATTEMPT_ID"=? AND "SEQUENCE"=?`), "SELECT canonical_sha256 FROM model_preparation_events WHERE attempt_id=$1 AND sequence=$2", `SELECT canonical_sha256 FROM model_preparation_events WHERE attempt_id=? AND sequence=?`, command.AttemptID, event.Sequence)).Scan(&previous); e != nil {
					return e
				}
				if sha != previous {
					return fail("MODEL_PREPARATION_EVENT_CONFLICT")
				}
				accepted.Duplicate = true
				out = append(out, accepted)
				continue
			}
			if event.Sequence != sequence+1 {
				return fail("MODEL_PREPARATION_EVENT_ORDER_CONFLICT")
			}
			if bytes+int64(len(canonical))+1 > command.Recipe.Resources.MaximumEventBytes {
				return fail("MODEL_PREPARATION_EVENT_LIMIT")
			}
			if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`INSERT INTO "MODEL_PREPARATION_EVENTS"("TENANT_ID","WORKSPACE_ID","PREPARATION_ID","ATTEMPT_ID","SEQUENCE","KIND","PAYLOAD","CANONICAL_SHA256") VALUES(?,?,?,?,?,?,CAST(? AS CLOB),?)`, 6), `INSERT INTO model_preparation_events(tenant_id,workspace_id,preparation_id,attempt_id,sequence,kind,payload,canonical_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, `INSERT INTO model_preparation_events(tenant_id,workspace_id,preparation_id,attempt_id,sequence,kind,payload,canonical_sha256) VALUES(?,?,?,?,?,?,CAST(? AS JSON),?)`, w.TenantID, w.ID, p.ID, command.AttemptID, event.Sequence, event.Kind, canonical, sha)); e != nil {
				return e
			}
			sequence = event.Sequence
			bytes += int64(len(canonical)) + 1
			if event.Sequence == 1 {
				if _, e = tx.Exec(ctx, preparationSelectedSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATIONS" SET "STATE"='running',"REVISION"="REVISION"+1,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), "UPDATE model_preparations SET state='running',revision=revision+1,updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparations SET state='running',revision=revision+1,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, p.ID)); e != nil {
					return e
				}
				p.State = "running"
				accepted.State = p.State
			}
			if _, e = tx.Exec(ctx, preparationSelectedRuntimeSQL(tx, preparationDM(`UPDATE "MODEL_PREPARATION_ATTEMPTS" SET "STATE"='running',"LAST_EVENT_SEQUENCE"=?,"EVENT_BYTES"=?,"UPDATED_AT"=SYS_EXTRACT_UTC(CURRENT_TIMESTAMP(6)) WHERE "ID"=?`), "UPDATE model_preparation_attempts SET state='running',last_event_sequence=$2,event_bytes=$3,updated_at=clock_timestamp() WHERE id=$1", `UPDATE model_preparation_attempts SET state='running',last_event_sequence=?,event_bytes=?,updated_at=UTC_TIMESTAMP(6) WHERE id=?`, []any{command.AttemptID, sequence, bytes}, []any{sequence, bytes, command.AttemptID})); e != nil {
				return e
			}
			var a identity.Auth
			if e = preparationIDRow(preparationQueryRow(ctx, tx, preparationSelectedSQL(tx, preparationDM(`SELECT "REQUESTED_BY","REQUESTED_AUDIENCE" FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "ID"=?`), "SELECT requested_by::text,requested_audience FROM model_preparation_attempts WHERE id=$1", `SELECT requested_by,requested_audience FROM model_preparation_attempts WHERE id=?`, command.AttemptID)), 0).Scan(&a.Me.User.ID, &a.Me.Audience); e != nil {
				return e
			}
			if e = preparationAuditWithin(ctx, tx, a, w, "model.preparation_event_recorded", p.ID, "Accepted exact ordered preparation progress", uuid.NewString()); e != nil {
				return e
			}
			out = append(out, accepted)
		}
		return nil
	})
	if e != nil {
		return []wire.EventAcceptance{}, e
	}
	return out, nil
}
