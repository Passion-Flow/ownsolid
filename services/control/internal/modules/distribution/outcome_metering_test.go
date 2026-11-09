package distribution_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	gatewaywire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/cellroute"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/httpfixture"
	"github.com/google/uuid"
)

func TestPostgresGatewayOutcomeMeteringAtomicRetry(t *testing.T) {
	c := setup(t)
	ctx := t.Context()
	access := c.access(t)
	gateway, credential := access.gateway.Gateway.ID, *access.gateway.Secret
	admit := func() gatewaywire.Outcome {
		t.Helper()
		input := access.input()
		if _, err := c.s.Admit(ctx, gateway, credential, input, uuid.NewString()); err != nil {
			t.Fatal(err)
		}
		return success(input.RequestID)
	}
	legacy := admit()
	prior, err := c.s.Report(ctx, gateway, credential, legacy, uuid.NewString())
	if err != nil || prior.RequestID != legacy.RequestID {
		t.Fatal("default report requires no ledger", err)
	}
	ledger, err := os.ReadFile("../metering/testdata/ledger.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.DB.Exec(ctx, string(ledger)); err != nil {
		t.Fatal(err)
	}
	outcome := admit()
	if _, err := c.s.DB.Exec(ctx, `CREATE FUNCTION refuse_synthetic_gateway_charge() RETURNS trigger
		LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic metering failure'; END $$;
		CREATE TRIGGER reject_synthetic_gateway_charge AFTER INSERT ON usage_ledger
		FOR EACH ROW EXECUTE FUNCTION refuse_synthetic_gateway_charge();`); err != nil {
		t.Fatal(err)
	}
	handler := identityhttp.New(c.s.Identity)
	handler.Distribution.Service.ProjectGatewayReceipts = true
	server := httpfixture.NewTLSServer(t, handler)
	url := httpfixture.AdminOrigin + cellroute.Public("/gateway/v1/instances/"+gateway+"/outcomes", agentwire.LocalScope())
	type reportReply struct {
		status int
		code   string
		ack    gatewaywire.Acknowledgment
		err    error
	}
	report := func(out gatewaywire.Outcome) reportReply {
		body, err := out.Canonical()
		if err != nil {
			return reportReply{err: err}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return reportReply{err: err}
		}
		request.Header.Set("Authorization", "Bearer "+credential)
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			return reportReply{err: err}
		}
		defer response.Body.Close()
		var payload struct {
			Code string                     `json:"code"`
			Data gatewaywire.Acknowledgment `json:"data"`
		}
		err = json.NewDecoder(response.Body).Decode(&payload)
		return reportReply{response.StatusCode, payload.Code, payload.Data, err}
	}
	failed := report(outcome)
	if failed.err != nil || failed.status != http.StatusServiceUnavailable || failed.code != "STORAGE_UNAVAILABLE" ||
		failed.ack != (gatewaywire.Acknowledgment{}) {
		t.Fatalf("ledger failure did not return a safe refusal: %+v", failed)
	}
	var pristine bool
	if err := c.s.DB.QueryRow(ctx, `SELECT state='admitted' AND capacity_held AND outcome IS NULL
		AND outcome_sha256 IS NULL AND reported_at IS NULL AND resolved_at IS NULL
		AND NOT EXISTS(SELECT 1 FROM audit_events WHERE target_id=r.id AND action='gateway.request_reported')
		AND NOT EXISTS(SELECT 1 FROM usage_ledger)
		FROM gateway_requests r WHERE id=$1`, outcome.RequestID).Scan(&pristine); err != nil || !pristine {
		t.Fatal("failed projection left a report, audit event, released capacity or ledger row", err)
	}
	if _, err := c.s.DB.Exec(ctx, "DROP TRIGGER reject_synthetic_gateway_charge ON usage_ledger"); err != nil {
		t.Fatal(err)
	}

	digest, err := outcome.Digest()
	if err != nil {
		t.Fatal(err)
	}
	expected := gatewaywire.Acknowledgment{RequestID: outcome.RequestID, CanonicalSHA256: digest}
	start := make(chan struct{})
	retries := make(chan reportReply, 2)
	for range cap(retries) {
		go func() {
			<-start
			retries <- report(outcome)
		}()
	}
	close(start)
	for range cap(retries) {
		reply := <-retries
		if reply.err != nil || reply.status != http.StatusOK || reply.ack != expected {
			t.Fatalf("exact retry did not acknowledge the original receipt: %+v", reply)
		}
	}
	changed := outcome
	changed.OutputStarted = false
	if reply := report(changed); reply.err != nil || reply.status != http.StatusConflict || reply.code != "GATEWAY_OUTCOME_CONFLICT" {
		t.Fatalf("changed report was not refused: %+v", reply)
	}
	for range 2 {
		if reply := report(legacy); reply.err != nil || reply.status != http.StatusOK || reply.ack != prior {
			t.Fatalf("predecessor report did not replay: %+v", reply)
		}
	}

	for _, id := range []string{outcome.RequestID, legacy.RequestID} {
		var ledgerRows, auditRows int
		if err := c.s.DB.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM usage_ledger l WHERE l.producer='gateway/receipt-v1' AND l.event_id=r.id::text
				AND l.tenant_id=r.tenant_id AND l.workspace_id=r.workspace_id AND l.occurred_at=r.admitted_at
				AND l.requests=1 AND l.tokens=9 AND l.unmeasured_token_requests=0),
			(SELECT count(*) FROM audit_events WHERE target_id=r.id AND action='gateway.request_reported')
			FROM gateway_requests r WHERE id=$1 AND state='reported' AND NOT capacity_held
				AND tenant_id=$2 AND workspace_id=$3 AND gateway_id=$4`,
			id, c.a.Me.Workspace.TenantID, access.alias.WorkspaceID, gateway).Scan(&ledgerRows, &auditRows); err != nil || ledgerRows != 1 || auditRows != 1 {
			t.Fatal("expected one correctly scoped charge and report audit for the original receipt", err, ledgerRows, auditRows)
		}
	}
	var ledgerRows int
	if err := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM usage_ledger").Scan(&ledgerRows); err != nil || ledgerRows != 2 {
		t.Fatal("projection wrote an extra ledger event", err, ledgerRows)
	}
}
