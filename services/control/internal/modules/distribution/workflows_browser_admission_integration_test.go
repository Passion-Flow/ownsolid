//go:build knowledge_gateway_integration

package distribution_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/security"
)

// This fixture-only channel observes real SQL and actual backend proxy calls.
// It never changes counters, clocks, policy, sessions or production responses.
func workflowBrowserAdmissionFence(t *testing.T, ctx context.Context, c scenario, root, nonce string, modelCalls *atomic.Int64) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		type counts struct {
			Runs, Intents, AcceptedAudits, GatewayRequests, ActiveRuns int
			ModelCalls                                                 int64
		}
		snapshot := func() (counts, error) {
			var value counts
			e := c.s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM workflow_runs),(SELECT count(*) FROM workflow_mutation_intents),(SELECT count(*) FROM audit_events WHERE action='workflow.run_accepted'),(SELECT count(*) FROM gateway_requests),(SELECT count(*) FROM workflow_runs WHERE state IN ('pending','running'))`).Scan(&value.Runs, &value.Intents, &value.AcceptedAudits, &value.GatewayRequests, &value.ActiveRuns)
			value.ModelCalls = modelCalls.Load()
			return value, e
		}
		policy := c.s.Identity.Config.Policy().Rates
		key := security.MAC(c.s.Identity.HMACKey, "m01-rate", "csrf.ip", "127.0.0.1")
		baseline := map[int]counts{}
		events := []any{}
		last := 0
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			raw, e := os.ReadFile(filepath.Join(root, "admission-request.json"))
			if os.IsNotExist(e) {
				continue
			}
			var request struct {
				Nonce      string `json:"nonce"`
				Sequence   int    `json:"sequence"`
				Operation  string `json:"operation"`
				Proof      int    `json:"proof"`
				Status     int    `json:"status"`
				Code       string `json:"code"`
				RetryAfter int    `json:"retry_after"`
				RequestID  string `json:"request_id"`
			}
			if e != nil || len(raw) > 4096 || json.Unmarshal(raw, &request) != nil || request.Nonce != nonce || request.Sequence < 1 || request.Sequence > 512 {
				t.Error("exact bounded browser admission observation request")
				return
			}
			if request.Sequence == last {
				continue
			}
			if request.Sequence != last+1 {
				t.Error("browser admission observation sequence")
				return
			}
			value, e := snapshot()
			if e != nil {
				t.Error("actual admission SQL snapshot", e)
				return
			}
			var count, retry int
			if e = c.s.DB.QueryRow(ctx, `SELECT COALESCE(max(attempt_count),0),COALESCE(max(GREATEST(0,ceil(EXTRACT(EPOCH FROM expires_at-clock_timestamp())))),0)::int FROM rate_limit_buckets WHERE bucket_key=$1 AND expires_at>clock_timestamp()`, key).Scan(&count, &retry); e != nil {
				t.Error("actual read-only CSRF budget", e)
				return
			}
			switch request.Operation {
			case "before":
				if value.ActiveRuns != 0 {
					t.Error("new browser action overlaps existing model work")
					return
				}
				baseline[request.Sequence] = value
			case "refused":
				before, ok := baseline[request.Proof]
				if !ok || !reflect.DeepEqual(before, value) || request.Status != 429 || request.Code != "RATE_LIMITED" || request.RequestID == "" || request.RetryAfter < 1 || request.RetryAfter > policy.CSRFWindowSeconds {
					t.Error("unaccepted CSRF request changed durable work or actual model calls")
					return
				}
				events = append(events, map[string]any{"owner": "csrf.ip", "path": "/api/v1/auth/csrf", "status": request.Status, "code": request.Code, "retry_after_seconds": request.RetryAfter, "request_id": request.RequestID, "before": before, "after": value, "unchanged": true, "count_limit": policy.CSRFCount, "window_seconds": policy.CSRFWindowSeconds})
			case "budget":
			default:
				t.Error("unknown admission observation")
				return
			}
			ack, _ := json.Marshal(map[string]any{"nonce": nonce, "sequence": request.Sequence, "remaining": policy.CSRFCount - count, "retry_after_seconds": retry, "counts": value})
			if e = os.WriteFile(filepath.Join(root, "admission-ack.pending"), ack, 0600); e != nil || os.Rename(filepath.Join(root, "admission-ack.pending"), filepath.Join(root, "admission-ack.json")) != nil {
				t.Error("admission observation acknowledgment")
				return
			}
			receipt, _ := json.MarshalIndent(map[string]any{"owner": "csrf.ip", "count_limit": policy.CSRFCount, "window_seconds": policy.CSRFWindowSeconds, "events": events, "scope": "Real browser status/header plus read-only SQL/actual backend-call invariants. No counter reset, policy change or fabricated response."}, "", "  ")
			if e = os.WriteFile(filepath.Join(root, "admission-sql-proof.json"), receipt, 0600); e != nil {
				t.Error(e)
				return
			}
			last = request.Sequence
		}
	}()
	return done
}
