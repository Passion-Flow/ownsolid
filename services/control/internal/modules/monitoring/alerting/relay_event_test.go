package alerting

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestWebhookMappingKeepsOccurrenceIdentity(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	makeAlert := func(fingerprint, node string) relayAlert {
		return relayAlert{"firing", map[string]string{
			"tenant_id": "tenant-a", "alertname": "NodeUnreachable", "node_id": node,
			"site_id": "site-a", "severity": "warning",
		}, start, fingerprint}
	}
	labels := map[string]string{"tenant_id": "tenant-a"}
	batch := relayWebhook{
		Version: "4", GroupKey: "group-a", Status: "firing", Receiver: "tenant-a-matrix",
		GroupLabels: labels, CommonLabels: labels,
		Alerts: []relayAlert{makeAlert("0000000000000002", "node-2"), makeAlert("0000000000000001", "node-1")},
	}
	route := tenantRoute{receiver: "tenant-a-matrix"}
	first, firingID, result := mapWebhook("tenant-a", route, batch)
	if result != delivered || !strings.Contains(first.Body, "2 firing") || first.MsgType != "m.text" {
		t.Fatalf("unexpected mapping: %v %s", result, first.Body)
	}
	slices.Reverse(batch.Alerts)
	batch.Alerts[0].StartsAt = start.In(time.FixedZone("other", 8*3600))
	again, againID, result := mapWebhook("tenant-a", route, batch)
	if result != delivered || againID != firingID || again != first {
		t.Fatal("alert order or timezone changed the delivery")
	}
	for i := range batch.Alerts {
		batch.Alerts[i].Status = "resolved"
	}
	batch.Status = "resolved"
	_, resolvedID, result := mapWebhook("tenant-a", route, batch)
	if result != delivered || resolvedID == firingID {
		t.Fatal("resolution reused firing identity")
	}

	for i := range batch.Alerts {
		batch.Alerts[i].Status = "firing"
		batch.Alerts[i].StartsAt = start.Add(time.Minute)
	}
	batch.Status = "firing"
	_, recurrenceID, result := mapWebhook("tenant-a", route, batch)
	if result != delivered || recurrenceID == firingID {
		t.Fatal("recurrence reused first firing identity")
	}
	batch.Alerts[0].Labels["tenant_id"] = "tenant-b"
	if _, _, result := mapWebhook("tenant-a", route, batch); result != scopeRejected {
		t.Fatalf("mixed tenant accepted: %s", result)
	}
	batch.Alerts[0].Labels["tenant_id"] = "tenant-a"
	batch.TruncatedAlerts = 1
	if _, _, result := mapWebhook("tenant-a", route, batch); result != invalidWebhook {
		t.Fatalf("incomplete batch accepted: %s", result)
	}
}

func TestRelayAcceptsOnlyExplicitPrivateAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1:443", "10.2.3.4:443", "[fd00::1]:443"} {
		if !privateAddress(ip) {
			t.Errorf("private address refused: %s", ip)
		}
	}
	for _, ip := range []string{"0.0.0.0:443", "8.8.8.8:443", "localhost:443", "127.0.0.1:0", "[fe80::1%lo]:443", "100.64.0.1:443"} {
		if privateAddress(ip) {
			t.Errorf("unapproved address accepted: %s", ip)
		}
	}
}
