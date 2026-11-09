//go:build alerting_candidate

package alerting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const candidateRetry deliveryOutcome = "RELAY_ESCALATION_DEFERRED"

type occurrenceScope struct{ tenant, workspace string }

type alertOccurrence struct {
	occurrenceScope
	id, fingerprint, name string
	started               time.Time
}

func webhookOccurrence(event relayDelivery) (alertOccurrence, bool) {
	if len(event.batch.Alerts) != 1 {
		return alertOccurrence{}, false
	}
	alert := event.batch.Alerts[0]
	workspace := alert.Labels["workspace_id"]
	if !relayLabel.MatchString(workspace) || event.batch.GroupLabels["workspace_id"] != workspace ||
		event.batch.CommonLabels["workspace_id"] != workspace || alert.StartsAt.Nanosecond()%1_000_000 != 0 {
		return alertOccurrence{}, false
	}
	canonical := event.tenant + "\x00" + workspace + "\x00" + alert.Fingerprint + "\x00" + strconv.FormatInt(alert.StartsAt.UnixMilli(), 10)
	digest := sha256.Sum256([]byte(canonical))
	return alertOccurrence{
		occurrenceScope{event.tenant, workspace}, hex.EncodeToString(digest[:]),
		alert.Fingerprint, alert.Labels["alertname"], alert.StartsAt.UTC(),
	}, true
}

func (o alertOccurrence) deliveryKey() string {
	return "ownsolid-am-escalation-v1-" + o.id
}

type escalationPolicy struct {
	delay, lease time.Duration
	attemptLimit int
}

type escalationGate struct {
	store  escalationStore
	relay  *webhookRelay
	source string
	client *http.Client
	policy escalationPolicy
}

func newEscalationGate(db *pgxpool.Pool, relay *webhookRelay, manager string, policy escalationPolicy) (*escalationGate, error) {
	source, err := url.Parse(manager)
	if err != nil || source.Scheme != "http" || source.User != nil || source.Path != "" ||
		source.RawQuery != "" || source.ForceQuery || source.Fragment != "" ||
		db == nil || relay == nil || policy.delay < time.Millisecond ||
		policy.lease <= relay.settings.timeout || policy.attemptLimit < 1 || policy.attemptLimit > 32 {
		return nil, errors.New("ESCALATION_CANDIDATE_CONFIG_INVALID")
	}
	addr, err := netip.ParseAddrPort(source.Host)
	if err != nil || !addr.Addr().IsLoopback() || addr.Port() == 0 {
		return nil, errors.New("ESCALATION_CANDIDATE_SOURCE_INVALID")
	}
	client := &http.Client{
		Transport: &http.Transport{},
		Timeout:   relay.settings.timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &escalationGate{escalationStore{db}, relay, source.String(), client, policy}, nil
}

func (g *escalationGate) observe(ctx context.Context, event relayDelivery) deliveryOutcome {
	o, ok := webhookOccurrence(event)
	if !ok {
		return invalidWebhook
	}
	if err := g.store.observe(ctx, o, event.batch.Status == "resolved", g.policy.delay); err != nil {
		return candidateRetry
	}
	return delivered
}

func (g *escalationGate) escalate(ctx context.Context, event relayDelivery) deliveryOutcome {
	o, ok := webhookOccurrence(event)
	if !ok {
		return invalidWebhook
	}
	if event.batch.Status == "resolved" {
		return g.observe(ctx, event)
	}
	ready, terminal, err := g.store.admission(ctx, o, g.policy.attemptLimit)
	if err != nil {
		return candidateRetry
	}
	if terminal {
		return delivered
	}
	if !ready || !g.live(ctx, o) {
		return candidateRetry
	}
	attempt, err := g.store.claim(ctx, o, g.policy)
	if err != nil {
		return candidateRetry
	}

	outcome := g.relay.send(ctx, event.route, event.text, o.deliveryKey())
	recorded := "retryable"
	switch outcome {
	case delivered:
		recorded = "delivered"
	case matrixRejected, feishuRejected, dingTalkRejected, weComRejected:
		recorded = "rejected"
	}
	if err := g.store.complete(ctx, o, attempt, recorded); err != nil {
		return candidateRetry
	}
	return outcome
}

func (g *escalationGate) live(ctx context.Context, o alertOccurrence) bool {
	params := url.Values{
		"filter": {"tenant_id=" + strconv.Quote(o.tenant), "workspace_id=" + strconv.Quote(o.workspace), "alertname=" + strconv.Quote(o.name)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.source+"/api/v2/alerts?"+params.Encode(), nil)
	if err != nil {
		return false
	}
	res, err := g.client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxWebhookBytes+1))
	var alerts []struct {
		Fingerprint string            `json:"fingerprint"`
		StartsAt    time.Time         `json:"startsAt"`
		EndsAt      time.Time         `json:"endsAt"`
		Labels      map[string]string `json:"labels"`
		Status      struct {
			State       string   `json:"state"`
			SilencedBy  []string `json:"silencedBy"`
			InhibitedBy []string `json:"inhibitedBy"`
		} `json:"status"`
	}
	if err != nil || len(raw) > maxWebhookBytes || json.Unmarshal(raw, &alerts) != nil {
		return false
	}
	for _, alert := range alerts {
		if alert.Fingerprint == o.fingerprint && alert.StartsAt.Equal(o.started) &&
			alert.Labels["tenant_id"] == o.tenant && alert.Labels["workspace_id"] == o.workspace &&
			alert.Labels["alertname"] == o.name {
			return alert.EndsAt.After(time.Now()) && alert.Status.State == "active" &&
				len(alert.Status.SilencedBy) == 0 && len(alert.Status.InhibitedBy) == 0
		}
	}
	return false
}
