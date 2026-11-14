package alerting

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

const maxWebhookAlerts = 256
const maxMessageDetails = 10

var alertFingerprint = regexp.MustCompile(`^[0-9a-f]{16}$`)

type relayWebhook struct {
	Version         string            `json:"version"`
	GroupKey        string            `json:"groupKey"`
	TruncatedAlerts int               `json:"truncatedAlerts"`
	Status          string            `json:"status"`
	Receiver        string            `json:"receiver"`
	GroupLabels     map[string]string `json:"groupLabels"`
	CommonLabels    map[string]string `json:"commonLabels"`
	Alerts          []relayAlert      `json:"alerts"`
}

type relayAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	StartsAt    time.Time         `json:"startsAt"`
	Fingerprint string            `json:"fingerprint"`
}

type alertIdentity struct {
	Fingerprint string
	StartsAt    time.Time
	Status      string
}

type matrixMessage struct {
	MsgType string `json:"msgtype"`
	Body    string `json:"body"`
}

func mapWebhook(tenant string, route tenantRoute, batch relayWebhook) (string, string, deliveryOutcome) {
	if batch.Receiver != route.receiver || batch.GroupLabels["tenant_id"] != tenant ||
		batch.CommonLabels["tenant_id"] != tenant {
		return "", "", scopeRejected
	}
	if batch.Version != "4" || len(batch.GroupKey) == 0 || len(batch.GroupKey) > 4096 ||
		strings.ContainsFunc(batch.GroupKey, unicode.IsControl) || batch.TruncatedAlerts != 0 ||
		len(batch.Alerts) == 0 || len(batch.Alerts) > maxWebhookAlerts {
		return "", "", invalidWebhook
	}

	alerts := append([]relayAlert(nil), batch.Alerts...)
	slices.SortFunc(alerts, func(a, b relayAlert) int { return strings.Compare(a.Fingerprint, b.Fingerprint) })
	identities := make([]alertIdentity, 0, len(alerts))
	firing := 0
	for i, alert := range alerts {
		if alert.Labels["tenant_id"] != tenant {
			return "", "", scopeRejected
		}
		if !alertFingerprint.MatchString(alert.Fingerprint) || i > 0 && alerts[i-1].Fingerprint == alert.Fingerprint ||
			alert.StartsAt.IsZero() || !relayLabel.MatchString(alert.Labels["alertname"]) {
			return "", "", invalidWebhook
		}
		switch alert.Status {
		case "firing":
			firing++
		case "resolved":
		default:
			return "", "", invalidWebhook
		}
		identities = append(identities, alertIdentity{alert.Fingerprint, alert.StartsAt.UTC(), alert.Status})
	}
	status := "resolved"
	if firing > 0 {
		status = "firing"
	}
	if batch.Status != status {
		return "", "", invalidWebhook
	}
	// Alertmanager refreshes firing deadlines and may change annotations on a
	// retry. Neither is part of the occurrence identity or the outbound text.
	canonical, err := json.Marshal(struct {
		Tenant, Receiver, Group, Status string
		Alerts                          []alertIdentity
	}{tenant, route.receiver, batch.GroupKey, status, identities})
	if err != nil {
		return "", "", invalidWebhook
	}
	digest := sha256.Sum256(canonical)
	transaction := "ownsolid-am-v1-" + hex.EncodeToString(digest[:])

	var body strings.Builder
	fmt.Fprintf(&body, "ownsolid %s | %s | %d firing, %d resolved", status, tenant, firing, len(alerts)-firing)
	for _, alert := range alerts[:min(len(alerts), maxMessageDetails)] {
		fmt.Fprintf(&body, "\n- %s: %s", alert.Status, alert.Labels["alertname"])
		for _, key := range []string{"severity", "site_id", "cell_id", "rack_id", "node_id"} {
			if value := alert.Labels[key]; relayLabel.MatchString(value) {
				fmt.Fprintf(&body, " %s=%s", key, value)
			}
		}
	}
	if remaining := len(alerts) - maxMessageDetails; remaining > 0 {
		fmt.Fprintf(&body, "\n+ %d more alerts", remaining)
	}
	return body.String(), transaction, delivered
}
