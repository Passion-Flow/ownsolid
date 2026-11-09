//go:build gateway_integration

package distribution_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	gatewaywire "github.com/Passion-Flow/ownsolid/api/gateway/v1"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/licensing"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/google/uuid"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type gatewayChainMode int

const (
	sharedHTTPS gatewayChainMode = iota
	dedicatedHTTPS
	licenseExpiry
	foregroundResponses
	foregroundMessages
)

// Actual Go/Rust/PostgreSQL transport and persistence; backend replies are explicitly synthetic.
func TestRealGoRustPostgresGatewayChainAndControlOutage(t *testing.T) {
	root := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	for _, mode := range []string{"shared-https", "dedicated-machine-https"} {
		t.Run(mode, func(t *testing.T) {
			output := filepath.Join(root, mode)
			if e := os.Mkdir(output, 0700); e != nil {
				t.Fatal(e)
			}
			t.Setenv("PROJECT_ONE_EVIDENCE_ROOT", output)
			chain := sharedHTTPS
			if mode == "dedicated-machine-https" {
				chain = dedicatedHTTPS
			}
			realGatewayChain(t, chain)
		})
	}
}

func TestM19RealGoRustLicenseExpiryAndAcceptedOperation(t *testing.T) {
	realGatewayChain(t, licenseExpiry)
}

func TestRealGoRustPostgresForegroundResponses(t *testing.T) {
	realGatewayChain(t, foregroundResponses)
}

func TestRealGoRustPostgresForegroundMessages(t *testing.T) {
	realGatewayChain(t, foregroundMessages)
}

func realGatewayChain(t *testing.T, mode gatewayChainMode) {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_GATEWAY_BINARY")
	if binary == "" {
		t.Fatal("explicit compiled native gateway binary required for gateway_integration profile")
	}
	if resolved, e := filepath.EvalSymlinks(binary); e != nil || resolved != binary {
		t.Fatal("canonical native binary required")
	}
	c := setup(t)
	ctx := context.Background()
	control := httptest.NewUnstartedServer(nil)
	caKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Project-one isolated gateway test CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, e := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	leafKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Isolated gateway loopback"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	leafDER, e := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	if e != nil {
		t.Fatal(e)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	authority := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	privateDER, e := x509.MarshalPKCS8PrivateKey(leafKey)
	if e != nil {
		t.Fatal(e)
	}
	private := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	pair, e := tls.X509KeyPair(certificate, private)
	if e != nil {
		t.Fatal(e)
	}
	control.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	controlOrigin := "https://" + control.Listener.Addr().String()
	machine := mode == dedicatedHTTPS
	licensePath := mode == licenseExpiry
	c.s.Identity.Config.Server.AdminOrigin = controlOrigin
	if machine {
		material := t.TempDir()
		certPath, keyPath := filepath.Join(material, "certificate.pem"), filepath.Join(material, "key.pem")
		for name, data := range map[string][]byte{certPath: certificate, keyPath: private} {
			if e := os.WriteFile(name, data, 0600); e != nil {
				t.Fatal(e)
			}
		}
		c.s.Identity.Config.Server.TransportMode = config.TransportHTTP
		c.s.Identity.Config.Server.ListenAddress = "127.0.0.1:18081"
		c.s.Identity.Config.Server.AdminOrigin = "http://127.0.0.1:18081"
		c.s.Identity.Config.Server.PortalOrigin = "http://localhost:18081"
		c.s.Identity.Config.MachineServer = &config.MachineServerConfiguration{ListenAddress: control.Listener.Addr().String(), Origin: controlOrigin, TLSCertificateFile: certPath, TLSPrivateKeyFile: keyPath}
	}
	access := c.access(t)
	var storedOrigin string
	if e := c.s.DB.QueryRow(ctx, "SELECT control_origin_sha256 FROM gateway_instances WHERE id=$1", access.gateway.Gateway.ID).Scan(&storedOrigin); e != nil || storedOrigin != fmt.Sprintf("%x", sha256.Sum256([]byte(controlOrigin))) {
		t.Fatal("gateway bound the wrong transport origin", e)
	}
	handler := identityhttp.New(c.s.Identity)
	control.Config.Handler = handler
	if machine {
		control.Config.Handler = handler.MachineHandler()
	}
	var licenseOffset atomic.Int64
	if licensePath {
		trust, e := c.s.Identity.Config.LicenseTrust()
		if e != nil {
			t.Fatal(e)
		}
		licensed, e := licensing.New(c.s.Identity, trust, c.s.Identity.Config.LicensePolicy(), func() time.Time { return time.Now().UTC().Add(time.Duration(licenseOffset.Load())) })
		if e != nil {
			t.Fatal(e)
		}
		c.s.Identity.BusinessAdmission = licensed
	}
	ownedSecrets := []string{*access.gateway.Secret, *access.key.Secret}
	control.StartTLS()
	defer control.Close()
	var generations, backendCalls atomic.Int32
	var paused atomic.Bool
	resume := make(chan struct{}, 1)
	defer close(resume)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("upstream received authority secrets")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/models" {
			json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{map[string]any{"object": "model", "id": agentwire.InferenceModelName}}})
			return
		}
		var body map[string]any
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			w.WriteHeader(400)
			return
		}
		if body["model"] != agentwire.InferenceModelName {
			t.Error("backend model alias was not normalized")
		}
		if r.URL.Path == "/tokenize" {
			json.NewEncoder(w).Encode(map[string]any{"count": 7, "tokens": []int{1, 2, 3, 4, 5, 6, 7}, "max_model_len": 2048})
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			w.WriteHeader(404)
			return
		}
		generations.Add(1)
		usage := map[string]any{"prompt_tokens": 7, "completion_tokens": 2, "total_tokens": 9}
		if body["stream"] == false {
			json.NewEncoder(w).Encode(map[string]any{"id": "chatcmpl-synthetic", "object": "chat.completion", "created": 1, "model": agentwire.InferenceModelName, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "Synthetic local completion \u4e2d\u6587"}, "finish_reason": "stop"}}, "usage": usage, "private_diagnostic": "must not forward"})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		event := func(choices any, usage any) {
			b, _ := json.Marshal(map[string]any{"id": "chatcmpl-synthetic", "object": "chat.completion.chunk", "created": 1, "model": agentwire.InferenceModelName, "choices": choices, "usage": usage})
			w.Write(append(append([]byte("data: "), b...), []byte("\n\n")...))
			flusher.Flush()
		}
		event([]any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Synthetic streaming \u4e2d\u6587"}, "finish_reason": nil}}, nil)
		if paused.Load() {
			select {
			case <-resume:
			case <-r.Context().Done():
				return
			}
		}
		event([]any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, nil)
		event([]any{}, usage)
		w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer backend.Close()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	state := filepath.Join(root, "state")
	if e := os.Mkdir(state, 0700); e != nil {
		t.Fatal(e)
	}
	for name, data := range map[string][]byte{"ca.pem": authority, "certificate.pem": certificate, "key.pem": private, "credential": []byte(*access.gateway.Secret + "\n")} {
		if e = os.WriteFile(filepath.Join(root, name), data, 0600); e != nil {
			t.Fatal(e)
		}
	}
	listener := httptest.NewUnstartedServer(nil)
	listen := listener.Listener.Addr().String()
	listener.Close()
	configuration := map[string]any{"gateway_id": access.gateway.Gateway.ID, "node_id": c.node.NodeID, "listen_address": listen, "backend_address": strings.TrimPrefix(backend.URL, "http://"), "control_origin": control.URL, "control_ca_file": filepath.Join(root, "ca.pem"), "gateway_credential_file": filepath.Join(root, "credential"), "tls_certificate_file": filepath.Join(root, "certificate.pem"), "tls_private_key_file": filepath.Join(root, "key.pem"), "state_directory": state, "limits": map[string]any{"active_requests": 2, "queued_requests": 2, "backend_idle_connections": 2, "connect_seconds": 1, "control_seconds": 1, "request_seconds": 5, "idle_seconds": 2, "header_seconds": 1, "shutdown_seconds": 2, "outcome_retry_seconds": 1}}
	configBytes, _ := json.Marshal(configuration)
	configPath := filepath.Join(root, "gateway.json")
	if e = os.WriteFile(configPath, configBytes, 0600); e != nil {
		t.Fatal(e)
	}
	logPath := filepath.Join(root, "gateway.log")
	log, e := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer log.Close()
	command := exec.Command(binary, "serve", "--config", configPath)
	command.Stdout = log
	command.Stderr = log
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	defer func() {
		command.Process.Signal(syscall.SIGTERM)
		select {
		case e := <-exited:
			if e != nil {
				t.Error("native gateway exit", e)
			}
		case <-time.After(8 * time.Second):
			command.Process.Kill()
			<-exited
			t.Error("native gateway shutdown timeout")
		}
		if output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); output != "" {
			data, e := os.ReadFile(logPath)
			if e != nil {
				t.Error(e)
				return
			}
			for _, secret := range ownedSecrets {
				data = bytes.ReplaceAll(data, []byte(secret), []byte("[redacted-synthetic-secret]"))
			}
			if e = os.WriteFile(filepath.Join(output, "native-gateway.log"), data, 0600); e != nil {
				t.Error(e)
			}
		}
	}()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(authority) {
		t.Fatal("private CA")
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}, MaxIdleConnsPerHost: 1, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
	origin := "https://" + listen
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, e := client.Get(origin + "/health/ready")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("native gateway startup timeout", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	request := func(path, key string, body any) *http.Response {
		t.Helper()
		var reader io.Reader
		method := http.MethodGet
		if body != nil {
			method = http.MethodPost
			b, e := json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
			reader = bytes.NewReader(b)
		}
		r, e := http.NewRequest(method, origin+path, reader)
		if e != nil {
			t.Fatal(e)
		}
		if path == "/v1/messages" {
			r.Header.Set("Anthropic-Version", "2026-10-21")
			r.Header.Set("X-Api-Key", key)
		} else {
			r.Header.Set("Authorization", "Bearer "+key)
		}
		if body != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		response, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	if mode == foregroundResponses || mode == foregroundMessages {
		verifyForegroundText(t, mode, c, access, request, &paused, &backendCalls, &generations)
		return
	}
	chat := func(stream bool) map[string]any {
		body := map[string]any{"model": access.alias.Name, "messages": []any{map[string]any{"role": "user", "content": "Synthetic coupled request \u4e2d\u6587"}}, "max_tokens": 16, "stream": stream}
		if stream {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
		return body
	}
	response := request("/v1/models", *access.key.Secret, nil)
	modelBytes, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || !bytes.Contains(modelBytes, []byte(access.alias.Name)) || bytes.Contains(modelBytes, []byte(agentwire.InferenceModelName)) {
		t.Fatal("actual coupled models", response.StatusCode, string(modelBytes))
	}
	response = request("/v1/chat/completions", *access.key.Secret, chat(false))
	var completion map[string]any
	e = json.NewDecoder(response.Body).Decode(&completion)
	response.Body.Close()
	if e != nil || response.StatusCode != 200 || completion["model"] != access.alias.Name || completion["private_diagnostic"] != nil {
		t.Fatal("actual coupled completion", response.StatusCode, e)
	}
	response = request("/v1/chat/completions", *access.key.Secret, chat(true))
	streamBytes, e := io.ReadAll(response.Body)
	response.Body.Close()
	if e != nil || response.StatusCode != 200 || !bytes.HasSuffix(streamBytes, []byte("data: [DONE]\n\n")) || !bytes.Contains(streamBytes, []byte(`"total_tokens":9`)) {
		t.Fatal("actual coupled SSE", response.StatusCode, e, string(streamBytes))
	}
	deadline = time.Now().Add(4 * time.Second)
	for {
		var count int
		if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests WHERE state='reported' AND NOT capacity_held").Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("real outcome delivery timeout")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if licensePath {
		paused.Store(true)
		heldStream := request("/v1/chat/completions", *access.key.Secret, chat(true))
		defer heldStream.Body.Close()
		heldReader := bufio.NewReader(heldStream.Body)
		first, e := heldReader.ReadString('\n')
		if e != nil || heldStream.StatusCode != 200 || !strings.HasPrefix(first, "data: ") {
			t.Fatal("actual licensed inflight stream", e)
		}
		licenseOffset.Store(int64(26 * time.Hour))
		callsBefore, generationsBefore := backendCalls.Load(), generations.Load()
		refused := request("/v1/chat/completions", *access.key.Secret, chat(false))
		denial, e := io.ReadAll(io.LimitReader(refused.Body, 16385))
		refused.Body.Close()
		if e != nil || refused.StatusCode != 403 || !bytes.Contains(denial, []byte(`"code":"license_unavailable"`)) || backendCalls.Load() != callsBefore || generations.Load() != generationsBefore {
			t.Fatal("actual expired license reached backend or wrong public denial", refused.StatusCode, e)
		}
		var requests int
		if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests").Scan(&requests); e != nil || requests != 3 {
			t.Fatal("license denial inserted/held a request", e, requests)
		}
		discovery := request("/v1/models", *access.key.Secret, nil)
		discovered, e := io.ReadAll(io.LimitReader(discovery.Body, 16385))
		discovery.Body.Close()
		if e != nil || discovery.StatusCode != 200 || !bytes.Contains(discovered, []byte(access.alias.Name)) {
			t.Fatal("expired scoped discovery", e)
		}
		resume <- struct{}{}
		rest, e := io.ReadAll(heldReader)
		heldStream.Body.Close()
		if e != nil || !bytes.HasSuffix(rest, []byte("data: [DONE]\n\n")) {
			t.Fatal("existing stream could not finish after license expiry", e)
		}
		paused.Store(false)
		waitReported := func(want int) {
			t.Helper()
			end := time.Now().Add(4 * time.Second)
			for {
				var count int
				if e := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests WHERE state='reported' AND NOT capacity_held").Scan(&count); e != nil {
					t.Fatal(e)
				}
				if count == want {
					return
				}
				if time.Now().After(end) {
					t.Fatal("actual expiry outcome timeout", want, count)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		waitReported(3)
		var operationID string
		e = c.s.WithTransientKey(ctx, access.owner, keyInput(access.alias), 2*time.Second, uuid.NewString(), func(raw string) error {
			ownedSecrets = append(ownedSecrets, raw)
			operationID = strings.Split(raw, ".")[1]
			response := request("/v1/chat/completions", raw, chat(false))
			var answer map[string]any
			err := json.NewDecoder(response.Body).Decode(&answer)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 || answer["model"] != access.alias.Name {
				t.Fatal("real private accepted operation failed after expiry", response.StatusCode, err)
			}
			before := backendCalls.Load()
			again := request("/v1/chat/completions", raw, chat(false))
			repeat, e := io.ReadAll(io.LimitReader(again.Body, 16385))
			again.Body.Close()
			if e != nil || again.StatusCode != 401 || !bytes.Contains(repeat, []byte("invalid_api_key")) || backendCalls.Load() != before {
				t.Fatal("private operation replay reached backend", again.StatusCode, e)
			}
			waitReported(4)
			return nil
		})
		if e != nil {
			t.Fatal("actual expired operation cleanup", e)
		}
		var revoked bool
		if e = c.s.DB.QueryRow(ctx, "SELECT revoked_at IS NOT NULL FROM api_keys WHERE id=$1", operationID).Scan(&revoked); e != nil || !revoked {
			t.Fatal("private operation key remained active", e)
		}
		if generations.Load() != 4 {
			t.Fatal("unexpected license generation effects", generations.Load())
		}
		if output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); output != "" {
			summary, _ := json.MarshalIndent(map[string]any{"status": "passed_actual_Go_Rust_PostgreSQL_license_expiry_and_accepted_operation", "license_clock": "explicit_test_only_atomic_offset", "model_and_tokenizer": "explicit_synthetic_HTTP_fixture_not_AI_hardware_execution", "actual_native_Rust": true, "actual_Go_license_HTTP_and_SQL": true, "ordinary_expired_public_status": 403, "denied_backend_tokenizer_and_completion_calls": 0, "denied_durable_rows": 0, "reported_without_held_capacity": 4, "private_operation_replay_public_status": 401, "private_operation_revoked": true, "generations": 4, "qualification": "No production native control process, backup-restore, vLLM/NVIDIA/amd64/Xinchuang/cloud qualification."}, "", "  ")
			if e = os.WriteFile(filepath.Join(output, "go-rust-license-chain.json"), summary, 0600); e != nil {
				t.Fatal(e)
			}
		}
		return
	}
	if _, e = c.s.RevokeKey(ctx, access.owner, access.alias.WorkspaceID, access.key.Key.ID, "Synthetic coupled revoke", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	response = request("/v1/models", *access.key.Secret, nil)
	denied, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 401 || !bytes.Contains(denied, []byte("invalid_api_key")) || generations.Load() != 2 {
		t.Fatal("real Go revocation did not reach Rust", response.StatusCode, string(denied))
	}
	replacement, e := c.s.MintKey(ctx, access.owner, keyInput(access.alias), uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	ownedSecrets = append(ownedSecrets, *replacement.Secret)
	paused.Store(true)
	response = request("/v1/chat/completions", *replacement.Secret, chat(true))
	reader := bufio.NewReader(response.Body)
	first, e := reader.ReadString('\n')
	if e != nil || !strings.HasPrefix(first, "data: ") {
		t.Fatal("in-flight admission", e)
	}
	control.Close()
	resume <- struct{}{}
	rest, e := io.ReadAll(reader)
	response.Body.Close()
	if e != nil || !bytes.HasSuffix(rest, []byte("data: [DONE]\n\n")) {
		t.Fatal("bounded admitted stream could not finish during real control outage", e, string(rest))
	}
	response = request("/v1/chat/completions", *replacement.Secret, chat(false))
	failure, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 503 || !bytes.Contains(failure, []byte("gateway_unavailable")) || generations.Load() != 3 {
		t.Fatal("control outage admitted new backend effects", response.StatusCode, string(failure))
	}
	pending, e := os.ReadDir(filepath.Join(state, "outcomes"))
	if e != nil || len(pending) != 1 {
		t.Fatal("outage did not preserve exact pending metadata", e, len(pending))
	}
	metadata, e := os.ReadFile(filepath.Join(state, "outcomes", pending[0].Name()))
	if e != nil || bytes.Contains(metadata, []byte("Synthetic coupled request")) || bytes.Contains(metadata, []byte(*replacement.Secret)) {
		t.Fatal("invalid private metadata", e)
	}
	var held int
	if e = c.s.DB.QueryRow(ctx, "SELECT count(*) FROM gateway_requests WHERE capacity_held AND state='admitted'").Scan(&held); e != nil || held != 1 {
		t.Fatal("outage falsely acknowledged capacity", e, held)
	}
	if output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"); output != "" {
		summary, _ := json.MarshalIndent(map[string]any{"status": "passed_real_go_rust_postgresql_chain", "scope": "Actual native gateway binary/private TLS/Go/PostgreSQL. Model/tokenizer HTTP replies are explicitly synthetic; no GPU or vLLM API execution.", "generations": generations.Load(), "reported_before_outage": 2, "pending_metadata_during_outage": 1, "assertions": []string{"models/completion/SSE preserve scoped aliases", "canonical usage receipt matches across Go/Rust", "fresh Go revocation reaches Rust", "actual Go server outage denies new generations", "already admitted stream completes after durable local metadata", "unknown Go capacity and private pending metadata remain"}}, "", "  ")
		if e = os.WriteFile(filepath.Join(output, "go-rust-chain.json"), summary, 0600); e != nil {
			t.Fatal(e)
		}
	}
}

func verifyForegroundText(
	t *testing.T,
	mode gatewayChainMode,
	c scenario,
	access access,
	request func(string, string, any) *http.Response,
	paused *atomic.Bool,
	backendCalls, generations *atomic.Int32,
) {
	t.Helper()
	path, prefix, delta, terminal := "/v1/responses", "resp_", "response.output_text.delta", "response.completed"
	body := func(stream bool) map[string]any {
		return map[string]any{"model": access.alias.Name, "input": "Synthetic foreground input", "max_output_tokens": 16, "stream": stream, "store": false}
	}
	if mode == foregroundMessages {
		path, prefix, delta, terminal = "/v1/messages", "msg_", "content_block_delta", "message_stop"
		body = func(stream bool) map[string]any {
			return map[string]any{"model": access.alias.Name, "max_tokens": 16, "stream": stream,
				"system":   "Synthetic system instructions",
				"messages": []any{map[string]any{"role": "user", "content": "Synthetic foreground input"}}}
		}
	}
	key := *access.key.Secret
	assertReported := func(id, status string, stream bool, usage *gatewaywire.Usage) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		for {
			var data []byte
			var held, streamed bool
			var workspace, keyID string
			err := c.s.DB.QueryRow(ctx, "SELECT outcome, capacity_held, stream, workspace_id, api_key_id FROM gateway_requests WHERE id=$1", id).Scan(&data, &held, &streamed, &workspace, &keyID)
			if err != nil {
				t.Fatal(err)
			}
			if data != nil {
				receipt, err := gatewaywire.DecodeOutcome(data)
				if err != nil || receipt.RequestID != id || receipt.Status != status || held || streamed != stream ||
					workspace != access.alias.WorkspaceID || keyID != access.key.Key.ID ||
					(usage == nil && receipt.Usage != nil) ||
					(usage != nil && (receipt.Usage == nil || *receipt.Usage != *usage)) {
					t.Fatal("foreground text receipt mismatch", receipt, err)
				}
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("foreground text outcome was not reported")
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	usage := &gatewaywire.Usage{PromptTokens: 7, CompletionTokens: 2, TotalTokens: 9}
	answer := request(path, key, body(false))
	id := answer.Header.Get("X-Request-Id")
	var value struct {
		ID, Model, Status, Type, Role string
		StopReason                    string `json:"stop_reason"`
		Usage                         struct {
			Input  int `json:"input_tokens"`
			Output int `json:"output_tokens"`
			Total  int `json:"total_tokens"`
		}
	}
	err := json.NewDecoder(answer.Body).Decode(&value)
	answer.Body.Close()
	if err != nil || answer.StatusCode != 200 || !gatewaywire.ValidID(id) || value.ID != prefix+id ||
		value.Model != access.alias.Name || value.Usage.Input != 7 || value.Usage.Output != 2 {
		t.Fatal("foreground text JSON failed", answer.StatusCode, err)
	}
	if mode == foregroundMessages {
		if value.Type != "message" || value.Role != "assistant" || value.StopReason != "end_turn" ||
			answer.Header.Get("Request-Id") != "req_"+id {
			t.Fatal("Messages JSON shape did not match the local protocol")
		}
	} else if value.Status != "completed" || value.Usage.Total != 9 {
		t.Fatal("Responses JSON terminal or usage did not match")
	}
	assertReported(id, "succeeded", false, usage)
	stream := request(path, key, body(true))
	events, err := io.ReadAll(stream.Body)
	stream.Body.Close()
	if err != nil || stream.StatusCode != 200 ||
		!bytes.Contains(events, []byte("event: "+delta+"\n")) ||
		!bytes.Contains(events, []byte("event: "+terminal+"\n")) {
		t.Fatal("foreground text SSE failed", stream.StatusCode, err)
	}
	assertReported(stream.Header.Get("X-Request-Id"), "succeeded", true, usage)

	paused.Store(true)
	cancelled := request(path, key, body(true))
	defer cancelled.Body.Close()
	reader := bufio.NewReader(cancelled.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal("foreground text delta missing", err)
		}
		if line == "event: "+delta+"\n" {
			break
		}
	}
	cancelled.Body.Close()
	assertReported(cancelled.Header.Get("X-Request-Id"), "client_cancelled", true, nil)
	if _, err := c.s.RevokeKey(context.Background(), access.owner, access.alias.WorkspaceID, access.key.Key.ID, "Synthetic foreground text revocation", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	before := backendCalls.Load()
	denied := request(path, key, body(false))
	denied.Body.Close()
	if denied.StatusCode != 401 || backendCalls.Load() != before || generations.Load() != 3 {
		t.Fatal("revoked key reached the text backend", denied.StatusCode)
	}
}
