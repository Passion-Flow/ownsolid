//go:build knowledge_database_integration && knowledge_encoder_integration && knowledge_native_operations_integration

package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/training"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

type nativeKnowledgeProcess struct {
	testing              *testing.T
	ctx                  context.Context
	binary, path, origin string
	client               *http.Client
	configuration        config.Config
	command              *exec.Cmd
	exited               chan error
	log                  *os.File
}

func nativeKnowledge(t *testing.T, ctx context.Context, s *Service, databaseName, label string) *nativeKnowledgeProcess {
	t.Helper()
	binary := os.Getenv("PROJECT_ONE_CONTROL_BINARY")
	root := t.TempDir()
	if binary == "" || os.Chmod(root, 0700) != nil {
		t.Fatal("explicit current native control binary/private directory required")
	}
	write := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(root, name)
		if os.WriteFile(path, data, 0600) != nil {
			t.Fatal("private native configuration")
		}
		return path
	}
	address, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	listen := address.Addr().String()
	address.Close()
	_, port, _ := net.SplitHostPort(listen)
	_, caFile, certificate, key := smtpsink.Certificate(t, root)
	roots := x509.NewCertPool()
	ca, e := os.ReadFile(caFile)
	if e != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("private native TLS")
	}
	rawDSN := os.Getenv("PROJECT_ONE_TEST_DSN")
	if rawDSN == "" {
		raw, err := config.Secret(os.Getenv("PROJECT_ONE_TEST_DSN_FILE"))
		if err != nil {
			t.Fatal("private native DSN required")
		}
		rawDSN = strings.TrimSpace(string(raw))
	}
	dsn, e := url.Parse(rawDSN)
	if e != nil || !strings.HasSuffix(databaseName, "_test") {
		t.Fatal("exact isolated native database")
	}
	dsn.Path = "/" + databaseName
	query := dsn.Query()
	query.Set("search_path", s.DB.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = query.Encode()
	c := s.Identity.Config
	c.Server.ListenAddress = listen
	c.Server.PortalOrigin = "https://127.0.0.1:" + port
	c.Server.AdminOrigin = "https://localhost:" + port
	c.Server.TLSCertificateFile = certificate
	c.Server.TLSPrivateKeyFile = key
	c.Server.PortalAssets = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "web/apps/portal/dist")
	c.Server.AdminAssets = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "web/apps/console/dist")
	{
		c.Datasets.PythonExecutable = os.Getenv("PROJECT_ONE_DATA_PYTHON")
		c.Datasets.ProcessorFile = filepath.Join(os.Getenv("PROJECT_ONE_REPOSITORY_ROOT"), "runtimes/data-processing/process.py")
		if _, e := os.Stat(c.Datasets.ArtifactRoot); os.IsNotExist(e) {
			// These are explicitly empty disposable namespaces. Full accepted artifact
			// export/import is the separate matched four-store recovery profile.
			if e = models.InitializeStore(c.Models.ArtifactRoot, os.Geteuid(), os.Getegid()); e != nil {
				t.Fatal(e)
			}
			tenant := s.Identity.Config.Models.ArtifactRoot
			if e = s.DB.QueryRow(ctx, "SELECT tenant_id::text FROM installation_state WHERE id=1").Scan(&tenant); e != nil {
				t.Fatal(e)
			}
			for _, item := range []struct {
				path       string
				initialize func(string, string, int, int) error
			}{{c.Datasets.ArtifactRoot, datasets.InitializeStore}, {c.Training.ArtifactRoot, training.InitializeStore}, {c.ModelPreparation.ArtifactRoot, modelpreparations.InitializeStore}} {
				if e = os.Mkdir(item.path, 0700); e != nil {
					t.Fatal(e)
				}
				if e = item.initialize(item.path, tenant, os.Geteuid(), os.Getegid()); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	c.Database.DSNFile = write("operator-dsn", []byte(dsn.String()))
	c.Database.MaxOpenConnections = 10
	c.Database.MaxIdleConnections = 1
	c.Database.StatementTimeoutSeconds = 5
	c.Security.AuthHMACKeyFile = write("auth-hmac", s.Identity.HMACKey)
	keys, _ := json.Marshal(s.Identity.Mail.Keys)
	c.Security.MailKeyringFile = write("mail-keyring.json", keys)
	c.Security.PasswordBlocklistFile = write("blocklist", []byte("passwordpassword\n"))
	c.SMTP.Host = "127.0.0.1"
	c.SMTP.Port = 1
	c.SMTP.TLSMode = "starttls_required"
	c.SMTP.ConnectTimeoutSeconds = 1
	c.SMTP.OperationTimeoutSeconds = 2
	raw, e := yaml.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	configurationFile := write("control.yaml", raw)
	if _, e = config.Load(configurationFile); e != nil {
		t.Fatal("actual native configuration", e)
	}
	jar, _ := cookiejar.New(nil)
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	process := &nativeKnowledgeProcess{testing: t, ctx: ctx, binary: binary, path: configurationFile, origin: c.Server.PortalOrigin, configuration: c, client: &http.Client{Transport: transport, Jar: jar, Timeout: config.Seconds(c.Policy().HTTP.RequestSeconds)}}
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("native recovery evidence root required")
	}
	process.log, e = os.OpenFile(filepath.Join(output, label+"-control.log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { process.stop(); process.client.CloseIdleConnections(); process.log.Close() })
	return process
}
func (p *nativeKnowledgeProcess) start() {
	p.testing.Helper()
	p.command = exec.CommandContext(p.ctx, p.binary, "serve", "--config", p.path)
	p.command.Stdout = p.log
	p.command.Stderr = p.log
	if e := p.command.Start(); e != nil {
		p.testing.Fatal("native control start", e)
	}
	p.exited = make(chan error, 1)
	command := p.command
	go func() { p.exited <- command.Wait() }()
	deadline := time.Now().Add(config.Seconds(p.configuration.Policy().HTTP.StartupSeconds))
	for time.Now().Before(deadline) {
		response, e := p.client.Get(p.origin + "/health/ready")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				return
			}
		}
		select {
		case <-p.exited:
			p.command = nil
			p.testing.Fatal("native control exited before readiness")
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	p.testing.Fatal("native knowledge readiness deadline")
}
func (p *nativeKnowledgeProcess) stop() {
	if p.command == nil {
		return
	}
	_ = p.command.Process.Signal(syscall.SIGTERM)
	select {
	case e := <-p.exited:
		if e != nil {
			p.testing.Error("native control shutdown", e)
		}
	case <-time.After(config.Seconds(p.configuration.Policy().HTTP.ShutdownSeconds + 3)):
		_ = p.command.Process.Kill()
		<-p.exited
		p.testing.Error("native knowledge shutdown deadline")
	}
	p.command = nil
	p.client.CloseIdleConnections()
}
func (p *nativeKnowledgeProcess) call(method, path string, input any, key string, status int) json.RawMessage {
	p.testing.Helper()
	var body []byte
	if input != nil {
		body, _ = json.Marshal(input)
	}
	headers := http.Header{}
	if method != "GET" {
		raw := p.call("GET", "/api/v1/auth/csrf", nil, "", 200)
		var csrf struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(raw, &csrf) != nil {
			p.testing.Fatal("native CSRF response")
		}
		headers.Set("X-CSRF-Token", csrf.Token)
		headers.Set("Origin", p.origin)
	}
	if input != nil {
		headers.Set("Content-Type", "application/json")
	}
	if key != "" {
		headers.Set("Idempotency-Key", key)
	}
	request, e := http.NewRequestWithContext(p.ctx, method, p.origin+path, bytes.NewReader(body))
	if e != nil {
		p.testing.Fatal(e)
	}
	request.Header = headers
	response, e := p.client.Do(request)
	if e != nil {
		p.testing.Fatal("native knowledge HTTP", e)
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024))
	if e != nil || response.StatusCode != status {
		var problem struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &problem)
		p.testing.Fatalf("native %s expected %d observed %d (%s)", path, status, response.StatusCode, problem.Code)
	}
	if status >= 400 || status == 204 {
		return raw
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		p.testing.Fatal("native response envelope")
	}
	return envelope.Data
}
func (p *nativeKnowledgeProcess) operator(wantSuccess bool) {
	p.testing.Helper()
	command := exec.CommandContext(p.ctx, p.binary, "invalidate-restored-auth", "--config", p.path, "--reason", "Synthetic isolated knowledge restore acceptance")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	e := command.Run()
	if (e == nil) != wantSuccess {
		p.testing.Fatal("native common restore operator outcome mismatch")
	}
	if wantSuccess && !strings.Contains(output.String(), "RESTORED_AUTH_INVALIDATED") {
		p.testing.Fatal("native restore operator missing success receipt")
	}
}
func TestM12ActualNativeRestartsAtomicLogicalRestoreOldLeaseFenceAndRecovery(t *testing.T) {
	s, a, p, password := serviceFixtureWithPassword(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	owned := fixture.OwnedPostgres(t, s.DB)
	w := a.Me.Workspace.ID
	base := createFacts(t, s, p, w)
	doc := uploadFacts(t, s, p, w, base.ID)
	processOne(t, s)
	doc, e := s.GetDocument(ctx, p, w, base.ID, doc.ID)
	if e != nil {
		t.Fatal(e)
	}
	original, e := s.GetSource(ctx, p, w, base.ID, doc.ID, doc.VisibleSource.ID)
	if e != nil {
		t.Fatal(e)
	}
	var originalVector string
	if e = s.DB.QueryRow(ctx, "SELECT embedding::text FROM knowledge_chunks WHERE document_id=$1 ORDER BY ordinal LIMIT 1", doc.ID).Scan(&originalVector); e != nil {
		t.Fatal(e)
	}
	if os.Getenv("PROJECT_ONE_TEST_NATIVE_POSTGRES_INPUT") != "" {
		owned.RestartNative(t, ctx)
	}
	sourceProcess := nativeKnowledge(t, ctx, s, s.DB.Config().ConnConfig.Database, "source")
	sourceProcess.start()
	sourceProcess.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, "", 200)
	path := "/api/v1/workspaces/" + w + "/knowledge-bases/" + base.ID
	sourceProcess.call("GET", path+"/documents/"+doc.ID+"/source?source_id="+original.SourceID, nil, "", 200)
	sourceProcess.operator(false)
	sourceProcess.stop()
	sourceProcess.start()
	sourceProcess.call("GET", "/api/v1/me", nil, "", 200)
	sourceProcess.stop()
	text := "The updated project launch city is Porto."
	replacement, e := s.Replace(ctx, p, w, base.ID, doc.ID, ReplaceSource{Content: text, SHA256: hashBytes([]byte(text)), ExpectedRevision: doc.Revision, Reason: "Retain a real admitted replacement across logical backup"}, uuid.NewString(), uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	candidate, e := s.nextCandidate(ctx)
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := s.claim(ctx, candidate)
	if e != nil || claimed == nil {
		t.Fatal("actual durable claim before simulated crash", e)
	}
	// Real claim/source/vector state is exported; stopping before CPU execution is
	// an explicit crash-after-claim fixture, not fabricated successful output.
	archive := owned.DumpSchema(t, ctx)
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	archiveFile := filepath.Join(output, "knowledge-backup.dump")
	if os.WriteFile(archiveFile, archive, 0600) != nil {
		t.Fatal("private actual database archive")
	}
	restored := owned.RestoreFresh(t, ctx, s.DB, archive, true)
	if e = database.Check(ctx, restored); e != nil {
		t.Fatal("restored exact schema", e)
	}
	accounts := *s.Identity
	accounts.DB = restored
	accounts.Storage = nil
	mail := *s.Identity.Mail
	mail.DB = restored
	accounts.Mail = &mail
	restoredService := &Service{DB: restored, Identity: &accounts, Workspaces: &workspaces.Service{DB: restored, Identity: &accounts}, Processor: s.Processor}
	targetProcess := nativeKnowledge(t, ctx, restoredService, restored.Config().ConnConfig.Database, "restored")
	if _, e = restored.Exec(ctx, `CREATE FUNCTION refuse_knowledge_restore() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.state IN ('pending','running') THEN RAISE EXCEPTION 'synthetic restore rollback'; END IF; RETURN NEW; END $$; CREATE TRIGGER refuse_knowledge_restore BEFORE UPDATE ON knowledge_attempts FOR EACH ROW EXECUTE FUNCTION refuse_knowledge_restore()`); e != nil {
		t.Fatal(e)
	}
	targetProcess.operator(false)
	if _, e = restoredService.GetBase(ctx, p, w, base.ID); e != nil {
		t.Fatal("failed common restore did not roll back account authority", e)
	}
	var state string
	if e = restored.QueryRow(ctx, "SELECT state FROM knowledge_attempts WHERE id=$1", claimed.id).Scan(&state); e != nil || state != "running" {
		t.Fatal("failed common restore changed durable lease", e)
	}
	if _, e = restored.Exec(ctx, "DROP TRIGGER refuse_knowledge_restore ON knowledge_attempts; DROP FUNCTION refuse_knowledge_restore()"); e != nil {
		t.Fatal(e)
	}
	targetProcess.operator(true)
	if _, e = restoredService.GetBase(ctx, p, w, base.ID); e == nil {
		t.Fatal("restored old session survived")
	}
	fresh := fixture.Login(t, &accounts, "admin@example.invalid", password, "portal")
	retained, e := restoredService.GetDocument(ctx, fresh, w, base.ID, doc.ID)
	if e != nil || retained.State != "failed" || retained.CurrentAttempt.ErrorCode == nil || *retained.CurrentAttempt.ErrorCode != "KNOWLEDGE_WORKER_INTERRUPTED" || retained.VisibleSource.ID != original.SourceID {
		t.Fatal("restored replacement/last-good lease fencing", e)
	}
	source, e := restoredService.GetSource(ctx, fresh, w, base.ID, doc.ID, original.SourceID)
	if e != nil || source.Content != original.Content || source.SHA256 != original.SHA256 {
		t.Fatal("exact restored original bytes/hash", e)
	}
	var vector string
	if e = restored.QueryRow(ctx, "SELECT embedding::text FROM knowledge_chunks WHERE document_id=$1 ORDER BY ordinal LIMIT 1", doc.ID).Scan(&vector); e != nil || vector != originalVector {
		t.Fatal("atomic actual vector/source snapshot", e)
	}
	missing := targetProcess.configuration
	missing.Knowledge.ModelDirectory = filepath.Join(t.TempDir(), "missing-encoder")
	raw, e := yaml.Marshal(missing)
	if e != nil {
		t.Fatal(e)
	}
	missingFile := filepath.Join(t.TempDir(), "missing.yaml")
	if os.WriteFile(missingFile, raw, 0600) != nil {
		t.Fatal("missing asset configuration")
	}
	command := exec.CommandContext(ctx, os.Getenv("PROJECT_ONE_CONTROL_BINARY"), "serve", "--config", missingFile)
	diagnostic, e := command.CombinedOutput()
	if e == nil || bytes.Contains(diagnostic, []byte("CONTROL_STARTED")) || !bytes.Contains(diagnostic, []byte("KNOWLEDGE_INSTALLED_ASSET_INVALID")) {
		t.Fatal("missing model silently served or used a fallback")
	}
	targetProcess.client.Jar = sourceProcess.client.Jar
	targetProcess.start()
	targetProcess.call("GET", "/api/v1/me", nil, "", 401)
	targetProcess.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, "", 200)
	result := targetProcess.call("POST", path+"/searches", SearchRequest{Query: "Which city does the project launch in?", TopK: 1, MinimumScore: 0.1}, "", 200)
	var found SearchResult
	if json.Unmarshal(result, &found) != nil || len(found.Matches) != 1 || found.Matches[0].SourceID != original.SourceID {
		t.Fatal("native restored last-good retrieval")
	}
	targetProcess.call("POST", path+"/documents/"+doc.ID+"/retries", RevisionReason{ExpectedRevision: retained.Revision, Reason: "Explicit new attempt after isolated restore"}, uuid.NewString(), 202)
	deadline := time.Now().Add(config.Seconds(s.policy().IngestSeconds))
	var ready Document
	for time.Now().Before(deadline) {
		raw = targetProcess.call("GET", path+"/documents/"+doc.ID, nil, "", 200)
		if json.Unmarshal(raw, &ready) != nil {
			t.Fatal("native document DTO")
		}
		if ready.State == "ready" && ready.PendingSource == nil {
			break
		}
		if ready.State == "failed" {
			t.Fatal("native explicit restore retry failed")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if ready.VisibleSource == nil || ready.VisibleSource.ID != replacement.PendingSource.ID || ready.CurrentAttempt.AttemptNumber != 2 {
		t.Fatal("restored old lease resumed or explicit attempt duplicated")
	}
	targetProcess.call("GET", path+"/documents/"+doc.ID+"/source?source_id="+original.SourceID, nil, "", 404)
	targetProcess.stop()
	sourceOriginal, e := s.GetSource(ctx, p, w, base.ID, doc.ID, original.SourceID)
	if e != nil || sourceOriginal.Content != original.Content {
		t.Fatal("isolated restore altered source database", e)
	}
	sum := sha256.Sum256(archive)
	receipt, _ := json.MarshalIndent(map[string]any{"status": "passed_actual_native_restart_and_isolated_logical_knowledge_restore", "database_archive": archiveFile, "archive_sha256": hex.EncodeToString(sum[:]), "archive_bytes": len(archive), "source_sha256": original.SHA256, "vector_sha256": hashBytes([]byte(originalVector)), "encoder_catalog_sha256": CatalogDigest(), "old_sessions_and_leases_invalidated": true, "operator_rollback_verified": true, "native_missing_model_refused": true, "native_explicit_retry_completed": true, "target_database": restored.Config().ConnConfig.Database, "scope": "Actual native process restarts/PG custom dump+restore/real CPU encoder/pgvector, synthetic original only. No physical target or complete offline platform bundle."}, "", "  ")
	if os.WriteFile(filepath.Join(output, "knowledge-recovery.json"), append(receipt, '\n'), 0600) != nil {
		t.Fatal("native recovery evidence")
	}
}

func TestM12ActualNativeDisabledKnowledgeStartsWithoutEncoderAndRefusesWrites(t *testing.T) {
	s, a, _, password := serviceFixtureWithPassword(t)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	s.Identity.Config.Knowledge.Enabled = false
	missing := filepath.Join(t.TempDir(), "missing-approved-assets")
	s.Identity.Config.Knowledge.ModelDirectory = missing
	s.Identity.Config.Knowledge.WorkerFile = filepath.Join(missing, "embed.py")
	s.Identity.Config.Knowledge.PythonExecutable = filepath.Join(missing, "python3.12")
	p := nativeKnowledge(t, ctx, s, s.DB.Config().ConnConfig.Database, "disabled")
	p.start()
	p.call("POST", "/api/v1/auth/login", map[string]any{"email": "admin@example.invalid", "password": password}, "", 200)
	collection := "/api/v1/workspaces/" + a.Me.Workspace.ID + "/knowledge-bases"
	raw := p.call("GET", collection+"/configuration", nil, "", 200)
	var public Configuration
	if json.Unmarshal(raw, &public) != nil || public.Enabled || public.EncoderAvailable || public.CanAsk {
		t.Fatal("disabled knowledge advertised encoder/answer availability")
	}
	raw = p.call("POST", collection, map[string]any{"name": "Disabled native refusal", "reason": "Actual disabled mode must not synthesize readiness"}, uuid.NewString(), 503)
	var problem struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(raw, &problem) != nil || problem.Code != "KNOWLEDGE_UNAVAILABLE" {
		t.Fatal("disabled native refusal lost exact safe code")
	}
	var count int
	if e := s.DB.QueryRow(ctx, "SELECT count(*) FROM knowledge_bases").Scan(&count); e != nil || count != 0 {
		t.Fatal("disabled native write changed business state", e)
	}
	p.stop()
	t.Log("Actual cumulative native disabled startup/readiness/local login succeeds without missing encoder/Python/worker; public capability unavailable, CSRF-valid write503 and zero new bases; no fallback model or worker")
}
