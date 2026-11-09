//go:build alerting_integration && linux

package alerting

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type feishuFixtureRoute struct {
	relayFixtureTenant
	hook, secret string
}

func newFeishuFixtureRoute(t *testing.T, root, id string) feishuFixtureRoute {
	t.Helper()
	config := tenantConfig{
		ID: id, Receiver: id + "-feishu",
		WebhookTokenFile:        filepath.Join(root, id+"-webhook"),
		FeishuHookFile:          filepath.Join(root, id+"-hook"),
		FeishuSigningSecretFile: filepath.Join(root, id+"-signing"),
	}
	return feishuFixtureRoute{
		relayFixtureTenant{config: config, webhookKey: relayFixtureSecret(t, config.WebhookTokenFile)},
		relayFixtureSecret(t, config.FeishuHookFile), relayFixtureSecret(t, config.FeishuSigningSecretFile),
	}
}

func persistFeishuFixture(directory, key, message string) error {
	f, err := os.OpenFile(filepath.Join(directory, key), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.WriteString(f, message)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func TestFeishuRelayRetriesOneDurableTenantDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	tenantA, tenantB := newFeishuFixtureRoute(t, root, "tenant-a"), newFeishuFixtureRoute(t, root, "tenant-b")
	for _, tnt := range []feishuFixtureRoute{tenantA, tenantB} {
		if err := os.Mkdir(filepath.Join(root, tnt.config.ID), 0700); err != nil {
			t.Fatal(err)
		}
	}
	routes := map[string]feishuFixtureRoute{tenantA.hook: tenantA, tenantB.hook: tenantB}
	var mu sync.Mutex
	attempts := map[string]int{}
	keyFormat := regexp.MustCompile(`^ownsolid-am-v1-[a-f0-9]{64}$`)
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hook, ok := strings.CutPrefix(req.URL.Path, feishuHookPath)
		route, found := routes[hook]
		if !ok || !found || req.URL.RawQuery != "" {
			http.NotFound(w, req)
			return
		}
		if req.Method == http.MethodOptions {
			w.Header().Set(relayCapabilityHeader, relayCapabilityVersion)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		key, err := strconv.Unquote(req.Header.Get(idempotencyKeyHeader))
		var message feishuMessage
		if req.Method != http.MethodPost || err != nil || !keyFormat.MatchString(key) ||
			json.NewDecoder(http.MaxBytesReader(w, req.Body, maxFeishuRequestBytes)).Decode(&message) != nil || message.MsgType != "text" {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		stamp, err := strconv.ParseInt(message.Timestamp, 10, 64)
		signature, decodeErr := base64.StdEncoding.DecodeString(message.Sign)
		expected := hmac.New(sha256.New, fmt.Appendf(nil, "%d\n%s", stamp, route.secret))
		if err != nil || decodeErr != nil || time.Since(time.Unix(stamp, 0)).Abs() > time.Minute ||
			!hmac.Equal(signature, expected.Sum(nil)) || !strings.Contains(message.Content.Text, " | "+route.config.ID+" | ") {
			http.Error(w, "signature or scope rejected", http.StatusForbidden)
			return
		}

		mu.Lock()
		defer mu.Unlock()
		attempts[key]++
		directory := filepath.Join(root, route.config.ID)
		path := filepath.Join(directory, key)
		previous, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			err = persistFeishuFixture(directory, key, message.Content.Text)
			// The fixture commits a real file before dropping the acknowledgement.
			if err == nil && route.config.ID == "tenant-a" && strings.Contains(message.Content.Text, "ownsolid firing") {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		} else if err == nil && string(previous) != message.Content.Text {
			http.Error(w, "conflicting replay", http.StatusConflict)
			return
		}
		if err != nil {
			t.Errorf("durable fixture storage failed: %v", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set(idempotencyKeyHeader, strconv.Quote(key))
		io.WriteString(w, `{"code":0}`)
	}))
	t.Cleanup(site.Close)
	siteCA := filepath.Join(root, "feishu-ca.pem")
	writeFixture(t, siteCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: site.Certificate().Raw}))
	cert, key, tlsClient := relayFixtureCertificate(t, root)
	listen := relayFixtureAddress(t)
	ingress := "https://" + listen
	configFile := filepath.Join(root, "feishu.json")
	writeFixture(t, configFile, fixtureRelayJSON(t, relayConfig{
		ListenAddress: listen, CertificateFile: cert, PrivateKeyFile: key, FeishuOrigin: site.URL,
		FeishuCAFile: siteCA, DeliverySeconds: 3, MaxConcurrent: 2,
		Tenants: []tenantConfig{tenantA.config, tenantB.config},
	}))
	relayFixtureProcess(t, "OWNSOLID_TEST_FEISHU_RELAY", nil, "-config", configFile)
	relayFixtureWait(t, ctx, func() bool {
		status, _ := relayFixtureRequest(ctx, tlsClient, http.MethodPost, ingress+webhookPath+"tenant-b", tenantA.webhookKey, nil, nil)
		return status == http.StatusUnauthorized
	})
	client := &http.Client{Transport: &http.Transport{}, Timeout: 3 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	manager := relayFixtureAlertmanager(t, ctx, client, root, ingress, tenantA.relayFixtureTenant, tenantB.relayFixtureTenant)
	now := time.Now()
	silence := fixtureRelayJSON(t, map[string]any{
		"matchers": []map[string]any{
			{"name": "tenant_id", "value": "tenant-a", "isRegex": false, "isEqual": true},
			{"name": "alertname", "value": "Maintenance", "isRegex": false, "isEqual": true},
		},
		"startsAt": now.Add(-time.Minute), "endsAt": now.Add(time.Hour),
		"createdBy": "fixture", "comment": "Only tenant A is silenced",
	})
	if err := alertRequest(ctx, client, http.MethodPost, manager+"/api/v2/silences", "application/json", silence, nil); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	create := func(tenant, name string) fixtureSubmittedAlert {
		return fixtureSubmittedAlert{
			Labels:   map[string]string{"tenant_id": tenant, "alertname": name, "severity": "critical"},
			StartsAt: start, EndsAt: start.Add(time.Hour),
		}
	}
	publish := func(alerts ...fixtureSubmittedAlert) {
		if err := alertRequest(ctx, client, http.MethodPost, manager+"/api/v2/alerts", "application/json", fixtureRelayJSON(t, alerts), nil); err != nil {
			t.Fatal(err)
		}
	}
	alert := create("tenant-a", "NodeUnreachable")
	publish(alert, create("tenant-a", "Maintenance"), create("tenant-b", "Maintenance"))

	check := func(tenant string, want int, required ...string) []string {
		t.Helper()
		var keys []string
		relayFixtureWait(t, ctx, func() bool {
			mu.Lock()
			defer mu.Unlock()
			dir := filepath.Join(root, tenant)
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) > want {
				t.Fatalf("unexpected delivery inventory for %s: %v %v", tenant, entries, err)
			}
			if len(entries) != want {
				return false
			}
			keys = keys[:0]
			var joined strings.Builder
			for _, entry := range entries {
				raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
				if err != nil {
					t.Fatal(err)
				}
				joined.Write(raw)
				keys = append(keys, entry.Name())
			}
			for _, text := range required {
				if !strings.Contains(joined.String(), text) {
					t.Fatalf("unexpected mapping for %s: %s", tenant, joined.String())
				}
			}
			return true
		})
		return keys
	}
	first := check("tenant-a", 1, "ownsolid firing", "NodeUnreachable", "severity=critical")[0]
	check("tenant-b", 1, "ownsolid firing", "Maintenance")
	relayFixtureWait(t, ctx, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts[first] >= 2
	})
	alert.EndsAt = time.Now().Add(-time.Second)
	publish(alert)
	check("tenant-a", 2, "ownsolid resolved", "NodeUnreachable")
	check("tenant-b", 1, "Maintenance")
	t.Logf("Alertmanager %s, Feishu relay %s and controlled HTTPS receiver %s", manager, ingress, site.URL)
}
