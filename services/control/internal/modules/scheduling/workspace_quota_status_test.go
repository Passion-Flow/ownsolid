package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestWorkspaceQuotaStatusPreservesQuantitiesAndRejectsInvalidUsage(t *testing.T) {
	const bytes = int64(9007199254740993)
	rows, err := workspaceQuotaResources(Quota{
		Guaranteed: Resources{CPU: 1000, Memory: bytes},
		Burst:      Resources{CPU: 4000, Memory: math.MaxInt64, NVIDIAGPU: 2},
	}, Resources{CPU: 1000, Memory: bytes, NVIDIAGPU: 2})
	want := []WorkspaceQuotaResource{
		{Resource: CPU, Guaranteed: "1000", HardLimit: "4000", Reserved: "1000", UnreservedQuota: "3000"},
		{Resource: Memory, Guaranteed: "9007199254740993", HardLimit: "9223372036854775807", Reserved: "9007199254740993", UnreservedQuota: "9214364837600034814"},
		{Resource: NVIDIAGPU, Guaranteed: "0", HardLimit: "2", Reserved: "2", UnreservedQuota: "0"},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("exact, sorted quota accounting: %+v, %v", rows, err)
	}
	body, err := json.Marshal(rows)
	if err != nil || !strings.Contains(string(body), `"reserved":"9007199254740993"`) {
		t.Fatalf("JSON lost integer precision: %s, %v", body, err)
	}
	for _, used := range []Resources{
		{CPU: -1}, {CPU: 2}, {Memory: 1}, {Resource("unknown"): 0},
	} {
		if rows, err := workspaceQuotaResources(Quota{Burst: Resources{CPU: 1}}, used); !errors.Is(err, ErrQuotaPolicyUnavailable) || rows != nil {
			t.Fatalf("invalid usage exposed quota: %+v, %v", rows, err)
		}
	}
}

func TestWorkspaceQuotaStatusFailurePreservesCommitUncertainty(t *testing.T) {
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	for _, test := range []struct {
		err  error
		code string
	}{
		{errors.Join(context.Canceled, unknown, errors.New("private detail")), string(transaction.CommitUnknown)},
		{&transaction.Error{Kind: transaction.CommitRolledBack}, string(transaction.CommitRolledBack)},
		{&transaction.Error{Kind: transaction.CheckConflict, SQLState: "23514"}, string(transaction.Unavailable)},
		{&transaction.Error{Kind: transaction.Cancelled}, string(transaction.Cancelled)},
		{&transaction.Error{Kind: transaction.Timeout}, string(transaction.Timeout)},
		{context.Canceled, string(transaction.Cancelled)},
		{context.DeadlineExceeded, string(transaction.Timeout)},
		{errors.New("RESOURCE_NOT_FOUND"), "RESOURCE_NOT_FOUND"},
		{errors.New("AUTH_SESSION_EXPIRED"), "AUTH_SESSION_EXPIRED"},
		{errors.New("private database message"), ErrQuotaPolicyUnavailable.Error()},
	} {
		if got := quotaStatusError(test.err); got.Error() != test.code {
			t.Fatalf("unsafe or misclassified failure: %s", got)
		}
	}
}
