package scheduling

import (
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/scheduling/celladapter"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestQueueTurnReasonUsesCurrentExactRank(t *testing.T) {
	earlier := QueueEntry{JobID: "earlier", QueueID: "team-a", Sequence: 1}
	later := QueueEntry{JobID: "later", QueueID: "team-b", Sequence: 2}
	entries := []QueueEntry{later, earlier}
	queues := map[string]QueueShare{
		"team-a": {Capacity: Resources{NVIDIAGPU: 4}},
		"team-b": {Capacity: Resources{NVIDIAGPU: 4}},
	}
	ranked, err := RankQueue(entries, queues)
	if err != nil {
		t.Fatal(err)
	}
	if reason, ok := QueueTurnReason(ranked, later); !ok || reason != (WaitReason{
		Code: "SCHEDULING_QUEUE_TURN_PENDING", Action: WaitForQueueTurn,
	}) {
		t.Fatalf("deferral: %+v, %v", reason, ok)
	}
	changed := later
	changed.Sequence++
	if _, ok := QueueTurnReason(ranked, changed); ok {
		t.Fatal("changed entry inherited an earlier rank")
	}
	queues["team-a"] = QueueShare{Capacity: Resources{NVIDIAGPU: 4}, Reserved: Resources{NVIDIAGPU: 1}}
	ranked, err = RankQueue(entries, queues)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := QueueTurnReason(ranked, later); ok {
		t.Fatal("head still has a queue waiting reason after the rank changed")
	}
}

func TestPreflightWaitReasonsKeepSharedCapacityPrivate(t *testing.T) {
	tree, err := BuildQuotaTree(quotaFixture())
	if err != nil {
		t.Fatal(err)
	}
	intent := reservationIntent(Attempt{Kind: Training, SourceID: "preview"}, 3)
	reserved := map[string]Resources{
		"workspace-a": {NVIDIAGPU: 2}, "department": {NVIDIAGPU: 6},
		"tenant": {NVIDIAGPU: 6}, "platform": {NVIDIAGPU: 6},
	}
	cell := CellCapacity{Total: Resources{NVIDIAGPU: 2, CPU: 1000}}
	estimate, err := tree.Preflight("tenant", "workspace-a", intent, reserved, cell)
	if err != nil {
		t.Fatal(err)
	}
	reasons := estimate.WaitReasons()
	if len(reasons) != 5 || reasons[0].Available == nil || *reasons[0].Available != 2 ||
		reasons[0].Action != WaitForCapacity || reasons[4].Action != ChangeRequestOrCell {
		t.Fatalf("preflight reasons: %+v", reasons)
	}
	for _, reason := range reasons[1:] {
		if reason.Available != nil || reason.Resource != NVIDIAGPU || reason.Requested != 3 {
			t.Fatalf("shared capacity leaked or reason omitted request: %+v", reason)
		}
	}
	*reasons[0].Available = 100
	if estimate.Limits[0].Available != 2 {
		t.Fatal("wait reason mutated the estimate")
	}
	if reasons := (PreflightResult{}).WaitReasons(); len(reasons) != 0 {
		t.Fatalf("fit produced waiting guidance: %+v", reasons)
	}
	if reasons := WaitReasons(ErrInvalidResources); len(reasons) != 0 {
		t.Fatalf("unrelated failure produced waiting guidance: %+v", reasons)
	}
}

func TestPostgresWaitReasonsRefreshWithQuotaAndRelease(t *testing.T) {
	f := newReservationFixture(t)
	if changed, err := reserveInTransaction(t.Context(), f.store, f.first, reservationIntent(f.first, 2)); err != nil || !changed {
		t.Fatalf("reserve competing attempt: %v, %v", changed, err)
	}
	a := newBorrowingAttempt(f.first, f.first.WorkspaceID)
	intent := reservationIntent(a, 5)
	assertQuotaReason := func(code LimitReason, action WaitAction, available int64) {
		t.Helper()
		changed, err := reserveInTransaction(t.Context(), f.store, a, intent)
		reasons := WaitReasons(err)
		want := []WaitReason{{Code: string(code), Action: action, Level: WorkspaceQuota, Resource: NVIDIAGPU,
			Requested: 5, Available: &available}}
		if changed || !errors.Is(err, ErrReservationQuota) || !reflect.DeepEqual(reasons, want) {
			t.Fatalf("reservation decision: changed=%v reasons=%+v err=%v", changed, reasons, err)
		}
	}
	assertQuotaReason(QuotaTooSmall, ChangeRequestOrQuota, 4)
	f.policy[0].Limits.Burst[NVIDIAGPU] = 5
	f.policy[4].Limits.Burst[NVIDIAGPU] = 3
	if err := pgx.BeginFunc(t.Context(), f.pool, func(tx pgx.Tx) error {
		_, _, err := ReplaceQuotaWithin(t.Context(), tx, 1, f.policy)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertQuotaReason(QuotaInUse, WaitForCapacity, 3)
	if changed, err := releaseInTransaction(t.Context(), f.store, f.first, fixtureReplicaReleases(f.first, reservationIntent(f.first, 2))...); err != nil || !changed {
		t.Fatalf("release competing attempt: %v, %v", changed, err)
	}
	for _, wantChanged := range []bool{true, false} {
		changed, err := reserveInTransaction(t.Context(), f.store, a, intent)
		if err != nil || changed != wantChanged || len(WaitReasons(err)) != 0 {
			t.Fatalf("allowed reservation or replay produced a reason: %v, %v", changed, err)
		}
	}
}

func TestPostgresWaitReasonsKeepForeignScopeAndVictimsPrivate(t *testing.T) {
	f := newBorrowingFixture(t)
	reserveBorrowing(t, f.store, f.first, 2)
	intent := borrowingIntent(f.lender, 2)
	if _, err := requestReclaimInTransaction(t.Context(), f.store, f.lender, intent); err != nil {
		t.Fatal(err)
	}

	foreign := f.lender
	foreign.TenantID = uuid.NewString()
	absent := foreign
	absent.WorkspaceID = uuid.NewString()
	want := []WaitReason{{Code: ErrQuotaScopeNotFound.Error(), Action: VerifyWorkspaceAccess}}
	for _, denied := range []Attempt{foreign, absent} {
		if changed, err := reserveInTransaction(t.Context(), f.store, denied, intent); changed ||
			!errors.Is(err, ErrQuotaScopeNotFound) || !reflect.DeepEqual(WaitReasons(err), want) {
			t.Fatalf("reserve leaked a denied scope: %v, %v", changed, err)
		}
		if plan, err := requestReclaimInTransaction(t.Context(), f.store, denied, intent); plan.State != "" ||
			len(plan.Victims) != 0 || !errors.Is(err, ErrQuotaScopeNotFound) || !reflect.DeepEqual(WaitReasons(err), want) {
			t.Fatalf("reclaim leaked a denied scope: %+v, %v", plan, err)
		}
	}
}

func TestCellWaitReasonsExposeOnlyFixedGuidance(t *testing.T) {
	for _, tc := range []struct {
		blocker celladapter.Blocker
		action  WaitAction
	}{
		{celladapter.WaitingForQuota, WaitForCellQuota},
		{celladapter.ExceedsMaxQuota, ChangeRequestOrQuota},
		{celladapter.TopologyPlacementFailed, ReviewCellTopology},
		{celladapter.QueueInadmissible, ReviewCellQueue},
		{celladapter.PodsNotReady, WaitForCellPods},
	} {
		got := cellWaitReasons(tc.blocker)
		want := WaitReason{Code: string(tc.blocker), Action: tc.action}
		if len(got) != 1 || got[0] != want {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
	if got := cellWaitReasons("unrecognized-cell-text"); got != nil {
		t.Fatalf("unknown blocker leaked: %+v", got)
	}
}

func TestObserveCellWaitReasonsRequiresCurrentBoundUID(t *testing.T) {
	token := rand.Text()
	type response struct {
		uid        string
		generation int
		forbidden  bool
	}
	var current atomic.Pointer[response]
	const boundUID = "owner-bound-uid"
	current.Store(&response{uid: boundUID, generation: 2})
	var reads atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet ||
			r.URL.Path != "/apis/kueue.x-k8s.io/v1beta2/namespaces/workspace-a/workloads/job" ||
			r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("unexpected cell request")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		state := current.Load()
		if state.forbidden {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		fmt.Fprintf(w, `{"apiVersion":"kueue.x-k8s.io/v1beta2","kind":"Workload",
"metadata":{"name":"job","namespace":"workspace-a","uid":%q,"resourceVersion":"private-version","generation":2},
"status":{"conditions":[{"type":"QuotaReserved","status":"False","reason":"WaitingForQuota",
"message":"private queue and capacity data","observedGeneration":%d}]}}`, state.uid, state.generation)
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	caFile, tokenFile := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "token")
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caFile, certificate, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := celladapter.New(celladapter.Config{
		Origin: server.URL, DialAddress: netip.MustParseAddr("127.0.0.1"),
		CAFile: caFile, TokenFile: tokenFile, Timeout: 5 * time.Second, MaxConcurrent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	binding := celladapter.Reference{Scheduler: celladapter.Kueue, Namespace: "workspace-a", Name: "job"}
	if got, err := ObserveCellWaitReasons(t.Context(), client, binding); !errors.Is(err, celladapter.ErrReference) || got != nil || reads.Load() != 0 {
		t.Fatalf("incomplete binding reached cell or produced guidance: %+v, %v", got, err)
	}
	binding.UID = boundUID
	if got, err := ObserveCellWaitReasons(t.Context(), nil, binding); !errors.Is(err, celladapter.ErrConfiguration) || got != nil {
		t.Fatalf("missing cell client: %+v, %v", got, err)
	}

	want := []WaitReason{{Code: string(celladapter.WaitingForQuota), Action: WaitForCellQuota}}
	for _, tc := range []struct {
		name    string
		state   response
		reasons []WaitReason
		err     error
	}{
		{"current", response{uid: binding.UID, generation: 2}, want, nil},
		{"stale", response{uid: binding.UID, generation: 1}, nil, nil},
		{"replaced", response{uid: "new-uid", generation: 2}, nil, celladapter.ErrReplaced},
		{"forbidden", response{forbidden: true}, nil, celladapter.ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current.Store(&tc.state)
			got, err := ObserveCellWaitReasons(t.Context(), client, binding)
			if !errors.Is(err, tc.err) || !reflect.DeepEqual(got, tc.reasons) {
				t.Fatalf("guidance: %+v, %v; want %+v, %v", got, err, tc.reasons, tc.err)
			}
		})
	}
}

func TestWaitReasonsMissingQuotaConfigurationRequiresTypedDecision(t *testing.T) {
	want := []WaitReason{{Code: "SCHEDULING_QUOTA_UNCONFIGURED", Action: "configure_quota_policy"}}
	for _, err := range []error{ErrQuotaUnconfigured, fmt.Errorf("owner admission: %w", ErrQuotaUnconfigured)} {
		if got := WaitReasons(err); !reflect.DeepEqual(got, want) {
			t.Fatalf("configuration guidance: %+v, want %+v", got, want)
		}
	}
	if got := WaitReasons(errors.New(ErrQuotaUnconfigured.Error())); len(got) != 0 {
		t.Fatalf("historical text became a fresh decision: %+v", got)
	}
}
