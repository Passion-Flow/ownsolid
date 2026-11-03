package modelpreparations

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/pagination"
	datawire "github.com/Passion-Flow/ownsolid/api/datasets/v1"
	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
)

const nativePreparationFields = `"ID","TENANT_ID","WORKSPACE_ID","NODE_ID","CURRENT_NODE_ID","MODEL_ID","TRAINING_JOB_ID","TRAINING_ATTEMPT_ID","TRAINING_ARTIFACT_ID","HELDOUT_ID","GPU_UUID","INPUT_SNAPSHOT","INPUT_SHA256","CONFIGURATION","CONFIGURATION_SHA256","EXECUTION_RECIPE","CREATED_BY","REASON","STATE","REVISION","WAIT_CODE","ERROR_CODE","CANCEL_REQUESTED_AT","CREATED_AT","UPDATED_AT","FINISHED_AT","ATTEMPT_NUMBER","CURRENT_ATTEMPT_ID","PENDING_RECOVERY_ID","REVIEW_ID","DERIVED_MODEL_ID"`
const nativeHoldoutFields = `"ID","WORKSPACE_ID","DATASET_ID","DATASET_VERSION_ID","NAME","MANIFEST_SHA256","EFFECTIVE_SOURCE_SHA256","ROW_MAPPING_SHA256","SOURCE_BYTES","ROW_COUNT","LABEL_COUNTS","RECIPE_ID","RECIPE_SHA256","INSTRUCTION_SHA256","FINGERPRINT_ALGORITHM","CREATED_BY","AUDIENCE","REASON","CREATED_AT"`
const nativeReviewFields = `"ID","WORKSPACE_ID","PREPARATION_ID","ATTEMPT_ID","ARTIFACT_ID","REPORT_SHA256","EXPECTED_REVISION","DECISION","MODEL_ID","MODEL_NAME","REVIEWED_BY","REASON","CREATED_AT"`
const nativeAttemptFields = `"ID","PREPARATION_ID","NODE_ID","TASK_ID","ATTEMPT_NUMBER","COMMAND_SHA256","STATE","LAST_EVENT_SEQUENCE","OUTCOME_SHA256","PHYSICAL_STOP_SHA256","ERROR_CODE","CREATED_AT","STARTED_AT","FINISHED_AT","PHYSICAL_STOPPED_AT"`

func preparationSelectedCursor(q transaction.Executor, cursor pagination.Cursor) error {
	if q.Dialect() == transaction.Dameng && cursor.ID != "" && (!datawire.ValidID(cursor.ID) || !preparationNativeCursor(cursor.Time)) {
		return fail("INVALID_CURSOR")
	}
	return nil
}

// Native pages use explicit lexicographic predicates and an admitted integer
// LIMIT. Empty state and absent cursors omit the predicate instead of relying
// on a vendor's empty-string or untyped-NULL comparison behavior.
func preparationNativePage(state string, cursor bool) preparationNativeQuery {
	text := "SELECT " + nativePreparationFields + ` FROM "MODEL_PREPARATIONS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?`
	order := []int{0, 1}
	if state != "" {
		if !validPreparationState(state) {
			return preparationNativeQuery{}
		}
		text += ` AND "STATE"=?`
		order = append(order, 3)
	}
	if cursor {
		text += ` AND ("CREATED_AT"<? OR ("CREATED_AT"=? AND "ID"<?))`
		order = append(order, 5, 5, 6)
	}
	text += ` ORDER BY "CREATED_AT" DESC,"ID" DESC`
	return preparationDMPage(text, 101).order(append(order, 7)...)
}

func preparationNativeHoldoutPage(cursor bool) preparationNativeQuery {
	text := "SELECT " + nativeHoldoutFields + ` FROM "MODEL_HOLDOUTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=?`
	order := []int{0, 1}
	if cursor {
		text += ` AND ("CREATED_AT"<? OR ("CREATED_AT"=? AND "ID"<?))`
		order = append(order, 3, 3, 4)
	}
	text += ` ORDER BY "CREATED_AT" DESC,"ID" DESC`
	return preparationDMPage(text, 101).order(append(order, 5)...)
}

func preparationNativeAttemptPage(cursor bool) preparationNativeQuery {
	text := "SELECT " + nativeAttemptFields + ` FROM "MODEL_PREPARATION_ATTEMPTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PREPARATION_ID"=?`
	order := []int{0, 1, 2}
	limitIndex := 3
	if cursor {
		text += ` AND ("CREATED_AT"<? OR ("CREATED_AT"=? AND "ID"<?))`
		order = append(order, 3, 3, 4)
		limitIndex = 5
	}
	text += ` ORDER BY "CREATED_AT" DESC,"ID" DESC`
	return preparationDMPage(text, 101).order(append(order, limitIndex)...)
}

func preparationNativeOverlap(table, column string, count int) preparationNativeQuery {
	var prefix string
	switch {
	case table == "model_holdout_sources" && column == "source_sha256":
		prefix = `SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_HOLDOUT_SOURCES" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "SOURCE_SHA256" IN (`
	case table == "model_holdout_prompts" && column == "prompt_sha256":
		prefix = `SELECT CASE WHEN EXISTS(SELECT 1 FROM "MODEL_HOLDOUT_PROMPTS" WHERE "TENANT_ID"=? AND "WORKSPACE_ID"=? AND "PROMPT_SHA256" IN (`
	default:
		return preparationNativeQuery{}
	}
	if count < 1 || count > admissionFingerprintBatchMaximum {
		return preparationNativeQuery{}
	}
	return preparationDM(prefix + strings.Repeat("?,", count-1) + `?)) THEN 1 ELSE 0 END FROM DUAL`)
}

// Wrap only this owner's rows, retaining the original executor identity when
// it is borrowed by Identity, Workspace, node, training or model repositories.
// Native integer booleans and UTC microsecond timestamps are checked before
// being copied into public state; a failed scan exposes no partially decoded row.
type preparationNativeRow struct {
	transaction.Row
	ids []int
}

func preparationIDRow(row transaction.Row, ids ...int) transaction.Row {
	if native, ok := row.(preparationNativeRow); ok {
		native.ids = ids
		return native
	}
	if native, ok := row.(preparationNativeRows); ok {
		return preparationNativeRow{Row: native.Rows, ids: ids}
	}
	return row
}

func preparationBorrowedExecutor(store transaction.Store, q transaction.Executor) error {
	if store == nil || q == nil || store.Dialect() != q.Dialect() {
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

func (r preparationNativeRow) Scan(dest ...any) error {
	bound := make([]any, len(dest))
	booleans := map[int]*int64{}
	stamps := map[int]*time.Time{}
	nullableStamps := map[int]**time.Time{}
	for i, d := range dest {
		pointer := reflect.ValueOf(d)
		if !pointer.IsValid() || pointer.Kind() != reflect.Pointer || pointer.IsNil() {
			return fail("MODEL_PREPARATION_DATA_INVALID")
		}
		bound[i] = reflect.New(pointer.Elem().Type()).Interface()
		switch d.(type) {
		case *bool:
			value := new(int64)
			bound[i], booleans[i] = value, value
		case *time.Time:
			value := new(time.Time)
			bound[i], stamps[i] = value, value
		case **time.Time:
			value := new(*time.Time)
			bound[i], nullableStamps[i] = value, value
		}
	}
	if e := r.Row.Scan(bound...); e != nil {
		return e
	}
	for _, i := range r.ids {
		if i < 0 || i >= len(bound) {
			return fail("MODEL_PREPARATION_DATA_INVALID")
		}
		switch value := bound[i].(type) {
		case *string:
			if !datawire.ValidID(*value) {
				return fail("MODEL_PREPARATION_DATA_INVALID")
			}
		case **string:
			if *value != nil && !datawire.ValidID(**value) {
				return fail("MODEL_PREPARATION_DATA_INVALID")
			}
		default:
			return fail("MODEL_PREPARATION_DATA_INVALID")
		}
	}
	for _, value := range booleans {
		if *value != 0 && *value != 1 {
			return fail("MODEL_PREPARATION_DATA_INVALID")
		}
	}
	for _, value := range stamps {
		if !preparationNativeCursor(*value) {
			return fail("MODEL_PREPARATION_DATA_INVALID")
		}
	}
	for _, value := range nullableStamps {
		if *value != nil && !preparationNativeCursor(**value) {
			return fail("MODEL_PREPARATION_DATA_INVALID")
		}
	}
	for i := range dest {
		if value, ok := booleans[i]; ok {
			*dest[i].(*bool) = *value == 1
		} else {
			reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(bound[i]).Elem())
		}
	}
	return nil
}

type preparationNativeRows struct{ transaction.Rows }

func (r preparationNativeRows) Scan(dest ...any) error {
	return (preparationNativeRow{Row: r.Rows}).Scan(dest...)
}

func preparationQueryRow(ctx context.Context, q transaction.Executor, statement transaction.Statement) transaction.Row {
	row := q.QueryRow(ctx, statement)
	if q.Dialect() == transaction.Dameng {
		return preparationNativeRow{Row: row}
	}
	return row
}

func preparationQuery(ctx context.Context, q transaction.Executor, statement transaction.Statement) (transaction.Rows, error) {
	rows, e := q.Query(ctx, statement)
	if e == nil && q.Dialect() == transaction.Dameng {
		return preparationNativeRows{rows}, nil
	}
	return rows, e
}

func (s *Service) verifyNativePreparationOutputs(ctx context.Context, q transaction.Executor, tenant string) error {
	return walkNativePreparationOutputs(ctx, q, tenant, true, func(d UploadDescriptor, report []byte) error {
		var result wire.PreparedResult
		if int64(len(report)) > wire.MaximumResultBytes || hash(report) != d.ManifestSHA256 || wire.DecodeStrict(report, &result) != nil || result.Status != "prepared" || result.TenantID != d.Scope.TenantID || result.WorkspaceID != d.Scope.WorkspaceID || result.PreparationID != d.Scope.PreparationID || result.AttemptID != d.AttemptID {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
		expected, _ := json.Marshal(d.Files)
		actual, _ := json.Marshal(result.ModelFiles)
		if string(actual) != string(expected) {
			return fail("MODEL_PREPARATION_STORAGE_DATA_INVALID")
		}
		return s.Store.VerifyPublished(ctx, d)
	})
}
