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

type botFixtureRoute struct {
	relayFixtureTenant
	hook, secret string
}

func newBotFixtureRoute(t *testing.T, root, id, name string, channel relayChannel) botFixtureRoute {
	t.Helper()
	config := tenantConfig{
		ID: id, Receiver: id + "-" + name,
		WebhookTokenFile: filepath.Join(root, id+"-webhook"),
	}
	tokenPath := filepath.Join(root, id+"-hook")
	secretPath := filepath.Join(root, id+"-signing")
	var secret string
	switch channel {
	case dingTalkChannel:
		config.DingTalkAccessTokenFile, config.DingTalkSigningSecretFile = tokenPath, secretPath
		secret = relayFixtureSecret(t, secretPath)
	case weComChannel:
		config.WeComKeyFile = tokenPath
	case feishuChannel:
		config.FeishuHookFile, config.FeishuSigningSecretFile = tokenPath, secretPath
		secret = relayFixtureSecret(t, secretPath)
	}
	return botFixtureRoute{
		relayFixtureTenant{config: config, webhookKey: relayFixtureSecret(t, config.WebhookTokenFile)},
		relayFixtureSecret(t, tokenPath), secret,
	}
}

func persistBotFixture(directory, key, message string) error {
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
	testDurableBotDelivery(t, feishuChannel, "feishu", "OWNSOLID_TEST_FEISHU_RELAY", `{"code":0}`)
}

func TestDingTalkRelayRetriesOneDurableTenantDelivery(t *testing.T) {
	testDurableBotDelivery(t, dingTalkChannel, "dingtalk", "OWNSOLID_TEST_DINGTALK_RELAY", `{"errcode":0}`)
}

func TestWeComRelayRetriesOneDurableTenantDelivery(t *testing.T) {
	testDurableBotDelivery(t, weComChannel, "wecom", "OWNSOLID_TEST_WECOM_RELAY", `{"errcode":0}`)
}

func testDurableBotDelivery(t *testing.T, channel relayChannel, name, binary, receipt string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	root := t.TempDir()
	tenantA, tenantB := newBotFixtureRoute(t, root, "tenant-a", name, channel), newBotFixtureRoute(t, root, "tenant-b", name, channel)
	for _, tnt := range []botFixtureRoute{tenantA, tenantB} {
		if err := os.Mkdir(filepath.Join(root, tnt.config.ID), 0700); err != nil {
			t.Fatal(err)
		}
	}
	routes := map[string]botFixtureRoute{tenantA.hook: tenantA, tenantB.hook: tenantB}
	var mu sync.Mutex
	attempts := map[string]int{}
	acknowledged := map[string]bool{}
	keyFormat := regexp.MustCompile(`^ownsolid-am-v1-[a-f0-9]{64}$`)
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hook, validPath := strings.CutPrefix(req.URL.Path, "/open-apis/bot/v2/hook/")
		switch channel {
		case dingTalkChannel:
			validPath = req.URL.Path == "/robot/send"
			hook = req.URL.Query().Get("access_token")
		case weComChannel:
			validPath = req.URL.Path == "/cgi-bin/webhook/send"
			hook = req.URL.Query().Get("key")
		}
		route, found := routes[hook]
		if !validPath || (channel == feishuChannel && (!found || req.URL.RawQuery != "")) {
			http.NotFound(w, req)
			return
		}
		if req.Method == http.MethodOptions {
			if req.URL.RawQuery != "" {
				t.Error("capability probe carried query credentials")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set(relayCapabilityHeader, relayCapabilityVersion)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		key, err := strconv.Unquote(req.Header.Get(idempotencyKeyHeader))
		if req.Method != http.MethodPost || err != nil || !keyFormat.MatchString(key) || !found {
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		message, valid := readBotFixturePost(w, req, route.secret, channel)
		if !valid || !strings.Contains(message, " | "+route.config.ID+" | ") {
			http.Error(w, "signature, text or scope rejected", http.StatusForbidden)
			return
		}

		mu.Lock()
		defer mu.Unlock()
		attempts[key]++
		directory := filepath.Join(root, route.config.ID)
		path := filepath.Join(directory, key)
		previous, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			err = persistBotFixture(directory, key, message)
			// The fixture commits a real file before dropping the acknowledgement.
			if err == nil && route.config.ID == "tenant-a" && strings.Contains(message, "ownsolid firing") {
				if channel != feishuChannel {
					io.WriteString(w, receipt)
				} else {
					w.WriteHeader(http.StatusServiceUnavailable)
				}
				return
			}
		} else if err == nil && string(previous) != message {
			http.Error(w, "conflicting replay", http.StatusConflict)
			return
		}
		if err != nil {
			t.Errorf("durable fixture storage failed: %v", err)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		acknowledged[key] = true
		w.Header().Set(idempotencyKeyHeader, strconv.Quote(key))
		io.WriteString(w, receipt)
	}))
	t.Cleanup(site.Close)
	siteCA := filepath.Join(root, "site-ca.pem")
	writeFixture(t, siteCA, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: site.Certificate().Raw}))
	cert, key, tlsClient := relayFixtureCertificate(t, root)
	listen := relayFixtureAddress(t)
	ingress := "https://" + listen
	configFile := filepath.Join(root, name+".json")
	config := relayConfig{
		ListenAddress: listen, CertificateFile: cert, PrivateKeyFile: key,
		DeliverySeconds: 3, MaxConcurrent: 2,
		Tenants: []tenantConfig{tenantA.config, tenantB.config},
	}
	switch channel {
	case dingTalkChannel:
		config.DingTalkOrigin, config.DingTalkCAFile = site.URL, siteCA
	case weComChannel:
		config.WeComOrigin, config.WeComCAFile = site.URL, siteCA
	case feishuChannel:
		config.FeishuOrigin, config.FeishuCAFile = site.URL, siteCA
	}
	writeFixture(t, configFile, fixtureRelayJSON(t, config))
	relayFixtureProcess(t, binary, nil, "-config", configFile)

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
	alerts := []fixtureSubmittedAlert{create("tenant-a", "NodeUnreachable")}
	if channel == weComChannel {
		// Long, shared rack labels force clipping while keeping 40 distinct
		// alerts in one group. The notification must retain the group count.
		alerts = make([]fixtureSubmittedAlert, 40)
		for i := range alerts {
			alerts[i] = create("tenant-a", "NodeUnreachable")
			labels := alerts[i].Labels
			labels["site_id"] = strings.Repeat("s", 90)
			labels["cell_id"] = strings.Repeat("c", 90)
			labels["rack_id"] = strings.Repeat("r", 90)
			labels["node_id"] = fmt.Sprintf("node-%02d-%s", i, strings.Repeat("n", 80))
		}
	}
	publish(append(alerts, create("tenant-a", "Maintenance"), create("tenant-b", "Maintenance"))...)

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
	firing := []string{"ownsolid firing", "NodeUnreachable", "severity=critical"}
	resolved := []string{"ownsolid resolved", "NodeUnreachable"}
	if channel == weComChannel {
		firing = append(firing, "40 firing, 0 resolved", "[Alert details shortened]")
		resolved = append(resolved, "0 firing, 40 resolved", "[Alert details shortened]")
	}
	first := check("tenant-a", 1, firing...)[0]
	check("tenant-b", 1, "ownsolid firing", "Maintenance")
	relayFixtureWait(t, ctx, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts[first] >= 2 && acknowledged[first]
	})
	end := time.Now().Add(-time.Second)
	for i := range alerts {
		alerts[i].EndsAt = end
	}
	publish(alerts...)
	check("tenant-a", 2, resolved...)
	check("tenant-b", 1, "Maintenance")
	t.Logf("Alertmanager %s, %s relay %s and controlled HTTPS receiver %s", manager, name, ingress, site.URL)
}

func readBotFixturePost(w http.ResponseWriter, req *http.Request, secret string, channel relayChannel) (string, bool) {
	body := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16<<10))
	body.DisallowUnknownFields()
	var stamp, signature, text string
	var expected []byte
	var at time.Time
	if channel != feishuChannel {
		var message struct {
			Type string `json:"msgtype"`
			Text struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		if body.Decode(&message) != nil || message.Type != "text" {
			return "", false
		}
		q := req.URL.Query()
		if channel == weComChannel {
			return message.Text.Content, len(q) == 1 && len(q["key"]) == 1 && len(message.Text.Content) <= 2048
		}
		if len(q) != 3 || len(q["access_token"]) != 1 || len(q["timestamp"]) != 1 || len(q["sign"]) != 1 {
			return "", false
		}
		stamp, signature, text = q.Get("timestamp"), q.Get("sign"), message.Text.Content
		millis, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			return "", false
		}
		at = time.UnixMilli(millis)
		mac := hmac.New(sha256.New, []byte(secret))
		fmt.Fprintf(mac, "%d\n%s", millis, secret)
		expected = mac.Sum(nil)
	} else {
		var message feishuMessage
		if body.Decode(&message) != nil || message.MsgType != "text" {
			return "", false
		}
		stamp, signature, text = message.Timestamp, message.Sign, message.Content.Text
		seconds, err := strconv.ParseInt(stamp, 10, 64)
		if err != nil {
			return "", false
		}
		at = time.Unix(seconds, 0)
		mac := hmac.New(sha256.New, fmt.Appendf(nil, "%d\n%s", seconds, secret))
		expected = mac.Sum(nil)
	}
	decoded, err := base64.StdEncoding.DecodeString(signature)
	return text, err == nil && time.Since(at).Abs() <= time.Minute && hmac.Equal(decoded, expected)
}
