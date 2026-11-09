//go:build gateway_integration

package distribution_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	gatewaywire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/google/uuid"
)

func TestRealGoRustPostgresLostOutcomeReply(t *testing.T) {
	realGatewayChain(t, lostOutcomeReply)
}

func TestRealGoRustPostgresRestartAfterLostOutcomeReply(t *testing.T) {
	realGatewayChain(t, restartAfterLostOutcomeReply)
}

type outcomeReplyLoss struct {
	path        string
	attempts    atomic.Int32
	first       chan gatewaywire.Acknowledgment
	replay      chan gatewaywire.Acknowledgment
	allowReplay chan struct{}
}

func newOutcomeReplyLoss(gatewayID string) *outcomeReplyLoss {
	return &outcomeReplyLoss{
		path:        cellroute.Public("/gateway/v1/instances/"+gatewayID+"/outcomes", agentwire.LocalScope()),
		first:       make(chan gatewaywire.Acknowledgment, 1),
		replay:      make(chan gatewaywire.Acknowledgment, 1),
		allowReplay: make(chan struct{}),
	}
}

func (loss *outcomeReplyLoss) wrap(t *testing.T, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != loss.path {
			next.ServeHTTP(w, r)
			return
		}
		attempt := loss.attempts.Add(1)
		if attempt != 1 {
			select {
			case <-loss.allowReplay:
			case <-r.Context().Done():
				return
			}
		}
		recorder := httptest.NewRecorder()
		next.ServeHTTP(recorder, r)
		var reply struct{ Data gatewaywire.Acknowledgment }
		err := json.Unmarshal(recorder.Body.Bytes(), &reply)
		if recorder.Code != http.StatusOK || err != nil || reply.Data.RequestID == "" || reply.Data.CanonicalSHA256 == "" {
			t.Errorf("Control outcome acknowledgment failed on attempt %d: HTTP %d (%v)", attempt, recorder.Code, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if attempt == 1 {
			// The real dispatcher has committed. Drop the TLS connection before any reply bytes are sent.
			conn, _, err := http.NewResponseController(w).Hijack()
			if err != nil {
				t.Error("cannot drop the committed acknowledgment", err)
				return
			}
			if err := conn.Close(); err != nil {
				t.Error("cannot close the acknowledgment connection", err)
			}
			loss.first <- reply.Data
			return
		}
		for name, values := range recorder.Header() {
			w.Header()[name] = values
		}
		w.WriteHeader(recorder.Code)
		if _, err := w.Write(recorder.Body.Bytes()); err != nil {
			t.Error("cannot deliver the replay acknowledgment", err)
		}
		if attempt == 2 {
			loss.replay <- reply.Data
		}
	})
}

func (loss *outcomeReplyLoss) verify(t *testing.T, c scenario, access access, state string, response *http.Response, generations *atomic.Int32, restart func(verifyStopped func())) {
	t.Helper()
	requestID := response.Header.Get("X-Request-ID")
	var completion struct {
		Model string            `json:"model"`
		Usage gatewaywire.Usage `json:"usage"`
	}
	decodeErr := json.NewDecoder(response.Body).Decode(&completion)
	response.Body.Close()
	usage := gatewaywire.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}
	if _, err := uuid.Parse(requestID); err != nil || decodeErr != nil || response.StatusCode != http.StatusOK ||
		completion.Model != access.alias.Name || completion.Usage != usage {
		t.Fatalf("invalid native completion: HTTP %d (%v, %v)", response.StatusCode, err, decodeErr)
	}
	await := func(reply <-chan gatewaywire.Acknowledgment) gatewaywire.Acknowledgment {
		t.Helper()
		select {
		case receipt := <-reply:
			return receipt
		case <-time.After(8 * time.Second):
			t.Fatal("timed out waiting for the actual Control receipt")
			return gatewaywire.Acknowledgment{}
		}
	}
	first := await(loss.first)
	if first.RequestID != requestID {
		t.Fatal("Control acknowledged a different request")
	}
	readReported := func() ([]byte, time.Time) {
		t.Helper()
		var body []byte
		var reportedAt time.Time
		var auditCount int
		err := c.s.DB.QueryRow(t.Context(), `
			SELECT outcome,reported_at,(SELECT count(*) FROM audit_events WHERE action='gateway.request_reported' AND target_id=r.id)
			FROM gateway_requests r
			WHERE id=$1 AND tenant_id=$2 AND workspace_id=$3 AND node_id=$4 AND gateway_id=$5
				AND api_key_id=$6 AND actor_user_id=$7 AND alias_id=$8 AND alias_revision=$9
				AND deployment_id=$10 AND deployment_revision=$11 AND outcome_sha256=$12
				AND state='reported' AND NOT capacity_held`,
			requestID, c.a.Me.Workspace.TenantID, access.alias.WorkspaceID, c.node.NodeID, access.gateway.Gateway.ID,
			access.key.Key.ID, access.owner.Me.User.ID, access.alias.ID, access.alias.Revision,
			access.alias.DeploymentID, access.alias.DeploymentRevision, first.CanonicalSHA256).Scan(&body, &reportedAt, &auditCount)
		if err != nil || auditCount != 1 {
			t.Fatalf("expected one scoped, committed outcome and audit event: %v, count %d", err, auditCount)
		}
		if restart != nil {
			var total, matching int
			err = c.s.DB.QueryRow(t.Context(), `SELECT count(*), count(*) FILTER (
				WHERE l.producer='gateway/receipt-v1' AND l.event_id=$1 AND l.tenant_id=r.tenant_id
					AND l.workspace_id=r.workspace_id AND l.occurred_at=r.admitted_at
					AND l.requests=1 AND l.tokens=$2 AND l.unmeasured_token_requests=0)
				FROM usage_ledger l CROSS JOIN gateway_requests r WHERE r.id=$1::uuid`,
				requestID, usage.TotalTokens).Scan(&total, &matching)
			if err != nil || total != 1 || matching != 1 {
				t.Fatalf("expected one scoped metering event: %v, total %d, matching %d", err, total, matching)
			}
		}
		return body, reportedAt
	}
	initial, reportedAt := readReported()
	outcome, err := gatewaywire.DecodeOutcome(initial)
	if err != nil || outcome.Status != "succeeded" || outcome.Usage == nil || *outcome.Usage != usage {
		t.Fatal("committed outcome differs from native response", err)
	}
	digest, err := outcome.Digest()
	if err != nil || digest != first.CanonicalSHA256 {
		t.Fatal("Control acknowledgment does not identify the stored outcome", err)
	}
	pending := filepath.Join(state, "outcomes", requestID+".json")
	data, err := os.ReadFile(pending)
	if err != nil {
		t.Fatal("native spool did not retain the unacknowledged outcome", err)
	}
	var record struct {
		Outcome         gatewaywire.Outcome `json:"outcome"`
		CanonicalSHA256 string              `json:"canonical_sha256"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal("invalid native spool record", err)
	}
	pendingDigest, err := record.Outcome.Digest()
	if err != nil || pendingDigest != digest || record.CanonicalSHA256 != digest {
		t.Fatal("native spool differs from the committed outcome", err)
	}

	if restart != nil {
		restart(func() {
			retained, err := os.ReadFile(pending)
			if err != nil || !bytes.Equal(data, retained) || loss.attempts.Load() != 1 {
				t.Fatal("the exited gateway changed the spool or attempted replay before restart", err)
			}
		})
	}
	close(loss.allowReplay)
	if replay := await(loss.replay); replay != first {
		t.Fatal("Control replay changed the canonical acknowledgment")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Stat(pending)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatal("native spool failed to remove the acknowledged outcome", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	final, finalReportedAt := readReported()
	if !bytes.Equal(initial, final) || !reportedAt.Equal(finalReportedAt) || generations.Load() != 1 || loss.attempts.Load() != 2 {
		t.Fatal("replay regenerated the request, rewrote its outcome, or sent an extra report")
	}
}
