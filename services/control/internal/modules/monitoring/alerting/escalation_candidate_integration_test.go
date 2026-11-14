//go:build alerting_candidate && alerting_integration && linux

package alerting

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed testdata/escalation-candidate.sql
var escalationFixtureDDL string

const escalationTenant = "tenant-a"
const escalationWorkspace = "work-a"
const escalationObserve = "candidate-observe"
const escalationSend = "candidate-escalate"

func TestEscalationCandidateWithAlertmanager(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 72*time.Second)
	defer cancel()
	pool := openEscalationFixture(t, ctx)
	root := t.TempDir()
	sink := escalationReceiptFixture(t, root)
	origin, err := url.Parse(sink.URL)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(sink.Certificate())
	tokenPath := filepath.Join(root, "webhook-token")
	token := relayFixtureSecret(t, tokenPath)
	configured := func(name string) *webhookRelay {
		return newWebhookRelay(relaySettings{
			origin: origin, roots: roots, channel: feishuChannel, timeout: 2 * time.Second, concurrent: 4,
			tenants: map[string]tenantRoute{escalationTenant: {
				receiver: name, inbound: sha256.Sum256([]byte(token)), target: "fixture-destination",
				outbound: relayFixtureSecret(t, filepath.Join(root, name+"-signing")),
			}},
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}
	observationRelay, escalationRelay := configured(escalationObserve), configured(escalationSend)
	t.Cleanup(observationRelay.client.CloseIdleConnections)
	t.Cleanup(escalationRelay.client.CloseIdleConnections)

	type callback struct {
		event   relayDelivery
		outcome deliveryOutcome
	}
	observed := make(chan callback, 16)
	escalated := make(chan callback, 16)
	mux := http.NewServeMux()
	ingress := httptest.NewUnstartedServer(mux)
	defer ingress.Close()
	base := "https://" + ingress.Listener.Addr().String()
	cert, key, ingressClient := relayFixtureCertificate(t, root)
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	ingress.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	managerClient := &http.Client{Transport: &http.Transport{}, Timeout: 3 * time.Second}
	t.Cleanup(managerClient.CloseIdleConnections)
	manager := startEscalationFixtureManager(t, ctx, managerClient, root, base, cert, tokenPath)
	gate, err := newEscalationGate(pool, escalationRelay, manager, escalationPolicy{
		delay: 250 * time.Millisecond, lease: 5 * time.Second, attemptLimit: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(gate.client.CloseIdleConnections)
	mount := func(prefix string, relay *webhookRelay, deliver func(context.Context, relayDelivery) deliveryOutcome, calls chan<- callback) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			relay.serveWebhook(w, r, func(ctx context.Context, event relayDelivery) deliveryOutcome {
				result := deliver(ctx, event)
				select {
				case calls <- callback{event, result}:
				default:
					t.Error("unexpected callback volume")
				}
				return result
			})
		})
		mux.Handle(prefix+"/", http.StripPrefix(prefix, handler))
	}
	mount("/observe", observationRelay, gate.observe, observed)
	mount("/escalate", escalationRelay, gate.escalate, escalated)
	ingress.StartTLS()
	if status, err := relayFixtureRequest(ctx, ingressClient, http.MethodPost, base+"/observe"+webhookPath+"tenant-b", token, nil, nil); err != nil || status != http.StatusUnauthorized {
		t.Fatalf("cross-tenant webhook status %d: %v", status, err)
	}

	create := func(name string) fixtureSubmittedAlert {
		start := time.Now().UTC().Truncate(time.Millisecond)
		return fixtureSubmittedAlert{
			Labels:   map[string]string{"tenant_id": escalationTenant, "workspace_id": escalationWorkspace, "alertname": name, "severity": "critical"},
			StartsAt: start, EndsAt: start.Add(10 * time.Minute),
		}
	}
	publish := func(alert fixtureSubmittedAlert) {
		t.Helper()
		if err := alertRequest(ctx, managerClient, http.MethodPost, manager+"/api/v2/alerts", "application/json", fixtureRelayJSON(t, []fixtureSubmittedAlert{alert}), nil); err != nil {
			t.Fatal(err)
		}
	}
	await := func(ch <-chan callback, alert fixtureSubmittedAlert, status string, want deliveryOutcome) (alertOccurrence, relayDelivery) {
		t.Helper()
		for {
			select {
			case got := <-ch:
				o, valid := webhookOccurrence(got.event)
				if !valid {
					t.Fatal("Alertmanager webhook did not yield an exact occurrence")
				}
				if o.name == alert.Labels["alertname"] && o.started.Equal(alert.StartsAt) &&
					got.event.batch.Status == status && got.outcome == want {
					return o, got.event
				}
			case <-ctx.Done():
				t.Fatalf("waiting for %s %s outcome %s: %v", alert.Labels["alertname"], status, want, ctx.Err())
			}
		}
	}

	oldAlert := create("NodeUnreachable")
	publish(oldAlert)
	old, _ := await(observed, oldAlert, "firing", delivered)
	for _, scope := range []occurrenceScope{{"tenant-b", old.workspace}, {old.tenant, "work-b"}} {
		if _, err := gate.store.acknowledge(ctx, acknowledgementPrincipal{scope, "foreign-responder"}, old.id); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("foreign scope unexpectedly obtained a receipt: %v", err)
		}
	}
	first := raceEscalationAcknowledgements(t, ctx, gate.store, old)
	_, _ = await(escalated, oldAlert, "firing", delivered)
	requireEscalationState(t, ctx, pool, old, true, false, false, 0)

	oldAlert.EndsAt = time.Now().UTC().Truncate(time.Millisecond).Add(-time.Millisecond)
	publish(oldAlert)
	_, _ = await(observed, oldAlert, "resolved", delivered)
	_, _ = await(escalated, oldAlert, "resolved", delivered)
	requireEscalationState(t, ctx, pool, old, true, true, false, 0)
	if _, err := gate.store.acknowledge(ctx, acknowledgementPrincipal{old.occurrenceScope, first.actor}, old.id); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("closed occurrence accepted a confirmation: %v", err)
	}

	nextAlert := create("NodeUnreachable")
	publish(nextAlert)
	next, _ := await(observed, nextAlert, "firing", delivered)
	if next.id == old.id || next.fingerprint != old.fingerprint || !next.started.After(old.started) {
		t.Fatalf("recurrence did not preserve the fingerprint and change the occurrence: %v / %v", old, next)
	}
	_, _ = await(escalated, nextAlert, "firing", feishuRetry)
	_, _ = await(escalated, nextAlert, "firing", delivered)
	requireEscalationState(t, ctx, pool, next, false, false, true, 2)
	requireRetryHistory(t, ctx, pool, next)
	received, err := os.ReadDir(filepath.Join(root, "received"))
	if err != nil || len(received) != 1 || received[0].Name() != next.deliveryKey() {
		t.Fatalf("expected one persisted escalation under the original key: %v %v", received, err)
	}

	muted := create("Maintenance")
	publish(muted)
	mutedOccurrence, mutedDelivery := await(observed, muted, "firing", delivered)
	relayFixtureWait(t, ctx, func() bool { return gate.live(ctx, mutedOccurrence) })
	now := time.Now()
	silence := fixtureRelayJSON(t, map[string]any{
		"matchers": []map[string]any{
			{"name": "tenant_id", "value": escalationTenant, "isRegex": false, "isEqual": true},
			{"name": "workspace_id", "value": escalationWorkspace, "isRegex": false, "isEqual": true},
			{"name": "alertname", "value": "Maintenance", "isRegex": false, "isEqual": true},
		},
		"startsAt": now.Add(-time.Minute), "endsAt": now.Add(time.Hour),
		"createdBy": "fixture", "comment": "Verify separate silence state",
	})
	var silenceReply struct{ SilenceID string }
	if err := alertRequest(ctx, managerClient, http.MethodPost, manager+"/api/v2/silences", "application/json", silence, &silenceReply); err != nil || silenceReply.SilenceID == "" {
		t.Fatalf("silence creation: %v", err)
	}
	relayFixtureWait(t, ctx, func() bool {
		ready, _, err := gate.store.admission(ctx, mutedOccurrence, gate.policy.attemptLimit)
		return err == nil && ready && !gate.live(ctx, mutedOccurrence)
	})
	// Replay the captured native event after silence to exercise the fresh
	// source check even though Alertmanager normally withholds the callback.
	if got := gate.escalate(ctx, mutedDelivery); got != candidateRetry {
		t.Fatalf("late callback during silence returned %s", got)
	}
	requireEscalationState(t, ctx, pool, mutedOccurrence, false, false, false, 0)

	nextAlert.EndsAt = time.Now().UTC().Truncate(time.Millisecond).Add(-time.Millisecond)
	publish(nextAlert)
	_, _ = await(observed, nextAlert, "resolved", delivered)
	requireEscalationState(t, ctx, pool, next, false, true, true, 2)
	checkEscalationLeaseFence(t, ctx, gate.store)
	t.Logf("Alertmanager %s, relay %s, site receiver %s, PostgreSQL 127.0.0.1:5432", manager, base, sink.URL)
}

func openEscalationFixture(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(os.Getenv("OWNSOLID_TEST_ESCALATION_DSN"))
	if err != nil || config.ConnConfig.Host != "127.0.0.1" || config.ConnConfig.Port != 5432 ||
		config.ConnConfig.Database != "ownsolid_escalation_test" || config.ConnConfig.User != "fixture" ||
		len(config.ConnConfig.Fallbacks) != 0 {
		t.Fatal("require the isolated escalation PostgreSQL fixture")
	}
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	relayFixtureWait(t, ctx, func() bool { return pool.Ping(ctx) == nil })
	if _, err := pool.Exec(ctx, escalationFixtureDDL); err != nil {
		t.Fatal(err)
	}
	return pool
}

func startEscalationFixtureManager(t *testing.T, ctx context.Context, client *http.Client, root, ingress, caFile, tokenFile string) string {
	t.Helper()
	var config strings.Builder
	config.WriteString(`route:
  receiver: discard
  group_by: [tenant_id, workspace_id, alertname]
  group_wait: 200ms
  group_interval: 2s
  repeat_interval: 30s
  routes:
    - receiver: candidate-observe
      matchers: ['tenant_id="tenant-a"']
      continue: true
    - receiver: candidate-escalate
      matchers: ['tenant_id="tenant-a"']
      group_wait: 5s
receivers:
  - name: discard
`)
	for _, target := range []struct{ name, prefix string }{{escalationObserve, "/observe"}, {escalationSend, "/escalate"}} {
		fmt.Fprintf(&config, `  - name: %s
    webhook_configs:
      - url: %q
        timeout: 8s
        send_resolved: true
        http_config:
          follow_redirects: false
          authorization:
            type: Bearer
            credentials_file: %q
          tls_config:
            ca_file: %q
`, target.name, ingress+target.prefix+webhookPath+escalationTenant, tokenFile, caFile)
	}
	path := filepath.Join(root, "candidate-alertmanager.yml")
	writeFixture(t, path, []byte(config.String()))
	return runAlertService(t, ctx, client, "OWNSOLID_TEST_ALERTMANAGER", "/-/ready",
		"--config.file="+path, "--storage.path="+filepath.Join(root, "manager"), "--cluster.listen-address=")
}

func escalationReceiptFixture(t *testing.T, root string) *httptest.Server {
	t.Helper()
	directory := filepath.Join(root, "received")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/bot/v2/hook/fixture-destination" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodOptions {
			w.Header().Set(relayCapabilityHeader, relayCapabilityVersion)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		key, err := strconv.Unquote(r.Header.Get(idempotencyKeyHeader))
		id, matches := strings.CutPrefix(key, "ownsolid-am-escalation-v1-")
		var body feishuMessage
		if r.Method != http.MethodPost || err != nil || !matches || !occurrenceID.MatchString(id) ||
			json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body) != nil || body.MsgType != "text" {
			http.Error(w, "invalid escalation request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		previous, err := os.ReadFile(filepath.Join(directory, key))
		if os.IsNotExist(err) {
			if err := persistBotFixture(directory, key, body.Content.Text); err != nil {
				t.Errorf("persist receiver state: %v", err)
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			// The receiver committed the message, but the first response
			// does not contain the durable acknowledgement.
			io.WriteString(w, `{"code":0}`)
			return
		}
		if err != nil || string(previous) != body.Content.Text {
			http.Error(w, "conflicting replay", http.StatusConflict)
			return
		}
		w.Header().Set(idempotencyKeyHeader, strconv.Quote(key))
		io.WriteString(w, `{"code":0}`)
	}))
	t.Cleanup(server.Close)
	return server
}

func raceEscalationAcknowledgements(t *testing.T, ctx context.Context, store escalationStore, o alertOccurrence) acknowledgementReceipt {
	t.Helper()
	const responders = 12
	type result struct {
		ack acknowledgementReceipt
		err error
	}
	results := make(chan result, responders)
	start := make(chan struct{})
	for i := range responders {
		go func() {
			<-start
			actor := fmt.Sprintf("responder-%d", i)
			ack, err := store.acknowledge(ctx, acknowledgementPrincipal{o.occurrenceScope, actor}, o.id)
			results <- result{ack, err}
		}()
	}
	close(start)
	var first acknowledgementReceipt
	for range responders {
		got := <-results
		if got.err != nil || got.ack.occurrenceScope != o.occurrenceScope || got.ack.id != o.id ||
			!strings.HasPrefix(got.ack.actor, "responder-") || got.ack.at.Before(o.started) {
			t.Fatalf("incorrect acknowledgement receipt: %+v %v", got.ack, got.err)
		}
		if first.actor == "" {
			first = got.ack
		}
		if got.ack.actor != first.actor || !got.ack.at.Equal(first.at) {
			t.Fatalf("multiple acknowledgement winners: %+v and %+v", first, got.ack)
		}
	}
	return first
}

func requireEscalationState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, o alertOccurrence, acknowledged, resolved, sent bool, attempts int) {
	t.Helper()
	var ack, close, delivered bool
	var count int
	err := pool.QueryRow(ctx, `
SELECT ack_at IS NOT NULL, resolved_at IS NOT NULL, sent_at IS NOT NULL, attempt_no
FROM ownsolid_alert_candidate WHERE tenant_id=$1 AND workspace_id=$2 AND occurrence_id=$3`,
		o.tenant, o.workspace, o.id).Scan(&ack, &close, &delivered, &count)
	if err != nil || ack != acknowledged || close != resolved || delivered != sent || count != attempts {
		t.Fatalf("unexpected %s state: ack=%t resolved=%t sent=%t attempts=%d: %v", o.name, ack, close, delivered, count, err)
	}
}

func requireRetryHistory(t *testing.T, ctx context.Context, pool *pgxpool.Pool, o alertOccurrence) {
	t.Helper()
	rows, err := pool.Query(ctx, `
SELECT attempt_no, delivery_key, result, completed_at >= started_at, lease_until > started_at
FROM ownsolid_alert_attempt_candidate
WHERE tenant_id=$1 AND workspace_id=$2 AND occurrence_id=$3 ORDER BY attempt_no`, o.tenant, o.workspace, o.id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for i, want := range []string{"retryable", "delivered"} {
		if !rows.Next() {
			t.Fatalf("missing attempt %d: %v", i+1, rows.Err())
		}
		var number int
		var key, outcome string
		var completed, leased bool
		if err := rows.Scan(&number, &key, &outcome, &completed, &leased); err != nil || number != i+1 ||
			key != o.deliveryKey() || outcome != want || !completed || !leased {
			t.Fatalf("unexpected attempt %d: %s completed=%t leased=%t: %v", number, outcome, completed, leased, err)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra attempts: %v", rows.Err())
	}
}

func checkEscalationLeaseFence(t *testing.T, ctx context.Context, store escalationStore) {
	t.Helper()
	digest := sha256.Sum256([]byte("fixture-expired-lease"))
	o := alertOccurrence{
		occurrenceScope: occurrenceScope{escalationTenant, escalationWorkspace},
		id:              fmt.Sprintf("%x", digest), fingerprint: "0000000000000001",
		started: time.Now().UTC().Truncate(time.Millisecond),
	}
	policy := escalationPolicy{delay: time.Millisecond, lease: 5 * time.Second, attemptLimit: 2}
	if err := store.observe(ctx, o, false, policy.delay); err != nil {
		t.Fatal(err)
	}
	relayFixtureWait(t, ctx, func() bool {
		ready, _, err := store.admission(ctx, o, policy.attemptLimit)
		return err == nil && ready
	})
	first, err := store.claim(ctx, o, policy)
	if err != nil || first != 1 {
		t.Fatalf("initial attempt: %d %v", first, err)
	}
	if _, err := store.claim(ctx, o, policy); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second claim overlapped the first lease: %v", err)
	}
	// Simulate a worker crash and an expired lease without sleeping.
	if _, err := store.db.Exec(ctx, `
UPDATE ownsolid_alert_candidate SET lease_until = clock_timestamp() - INTERVAL '1 second'
WHERE tenant_id = $1 AND workspace_id = $2 AND occurrence_id = $3`, o.tenant, o.workspace, o.id); err != nil {
		t.Fatal(err)
	}
	second, err := store.claim(ctx, o, policy)
	if err != nil || second != 2 {
		t.Fatalf("retry attempt: %d %v", second, err)
	}
	if err := store.complete(ctx, o, first, "delivered"); err == nil {
		t.Fatal("expired attempt completed a newer lease")
	}
	if err := store.complete(ctx, o, second, "delivered"); err != nil {
		t.Fatal(err)
	}
	requireEscalationState(t, ctx, store.db, o, false, false, true, 2)
}
