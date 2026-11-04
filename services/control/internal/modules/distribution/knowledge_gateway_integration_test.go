//go:build knowledge_gateway_integration

package distribution_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	agentwire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/distribution"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/knowledge"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

type completionBarrier struct {
	entered, release chan struct{}
	once             sync.Once
	finished         chan struct{}
	finishOnce       sync.Once
}

// Actual Go/PostgreSQL/pgvector/offline MiniLM/native Rust/real Qwen CPU generation.
// Deployment/node/GPU observations come from the existing explicit synthetic
// machine fixture; they establish no vLLM, NVIDIA or other physical qualification.
func TestM12RealCPUModelRustGatewayCitedAnswerAndCurrentSourceMembershipCancellationFences(t *testing.T) {
	runRealCPUModelGateway(t, false)
}
func TestM13RealCPUModelRustGatewayDurableWorkflowsAndCurrentAuthority(t *testing.T) {
	runRealCPUModelGateway(t, true)
}

func runRealCPUModelGateway(t *testing.T, workflowMode bool) {
	// Distinct declared workloads: M12 RAG regressions and M13 durable workflows.
	budget := 150 * time.Second
	if workflowMode {
		budget = 300*time.Second + 2*config.Seconds(config.DefaultRuntimePolicy().Rates.CSRFWindowSeconds)
	}
	ctx, cancelTest := context.WithTimeout(context.Background(), budget)
	defer cancelTest()
	binary := os.Getenv("PROJECT_ONE_GATEWAY_BINARY")
	backendAddress := os.Getenv("PROJECT_ONE_CAUSAL_BACKEND_ADDRESS")
	address, e := net.ResolveTCPAddr("tcp4", backendAddress)
	if binary == "" || e != nil || address == nil || !address.IP.Equal(net.ParseIP("127.0.0.1")) || address.Port == 0 {
		t.Fatal("explicit actual native Rust and CPU backend required")
	}
	c := setup(t)
	deployment := c.readyDeployment(t)

	root := t.TempDir()
	if os.Chmod(root, 0700) != nil {
		t.Fatal("private root")
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	cert, caFile, certFile, keyFile := smtpsink.Certificate(t, root)
	control := httptest.NewUnstartedServer(nil)
	control.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	c.s.Identity.Config.Server.AdminOrigin = "https://" + control.Listener.Addr().String()
	reserve := httptest.NewUnstartedServer(nil)
	listen := reserve.Listener.Addr().String()
	reserve.Close()
	origin := "https://" + listen
	input := c.input()
	input.ClientOrigin = origin
	gateway, e := c.s.Provision(ctx, c.a, input, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	alias, e := c.s.Publish(ctx, c.a, distribution.PublishRequest{WorkspaceID: c.a.Me.Workspace.ID, GatewayID: gateway.Gateway.ID, Name: "knowledge-cpu", DeploymentID: deployment.ID, DeploymentRevision: deployment.Revision, Reason: "Actual local CPU answer integration with synthetic machine observation"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	scratch := filepath.Join(root, "encoder-scratch")
	if os.Mkdir(scratch, 0700) != nil {
		t.Fatal("scratch")
	}
	c.s.Identity.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch, Gateways: []config.KnowledgeGateway{{Origin: origin, DialAddresses: []string{"127.0.0.1"}, CAFile: caFile}}}
	policy := c.s.Identity.Config.Policy()
	policy.Knowledge.OutputTokens = 96
	policy.HTTP.RequestSeconds = 90
	policy.HTTP.StartupSeconds = 60
	policy.HTTP.ShutdownSeconds = 20
	c.s.Identity.Config.Runtime = &policy
	if os.Getenv("PROJECT_ONE_RAG_BROWSER") == "1" {
		_, port, _ := net.SplitHostPort(control.Listener.Addr().String())
		c.s.Identity.Config.Server.PortalOrigin = "https://localhost:" + port
		c.s.Identity.Config.Server.PortalAssets = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "web/apps/portal/dist")
		c.s.Identity.Config.Server.AdminAssets = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "web/apps/console/dist")
	}
	handler := identityhttp.New(c.s.Identity)
	if e = handler.Knowledge.Service.OpenConfiguredProcessor(ctx); e != nil {
		t.Fatal(e)
	}
	defer handler.Knowledge.Service.Processor.Close()
	control.Config.Handler = handler
	control.StartTLS()
	defer control.Close()
	// Maintain the existing synthetic machine fixture through the actual heartbeat
	// service. The product's ordinary30-second freshness boundary is unchanged.
	heartbeatCtx, stopHeartbeats := context.WithCancel(ctx)
	heartbeatsDone := make(chan struct{})
	go func() {
		defer close(heartbeatsDone)
		ticker := time.NewTicker(config.Seconds(c.s.Identity.Config.Policy().Nodes.OnlineSeconds / 3))
		defer ticker.Stop()
		for {
			observed, err := c.s.Deployments.Get(heartbeatCtx, c.a, "", deployment.ID)
			if err != nil {
				if heartbeatCtx.Err() == nil {
					t.Error("synthetic machine observation read", err)
				}
				return
			}
			if observed.Observation == nil {
				t.Error("synthetic deployment observation missing")
				return
			}
			state := *observed.Observation
			now := time.Now().UTC()
			if state.Verification != nil {
				verification := *state.Verification
				verification.ObservedAt = now
				state.Verification = &verification
			}
			h := c.handshake
			h.Facts.ObservedAt = now
			h.Inference = &agentwire.InferenceObservation{GPUs: []agentwire.NVIDIAGPU{{UUID: state.GPUUUID, Name: "Synthetic GPU", DriverVersion: "580.1", MemoryTotalMiB: 16384, MemoryFreeMiB: 16384, ComputeCapability: 8, MIGMode: "Disabled", ObservedAt: now}}, Deployment: &state, ObservedAt: now}
			if _, err = c.s.Nodes.Heartbeat(heartbeatCtx, c.node.NodeID, c.node.Credential, h); err != nil {
				if heartbeatCtx.Err() == nil {
					t.Error("synthetic current heartbeat", err)
				}
				return
			}
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { stopHeartbeats(); <-heartbeatsDone }()

	target, _ := url.Parse("http://" + backendAddress)
	forward := httputil.NewSingleHostReverseProxy(target)
	forward.Transport = &http.Transport{Proxy: nil, DisableCompression: true, MaxConnsPerHost: 1}
	defer forward.Transport.(*http.Transport).CloseIdleConnections()
	var barrier atomic.Pointer[completionBarrier]
	var workflowFault atomic.Int32
	var modelCalls atomic.Int64
	forward.ModifyResponse = func(response *http.Response) error {
		if response.Request.URL.Path != "/v1/chat/completions" || !workflowFault.CompareAndSwap(2, 0) {
			return nil
		}
		// Explicit test-only corruption of an actual CPU response exercises citation
		// rejection. This response must never become a successful product answer.
		raw, e := io.ReadAll(io.LimitReader(response.Body, 1048577))
		response.Body.Close()
		if e != nil {
			return e
		}
		var value map[string]any
		if e = json.Unmarshal(raw, &value); e != nil {
			return e
		}
		choices, ok := value["choices"].([]any)
		if !ok || len(choices) != 1 {
			return fmt.Errorf("SYNTHETIC_CPU_RESPONSE_INVALID")
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			return fmt.Errorf("SYNTHETIC_CPU_RESPONSE_INVALID")
		}
		message, ok := choice["message"].(map[string]any)
		if !ok {
			return fmt.Errorf("SYNTHETIC_CPU_RESPONSE_INVALID")
		}
		message["content"] = "Synthetic corrupted citation [S999]"
		raw, e = json.Marshal(value)
		if e != nil {
			return e
		}
		response.Body = io.NopCloser(strings.NewReader(string(raw)))
		response.ContentLength = int64(len(raw))
		response.Header.Set("Content-Length", fmt.Sprint(len(raw)))
		return nil
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		modelCalls.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("model backend received delegated credentials")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			if workflowFault.CompareAndSwap(1, 0) {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if held := barrier.Load(); held != nil {
				if held.finished != nil {
					defer held.finishOnce.Do(func() { close(held.finished) })
				}
				held.once.Do(func() { close(held.entered) })
				select {
				case <-held.release:
				case <-r.Context().Done():
					return
				}
			}
		}
		forward.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	state := filepath.Join(root, "gateway-state")
	if os.Mkdir(state, 0700) != nil {
		t.Fatal("gateway state")
	}
	credential := filepath.Join(root, "credential")
	if os.WriteFile(credential, []byte(*gateway.Secret+"\n"), 0600) != nil {
		t.Fatal("credential")
	}
	// A distinct longer test gateway idle budget lets the five-second workflow
	// deadline be observed as the actual first cause. M12 keeps its existing budget.
	backendIdleSeconds := 5
	if workflowMode {
		backendIdleSeconds = 15
	}
	configuration := map[string]any{"gateway_id": gateway.Gateway.ID, "node_id": c.node.NodeID, "listen_address": listen, "backend_address": strings.TrimPrefix(proxy.URL, "http://"), "control_origin": control.URL, "control_ca_file": caFile, "gateway_credential_file": credential, "tls_certificate_file": certFile, "tls_private_key_file": keyFile, "state_directory": state, "limits": map[string]any{"runtime_threads": 1, "blocking_threads": 2, "active_requests": 1, "queued_requests": 1, "backend_idle_connections": 1, "connect_seconds": 2, "control_seconds": 5, "request_seconds": 60, "idle_seconds": backendIdleSeconds, "header_seconds": 3, "shutdown_seconds": 5, "outcome_retry_seconds": 1, "maximum_context_tokens": 2048, "maximum_output_tokens": 96, "default_output_tokens": 96}}
	raw, _ := json.Marshal(configuration)
	configurationFile := filepath.Join(root, "gateway.json")
	if os.WriteFile(configurationFile, raw, 0600) != nil {
		t.Fatal("gateway config")
	}
	logFile, e := os.OpenFile(filepath.Join(root, "gateway.log"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer logFile.Close()
	command := exec.Command(binary, "serve", "--config", configurationFile)
	command.Stdout = logFile
	command.Stderr = logFile
	if e = command.Start(); e != nil {
		t.Fatal(e)
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	defer func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		select {
		case e := <-exited:
			if e != nil {
				t.Error("Rust shutdown", e)
			}
		case <-time.After(8 * time.Second):
			_ = command.Process.Kill()
			<-exited
			t.Error("Rust shutdown deadline")
		}
	}()
	roots := x509.NewCertPool()
	authority, e := os.ReadFile(caFile)
	if e != nil || !roots.AppendCertsFromPEM(authority) {
		t.Fatal("private CA")
	}
	clientTransport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer clientTransport.CloseIdleConnections()
	client := &http.Client{Transport: clientTransport, Timeout: time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, e := client.Get(origin + "/health/ready")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual Rust startup", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	service := handler.Knowledge.Service
	workspace := c.a.Me.Workspace.ID
	base, e := service.CreateBase(ctx, c.p, knowledge.CreateBase{WorkspaceID: workspace, Name: "Actual cited facts", Reason: "Actual local RAG integration"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	upload := func(name string) knowledge.Document {
		text := "The project launch city is Lisbon."
		sum := sha256.Sum256([]byte(text))
		doc, e := service.Upload(ctx, c.p, workspace, base.ID, knowledge.UploadDocument{Name: name, Content: text, SHA256: hex.EncodeToString(sum[:]), Reason: "Actual CPU original"}, uuid.NewString(), uuid.NewString())
		if e != nil {
			t.Fatal(e)
		}
		if worked, e := service.ProcessNext(ctx); e != nil || !worked {
			t.Fatal("actual MiniLM ingestion", e)
		}
		ready, e := service.GetDocument(ctx, c.p, workspace, base.ID, doc.ID)
		if e != nil || ready.VisibleSource == nil {
			t.Fatal("actual vector ready", e)
		}
		return ready
	}
	doc := upload("facts.txt")
	if workflowMode {
		verifyActualWorkflows(t, ctx, c, handler, base, alias, doc, &barrier, &workflowFault, control, cert, caFile, root, func() { stopHeartbeats(); <-heartbeatsDone }, &modelCalls)
		return
	}
	question := knowledge.AnswerRequest{Question: "Which city does the project launch in? Answer in one short sentence with a source citation.", AliasID: alias.ID, TopK: 1, MinimumScore: 0.1}
	answer, e := service.Answer(ctx, c.p, workspace, base.ID, question, uuid.NewString())
	if e != nil || !strings.Contains(answer.Answer, "Lisbon") || !strings.Contains(answer.Answer, "[S1]") || len(answer.Sources) != 1 || answer.Sources[0].SourceID != doc.VisibleSource.ID || answer.Usage.PromptTokens < 1 || answer.Usage.CompletionTokens < 1 {
		t.Fatal("actual Rust/CPU cited answer", e)
	}
	assertNoLiveKeys := func() {
		t.Helper()
		var live int
		if e := c.s.DB.QueryRow(ctx, "SELECT count(*) FROM api_keys WHERE revoked_at IS NULL AND expires_at>clock_timestamp()").Scan(&live); e != nil || live != 0 {
			t.Fatal("transient key authority leaked", e)
		}
	}
	assertNoLiveKeys()
	if os.Getenv("PROJECT_ONE_RAG_BROWSER") == "1" {
		output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
		if output == "" {
			t.Fatal("browser evidence root required")
		}
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		spki := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
		payload, _ := json.Marshal(map[string]any{"admin": control.URL, "portal": c.s.Identity.Config.Server.PortalOrigin, "admin_email": "admin@example.invalid", "admin_password": c.password, "alias_id": alias.ID, "source_id": doc.VisibleSource.ID, "workspace_id": workspace, "output": output, "spki": base64.StdEncoding.EncodeToString(spki[:])})
		fixtureFile := filepath.Join(root, "answer-browser.json")
		if err = os.WriteFile(fixtureFile, payload, 0600); err != nil {
			t.Fatal(err)
		}
		browserCtx, cancelBrowser := context.WithTimeout(ctx, 90*time.Second)
		browserCommand := exec.CommandContext(browserCtx, "node", filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "tests/acceptance/knowledge/knowledge-answer.mjs"), fixtureFile)
		browserCommand.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile)
		browserCommand.Stdout = os.Stdout
		browserCommand.Stderr = os.Stderr
		browserError := browserCommand.Run()
		cancelBrowser()
		if browserError != nil {
			t.Fatal("actual dual React Rust CPU answer browser", browserError)
		}
		result, err := os.ReadFile(filepath.Join(output, "answer-browser.json"))
		if err != nil {
			t.Fatal(err)
		}
		var evidence struct {
			Status  string   `json:"status"`
			Steps   []string `json:"steps"`
			Locales []string `json:"locales"`
		}
		if json.Unmarshal(result, &evidence) != nil || evidence.Status != "passed_actual_dual_react_go_rust_cpu_cited_answers" || len(evidence.Steps) != 4 || len(evidence.Locales) != 2 || evidence.Locales[0] != "en" || evidence.Locales[1] != "zh-CN" {
			t.Fatal("actual answer browser receipt")
		}
		assertNoLiveKeys()
	}
	// An ordinary-key-free no-match failure must not synthesize an answer.
	noMatch := question
	noMatch.MinimumScore = 1
	_, e = service.Answer(ctx, c.p, workspace, base.ID, noMatch, uuid.NewString())
	check(t, e, "KNOWLEDGE_NO_CONTEXT")
	assertNoLiveKeys()
	held := &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	barrier.Store(held)
	done := make(chan error, 1)
	go func() { _, e := service.Answer(ctx, c.p, workspace, base.ID, question, uuid.NewString()); done <- e }()
	select {
	case <-held.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("source race did not reach actual backend")
	}
	if _, e = service.Delete(ctx, c.p, workspace, base.ID, doc.ID, knowledge.RevisionReason{ExpectedRevision: doc.Revision, Reason: "Actual source deletion during real model request"}, uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	close(held.release)
	check(t, <-done, "KNOWLEDGE_ANSWER_SOURCE_CHANGED")
	barrier.Store(nil)
	assertNoLiveKeys()
	_ = upload("current.txt")
	member := c.user(t, "rag-member@example.invalid")
	held = &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	barrier.Store(held)
	go func() { _, e := service.Answer(ctx, member, workspace, base.ID, question, uuid.NewString()); done <- e }()
	select {
	case <-held.entered:
	case early := <-done:
		t.Fatal("membership request failed before actual backend", early)
	case <-time.After(30 * time.Second):
		t.Fatal("membership race did not reach actual backend")
	}
	var membershipVersion int64
	if e = c.s.DB.QueryRow(ctx, "SELECT version FROM workspace_memberships WHERE workspace_id=$1 AND user_id=$2", workspace, member.Me.User.ID).Scan(&membershipVersion); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Workspaces.ChangeMember(ctx, c.a, workspace, member.Me.User.ID, "", membershipVersion, "Actual membership removal during generation", uuid.NewString(), true); e != nil {
		t.Fatal(e)
	}
	if _, e = c.s.Workspaces.AddMember(ctx, c.a, workspace, "rag-member@example.invalid", "member", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	close(held.release)
	check(t, <-done, "KNOWLEDGE_AUTHORITY_INVALID")
	barrier.Store(nil)
	assertNoLiveKeys()
	held = &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	barrier.Store(held)
	requestCtx, cancel := context.WithCancel(ctx)
	go func() {
		_, e := service.Answer(requestCtx, c.p, workspace, base.ID, question, uuid.NewString())
		done <- e
	}()
	select {
	case <-held.entered:
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("cancellation did not reach actual backend")
	}
	cancel()
	check(t, <-done, "KNOWLEDGE_REQUEST_CANCELLED")
	close(held.release)
	barrier.Store(nil)
	assertNoLiveKeys()
	// Hold an admitted completion request before CPU forwarding, revoke its alias, then
	// prove the final Go transaction refuses the response and cleans its key.
	held = &completionBarrier{entered: make(chan struct{}), release: make(chan struct{})}
	barrier.Store(held)
	go func() { _, e := service.Answer(ctx, c.p, workspace, base.ID, question, uuid.NewString()); done <- e }()
	select {
	case <-held.entered:
	case early := <-done:
		t.Fatal("alias request failed before actual backend", early)
	case <-time.After(30 * time.Second):
		t.Fatal("alias revocation race did not reach actual backend")
	}
	if _, e = c.s.DisableAlias(ctx, c.a, workspace, alias.ID, alias.Revision, "Actual alias disable during generated answer delivery", uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	close(held.release)
	check(t, <-done, "MODEL_ALIAS_UNAVAILABLE")
	barrier.Store(nil)
	assertNoLiveKeys()
	// The real Rust request ledger contains model usage/outcomes, never knowledge
	// source/question/answer text or ordinary-key plaintext.
	var metadata string
	if e = c.s.DB.QueryRow(ctx, "SELECT COALESCE(json_agg(row_to_json(r))::text,'[]') FROM gateway_requests r").Scan(&metadata); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(metadata, "Lisbon") || strings.Contains(metadata, question.Question) || strings.Contains(metadata, "pk1.") {
		t.Fatal("request history retained private payload")
	}
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("evidence root required")
	}
	receipt, _ := json.MarshalIndent(map[string]any{"status": "passed_actual_go_rust_pgvector_cpu_cited_answer", "usage": answer.Usage, "source_fence": true, "membership_replacement_fence": true, "current_alias_revocation_fence": true, "cancellation_and_key_cleanup": true, "all_transient_keys_revoked": true, "synthetic_machine_observation": true, "actual_cpu_generation": true, "vllm_or_gpu_qualification": false}, "", "  ")
	if e = os.WriteFile(filepath.Join(output, "actual-rag.json"), append(receipt, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
}
