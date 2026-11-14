//go:build alerting_integration && linux

package alerting

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func fixtureRelayJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func relayFixtureAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func relayFixtureProcess(t *testing.T, env string, variables []string, args ...string) {
	t.Helper()
	binary := os.Getenv(env)
	if !filepath.IsAbs(binary) {
		t.Fatalf("%s must name a pinned absolute binary path", env)
	}
	file, err := os.CreateTemp(t.TempDir(), "process-")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = append([]string{"GOMAXPROCS=2"}, variables...)
	cmd.Stdout, cmd.Stderr = file, file
	err = cmd.Start()
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("%s did not exit cleanly: %v", env, err)
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Errorf("%s shutdown timed out", env)
		}
		if t.Failed() {
			log, _ := os.ReadFile(file.Name())
			t.Logf("%s log:\n%s", env, log)
		}
	})
}

func relayFixtureSecret(t *testing.T, path string) string {
	t.Helper()
	var raw [32]byte
	rand.Read(raw[:])
	token := hex.EncodeToString(raw[:])
	writeFixture(t, path, []byte(token+"\n"))
	return token
}

func relayFixtureRequest(ctx context.Context, client *http.Client, method, endpoint, token string, raw []byte, result any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if result != nil && resp.StatusCode == http.StatusOK {
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result)
	} else {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	}
	return resp.StatusCode, err
}

func relayFixtureWait(t *testing.T, ctx context.Context, probe func() bool) {
	t.Helper()
	for {
		if probe() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("fixture condition timed out: %v", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func relayFixtureMatrix(t *testing.T, ctx context.Context, client *http.Client, root string) (string, string) {
	t.Helper()
	addr := relayFixtureAddress(t)
	secretPath := filepath.Join(root, "registration-secret")
	secret := relayFixtureSecret(t, secretPath)
	conf := filepath.Join(root, "matrix.toml")
	writeFixture(t, conf, fmt.Appendf(nil, `[global]
server_name = "relay.test"
address = ["127.0.0.1"]
port = %s
database_path = %q
allow_federation = false
trusted_servers = []
allow_encryption = false
db_cache_capacity_mb = 32.0
rocksdb_parallelism_threads = 2
registration_shared_secret_file = %q
`, strings.TrimPrefix(addr, "127.0.0.1:"), filepath.Join(root, "matrix-data"), secretPath))
	relayFixtureProcess(t, "OWNSOLID_TEST_TUWUNEL", []string{"TUWUNEL_CONFIG=" + conf})
	origin := "http://" + addr
	ready, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	relayFixtureWait(t, ready, func() bool {
		code, _ := relayFixtureRequest(ready, client, http.MethodGet, origin+"/_matrix/client/versions", "", nil, nil)
		return code == http.StatusOK
	})
	return origin, secret
}

type relayFixtureTenant struct {
	config     tenantConfig
	webhookKey string
	matrixKey  string
}

func relayFixtureRoom(t *testing.T, ctx context.Context, client *http.Client, origin, secret, root, tenant string) relayFixtureTenant {
	t.Helper()
	type registrationResult struct {
		Nonce       string `json:"nonce"`
		AccessToken string `json:"access_token"`
	}
	register := origin + "/_synapse/admin/v1/register"
	var receipt registrationResult
	if code, err := relayFixtureRequest(ctx, client, http.MethodGet, register, "", nil, &receipt); err != nil || code != http.StatusOK || receipt.Nonce == "" {
		t.Fatalf("registration nonce: %d %v", code, err)
	}
	var raw [32]byte
	rand.Read(raw[:])
	password := hex.EncodeToString(raw[:])
	mac := hmac.New(sha1.New, []byte(secret))
	io.WriteString(mac, strings.Join([]string{receipt.Nonce, tenant, password, "notadmin"}, "\x00"))
	data := fixtureRelayJSON(t, map[string]any{
		"nonce": receipt.Nonce, "username": tenant, "password": password,
		"admin": false, "mac": hex.EncodeToString(mac.Sum(nil)),
	})
	if code, err := relayFixtureRequest(ctx, client, http.MethodPost, register, "", data, &receipt); err != nil || code != http.StatusOK || receipt.AccessToken == "" {
		t.Fatalf("synthetic account provisioning: %d %v", code, err)
	}
	var room struct {
		ID string `json:"room_id"`
	}
	data = fixtureRelayJSON(t, map[string]any{
		"preset": "private_chat", "creation_content": map[string]bool{"m.federate": false},
	})
	if code, err := relayFixtureRequest(ctx, client, http.MethodPost, origin+"/_matrix/client/v3/createRoom", receipt.AccessToken, data, &room); err != nil || code != http.StatusOK || room.ID == "" {
		t.Fatalf("synthetic private room provisioning: %d %v", code, err)
	}
	config := tenantConfig{
		ID: tenant, Receiver: tenant + "-matrix", RoomID: room.ID,
		WebhookTokenFile: filepath.Join(root, tenant+"-webhook"), MatrixTokenFile: filepath.Join(root, tenant+"-matrix"),
	}
	webhookKey := relayFixtureSecret(t, config.WebhookTokenFile)
	writeFixture(t, config.MatrixTokenFile, []byte(receipt.AccessToken+"\n"))
	return relayFixtureTenant{config, webhookKey, receipt.AccessToken}
}

type fixtureMatrixSend struct {
	path, event string
}

type matrixFixtureTransport struct{ http.RoundTripper }

func (t matrixFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.RoundTripper.RoundTrip(req)
	if err == nil {
		res.Request = req
	}
	return res, err
}

func relayFixtureProxy(t *testing.T, matrix, faultRoom, root string) (*httptest.Server, string, func() []fixtureMatrixSend) {
	t.Helper()
	upstream, err := url.Parse(matrix)
	if err != nil {
		t.Fatal(err)
	}
	var lock sync.Mutex
	var sends []fixtureMatrixSend
	var dropped bool
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	transport := &http.Transport{}
	t.Cleanup(transport.CloseIdleConnections)
	proxy.Transport = matrixFixtureTransport{transport}
	proxy.ModifyResponse = func(response *http.Response) error {
		request := response.Request
		if request.Method != http.MethodPut || !strings.Contains(request.URL.Path, "/send/m.room.message/") || response.StatusCode != http.StatusOK {
			return nil
		}
		raw, err := io.ReadAll(io.LimitReader(response.Body, 8192))
		response.Body.Close()
		if err != nil {
			return err
		}
		var receipt struct {
			EventID string `json:"event_id"`
		}
		if err := json.Unmarshal(raw, &receipt); err != nil || receipt.EventID == "" {
			return errors.New("invalid fixture event receipt")
		}
		response.Body = io.NopCloser(bytes.NewReader(raw))
		lock.Lock()
		defer lock.Unlock()
		sends = append(sends, fixtureMatrixSend{request.URL.Path, receipt.EventID})
		if !dropped && strings.Contains(request.URL.Path, "/rooms/"+faultRoom+"/") {
			dropped = true
			return errors.New("fixture lost the committed Matrix response")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		w.WriteHeader(http.StatusBadGateway)
	}
	server := httptest.NewTLSServer(proxy)
	t.Cleanup(server.Close)
	ca := filepath.Join(root, "matrix-ca.pem")
	writeFixture(t, ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	return server, ca, func() []fixtureMatrixSend {
		lock.Lock()
		defer lock.Unlock()
		return append([]fixtureMatrixSend(nil), sends...)
	}
}

func relayFixtureCertificate(t *testing.T, root string) (string, string, *http.Client) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Relay fixture"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certFile, keyFile := filepath.Join(root, "relay-ca.pem"), filepath.Join(root, "relay-key.pem")
	writeFixture(t, certFile, cert)
	writeFixture(t, keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(cert) {
		t.Fatal("fixture CA did not parse")
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}, Timeout: 3 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	return certFile, keyFile, client
}

type matrixFixtureEvent struct {
	Type    string        `json:"type"`
	ID      string        `json:"event_id"`
	Content matrixMessage `json:"content"`
}

func relayFixtureRoomEvents(ctx context.Context, client *http.Client, origin string, room relayFixtureTenant, token string) ([]matrixFixtureEvent, int, error) {
	var page struct {
		Chunk []matrixFixtureEvent `json:"chunk"`
	}
	endpoint := origin + "/_matrix/client/v3/rooms/" + url.PathEscape(room.config.RoomID) + "/messages?dir=b&limit=100"
	status, err := relayFixtureRequest(ctx, client, http.MethodGet, endpoint, token, nil, &page)
	var messages []matrixFixtureEvent
	for _, event := range page.Chunk {
		if event.Type == "m.room.message" {
			messages = append(messages, event)
		}
	}
	return messages, status, err
}

func relayFixtureAlertmanager(t *testing.T, ctx context.Context, client *http.Client, root, ingress string, tenants ...relayFixtureTenant) string {
	t.Helper()
	raw, err := os.ReadFile("alertmanager-matrix.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	substitutions := []string{
		"/etc/ownsolid/relay/", root + "/",
		"group_wait: 15s", "group_wait: 2s",
		"group_interval: 1m", "group_interval: 2s",
	}
	for _, tenant := range tenants {
		path := filepath.Join(root, tenant.config.ID+"-url")
		writeFixture(t, path, []byte(ingress+webhookPath+tenant.config.ID+"\n"))
		substitutions = append(substitutions, tenant.config.ID+"-matrix", tenant.config.Receiver)
	}
	rules := strings.NewReplacer(substitutions...).Replace(string(raw))
	configFile := filepath.Join(root, "alertmanager.yml")
	writeFixture(t, configFile, []byte(rules))
	return runAlertService(t, ctx, client, "OWNSOLID_TEST_ALERTMANAGER", "/-/ready",
		"--config.file="+configFile, "--storage.path="+filepath.Join(root, "alertmanager"), "--cluster.listen-address=")
}

type fixtureSubmittedAlert struct {
	Labels   map[string]string `json:"labels"`
	StartsAt time.Time         `json:"startsAt"`
	EndsAt   time.Time         `json:"endsAt"`
}

func TestMatrixRelayDeliversOneAuthenticatedTenantGroup(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	root := t.TempDir()
	client := &http.Client{Transport: &http.Transport{}, Timeout: 3 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	matrix, secret := relayFixtureMatrix(t, ctx, client, root)
	tenantA := relayFixtureRoom(t, ctx, client, matrix, secret, root, "tenant-a")
	tenantB := relayFixtureRoom(t, ctx, client, matrix, secret, root, "tenant-b")
	front, matrixCA, observed := relayFixtureProxy(t, matrix, tenantA.config.RoomID, root)
	certificate, key, tlsClient := relayFixtureCertificate(t, root)

	address := relayFixtureAddress(t)
	ingress := "https://" + address
	filename := filepath.Join(root, "relay.json")
	conf := relayConfig{
		ListenAddress: address, CertificateFile: certificate, PrivateKeyFile: key,
		MatrixOrigin: front.URL, MatrixCAFile: matrixCA, DeliverySeconds: 3, MaxConcurrent: 2,
		Tenants: []tenantConfig{tenantA.config, tenantB.config},
	}
	writeFixture(t, filename, fixtureRelayJSON(t, conf))
	relayFixtureProcess(t, "OWNSOLID_TEST_MATRIX_RELAY", nil, "-config", filename)
	relayFixtureWait(t, ctx, func() bool {
		code, _ := relayFixtureRequest(ctx, tlsClient, http.MethodPost, ingress+webhookPath+"tenant-a", "", nil, nil)
		return code == http.StatusUnauthorized
	})

	// Even an authenticated sender cannot select a different tenant by using
	// another route or inserting mismatched labels into an otherwise valid batch.
	now := time.Now().UTC()
	labels := map[string]string{"tenant_id": "tenant-a", "alertname": "ForgeryProbe"}
	forgery := relayWebhook{
		Version: "4", GroupKey: "test", Status: "firing", Receiver: tenantA.config.Receiver,
		GroupLabels: labels, CommonLabels: labels,
		Alerts: []relayAlert{{Status: "firing", Labels: map[string]string{"tenant_id": "tenant-b", "alertname": "ForgeryProbe"}, StartsAt: now, Fingerprint: "0123456789abcdef"}},
	}
	raw := fixtureRelayJSON(t, forgery)
	for _, attempt := range []struct {
		path, token string
		status      int
	}{
		{"tenant-b", tenantA.webhookKey, http.StatusUnauthorized},
		{"tenant-a", tenantA.webhookKey, http.StatusForbidden},
	} {
		code, err := relayFixtureRequest(ctx, tlsClient, http.MethodPost, ingress+webhookPath+attempt.path, attempt.token, raw, nil)
		if err != nil || code != attempt.status || len(observed()) != 0 {
			t.Fatalf("scope/auth refusal: %d %v", code, err)
		}
	}

	manager := relayFixtureAlertmanager(t, ctx, client, root, ingress, tenantA, tenantB)
	silence := fixtureRelayJSON(t, map[string]any{
		"matchers": []map[string]any{
			{"name": "tenant_id", "value": "tenant-a", "isRegex": false, "isEqual": true},
			{"name": "alertname", "value": "MaintenanceProbe", "isRegex": false, "isEqual": true},
		},
		"startsAt": now.Add(-time.Minute), "endsAt": now.Add(time.Hour),
		"createdBy": "fixture", "comment": "Tenant A maintenance",
	})
	if err := alertRequest(ctx, client, http.MethodPost, manager+"/api/v2/silences", "application/json", silence, nil); err != nil {
		t.Fatal(err)
	}

	// A fresh firing timestamp makes Alertmanager apply group_wait before
	// flushing. Backdated alerts are eligible for immediate delivery.
	start := time.Now().UTC()
	var rack []fixtureSubmittedAlert
	for i := range 40 {
		rack = append(rack, fixtureSubmittedAlert{map[string]string{
			"tenant_id": "tenant-a", "alertname": "RackNodeUnreachable", "node_id": fmt.Sprint(i),
			"site_id": "site-a", "cell_id": "cell-a", "rack_id": "rack-1", "severity": "warning",
		}, start, start.Add(time.Hour)})
	}
	publish := func(alerts []fixtureSubmittedAlert) {
		t.Helper()
		if err := alertRequest(ctx, client, http.MethodPost, manager+"/api/v2/alerts", "application/json", fixtureRelayJSON(t, alerts), nil); err != nil {
			t.Fatal(err)
		}
	}
	input := append([]fixtureSubmittedAlert(nil), rack...)
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		input = append(input, fixtureSubmittedAlert{map[string]string{
			"tenant_id": tenant, "alertname": "MaintenanceProbe",
		}, start, start.Add(time.Hour)})
	}
	publish(input)

	var state []struct {
		Labels map[string]string
		Status struct {
			SilencedBy []string
		}
	}
	relayFixtureWait(t, ctx, func() bool {
		if err := alertRequest(ctx, client, http.MethodGet, manager+"/api/v2/alerts", "", nil, &state); err != nil {
			t.Fatal(err)
		}
		for _, alert := range state {
			if alert.Labels["tenant_id"] == "tenant-a" && alert.Labels["alertname"] == "MaintenanceProbe" {
				return len(alert.Status.SilencedBy) == 1
			}
		}
		return false
	})

	waitMessages := func(room relayFixtureTenant, wanted int) []matrixFixtureEvent {
		t.Helper()
		var events []matrixFixtureEvent
		relayFixtureWait(t, ctx, func() bool {
			var status int
			var err error
			events, status, err = relayFixtureRoomEvents(ctx, client, matrix, room, room.matrixKey)
			if err != nil || status != http.StatusOK {
				t.Fatalf("Matrix room query: %d %v", status, err)
			}
			if len(events) > wanted {
				t.Fatalf("unexpected room delivery count: %d, wanted %d", len(events), wanted)
			}
			return len(events) == wanted
		})
		return events
	}
	first := waitMessages(tenantA, 1)[0]
	if first.Content.MsgType != "m.text" || !strings.Contains(first.Content.Body, "40 firing") ||
		!strings.Contains(first.Content.Body, "+ 30 more alerts") || strings.Contains(first.Content.Body, "MaintenanceProbe") {
		t.Fatalf("unexpected first tenant group: %s", first.Content.Body)
	}
	other := waitMessages(tenantB, 1)[0]
	if !strings.Contains(other.Content.Body, "MaintenanceProbe") || strings.Contains(other.Content.Body, "RackNodeUnreachable") {
		t.Fatalf("unexpected second tenant group: %s", other.Content.Body)
	}
	relayFixtureWait(t, ctx, func() bool {
		var retries []fixtureMatrixSend
		for _, send := range observed() {
			if strings.Contains(send.path, "/rooms/"+tenantA.config.RoomID+"/") {
				retries = append(retries, send)
			}
		}
		if len(retries) < 2 {
			return false
		}
		if retries[0] != retries[1] || retries[0].event != first.ID {
			t.Fatalf("Matrix retransmission lost the original event identity: %+v", retries)
		}
		return true
	})
	_, status, err := relayFixtureRoomEvents(ctx, client, matrix, tenantA, tenantB.matrixKey)
	if err != nil || status != http.StatusForbidden {
		t.Fatalf("other tenant could read a private room: %d %v", status, err)
	}

	for i := range rack {
		rack[i].EndsAt = time.Now().Add(-time.Second)
	}
	publish(rack)
	resolved := waitMessages(tenantA, 2)[0]
	if resolved.ID == first.ID || !strings.Contains(resolved.Content.Body, "40 resolved") {
		t.Fatalf("unexpected resolution: %s", resolved.Content.Body)
	}
	restart := time.Now()
	for i := range rack {
		rack[i].StartsAt, rack[i].EndsAt = restart, restart.Add(time.Hour)
	}
	publish(rack)
	recurrence := waitMessages(tenantA, 3)[0]
	if recurrence.ID == first.ID || recurrence.ID == resolved.ID || !strings.Contains(recurrence.Content.Body, "40 firing") {
		t.Fatalf("new alert occurrence was lost: %s", recurrence.Content.Body)
	}
	waitMessages(tenantB, 1)
	t.Logf("Alertmanager %s, relay %s, Matrix %s via %s: one firing group after lost receipt, one resolution and one recurrence", manager, ingress, matrix, front.URL)
}
