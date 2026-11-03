package workflows

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
)

func TestWorkflowRepositoryErrorsRemainSafeAndStable(t *testing.T) {
	for _, code := range []string{"AUTH_SESSION_EXPIRED", "ACCESS_DENIED", "WORKFLOW_DRAFT_CHANGED", "KNOWLEDGE_ANSWER_SOURCE_CHANGED", "MODEL_ALIAS_UNAVAILABLE", "PLUGIN_GRANT_REQUIRED", "IDEMPOTENCY_CONFLICT", string(transaction.CommitUnknown), string(transaction.CommitRolledBack)} {
		if got := workflowRepositoryError(errors.New(code)); got == nil || got.Error() != code {
			t.Fatalf("safe workflow code changed: %s into %v", code, got)
		}
	}
	for _, raw := range []string{"WORKFLOW_ private question=secret", "STORAGE_ sql=secret", "driver statement with private bytes"} {
		if got := workflowRepositoryError(errors.New(raw)); got == nil || got.Error() != "STORAGE_UNAVAILABLE" {
			t.Fatal("private diagnostic escaped workflow boundary")
		}
	}
	if got := workflowRepositoryError(context.Canceled); got.Error() != string(transaction.Cancelled) {
		t.Fatal("cancel code lost")
	}
	if got := workflowRepositoryError(context.DeadlineExceeded); got.Error() != string(transaction.Timeout) {
		t.Fatal("timeout code lost")
	}
	unknown := &transaction.Error{Kind: transaction.CommitUnknown}
	if workflowRepositoryError(unknown) != unknown {
		t.Fatal("unknown commit classification discarded")
	}
}

// These are explicit source/binding assertions, not database execution or a
// substitute SQL adapter. Actual syntax, constraints and rows require real gates.
func TestWorkflowExplicitVendorBindingContracts(t *testing.T) {
	queries := []workflowQuery{
		workflowAudit, workflowReadIntent, workflowSaveIntent, workflowNameExists, workflowLock, workflowResolve, workflowCount, workflowCreate, workflowUpdate, workflowCancelAll,
		workflowVersionScoped, workflowVersionCount, workflowVersionCreate, workflowPublish, workflowVersionByID, workflowConversationLock, workflowConversationCount, workflowConversationCreate, workflowConversationDelete, workflowConversationCancel,
		workflowRunVisible, workflowRunLock, workflowRunByID, workflowConversationBusy, workflowLiveCount, workflowRunCount, workflowRunCreate, workflowTraceCreate, workflowConversationTurn, workflowRunConversation, workflowCancelOne,
		workflowRunTimely, workflowRunClaim, workflowRunRenew, workflowTraceTransition, workflowTraceEnd, workflowRunAnswerSuccess, workflowRunToolSuccess, workflowTraceFailure, workflowRunFailure,
		workflowLockActor, workflowLockWorkspace, workflowLockIdentity, workflowLockOwnedConversation, workflowTracePage, workflowExpiredCandidates, workflowLockExpiredConversation, workflowExpiredEligible, workflowTraceInterrupt, workflowRunInterrupt,
		workflowRetentionWorkspace, workflowRetentionRuns, workflowRetentionConversations, workflowRetentionIntents, workflowDeleteRun, workflowDeleteConversation, workflowDeleteIntent, workflowRetentionAudit,
		workflowRestoreTraces, workflowRestoreRuns, workflowRestoreNext, workflowRestoreAuthority,
	}
	parameter := regexp.MustCompile(`\$([1-9][0-9]*)`)
	for index, q := range queries {
		if strings.TrimSpace(q.postgres) == "" || strings.TrimSpace(q.mysql) == "" {
			t.Fatal("empty vendor statement", index)
		}
		count := 0
		for _, match := range parameter.FindAllStringSubmatch(q.postgres, -1) {
			n, _ := strconv.Atoi(match[1])
			count = max(count, n)
		}
		mysqlCount := count
		if len(q.mysqlOrder) > 0 {
			mysqlCount = len(q.mysqlOrder)
		}
		if strings.Count(q.mysql, "?") != mysqlCount {
			t.Fatal("explicit MySQL binding count", index)
		}
		for _, position := range q.mysqlOrder {
			if position < 1 || position > count {
				t.Fatal("explicit argument order outside owning query", index)
			}
		}
		for _, pgOnly := range []string{"::text", "::uuid", "clock_timestamp()", "make_interval(", "$1"} {
			if strings.Contains(q.mysql, pgOnly) {
				t.Fatal("PG syntax in selected MySQL statement", index)
			}
		}
	}
}
