package browser_test

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/datasets"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	transport "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/models"
	workspaceModule "github.com/Passion-Flow/ownsolid/services/control/internal/modules/workspaces"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/localization"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
)

func TestM12KnowledgeBrowser(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
			testProductionBrowser(t, "knowledge/knowledge-flow.mjs", "knowledge-browser-"+locale, true)
		})
	}
}

func TestM08DatasetBrowser(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
			testProductionBrowser(t, "datasets/dataset-flow.mjs", "datasets-browser-"+locale, false)
		})
	}
}
func TestM10HoldoutBrowser(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
			testProductionBrowser(t, "model-lifecycle/holdout-flow.mjs", "model-lifecycle-browser-"+locale, true)
		})
	}
}
func TestM09TrainingBrowser(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
			testProductionBrowser(t, "training/training-flow.mjs", "training-browser-"+locale, false)
		})
	}
}
func TestM01ProductionBrowser(t *testing.T) {
	testProductionBrowser(t, "identity/account-flow.mjs", "browser", false)
}
func TestM02ProductionBrowser(t *testing.T) {
	testProductionBrowser(t, "workspaces/workspace-flow.mjs", "workspaces-browser", true)
}
func TestM03ProductionBrowser(t *testing.T) {
	testProductionBrowser(t, "nodes/node-flow.mjs", "nodes-browser", false)
}
func TestM04ProductionBrowser(t *testing.T) {
	testProductionBrowser(t, "components/component-flow.mjs", "components-browser", false)
}
func TestM06DeploymentBrowser(t *testing.T) {
	testProductionBrowser(t, "deployments/deployment-flow.mjs", "deployments-browser", false)
}
func TestM10AliasCutoverBrowser(t *testing.T) {
	testProductionBrowser(t, "distribution/alias-cutover-flow.mjs", "alias-cutover-browser", false)
}
func TestM07DistributionBrowser(t *testing.T) {
	for _, locale := range []string{"en", "zh-CN"} {
		t.Run(locale, func(t *testing.T) {
			t.Setenv("PROJECT_ONE_BROWSER_LOCALE", locale)
			testProductionBrowser(t, "distribution/distribution-flow.mjs", "distribution-browser-"+locale, false)
		})
	}
}
func TestM06ModelImportBrowser(t *testing.T) {
	testProductionBrowser(t, "models/model-flow.mjs", "models-browser", false)
}
func TestM05ProductionBrowser(t *testing.T) {
	testProductionBrowser(t, "components/maintenance-flow.mjs", "maintenance-browser", false)
}
func testProductionBrowser(t *testing.T, script, evidence string, workspace bool, verify ...func(context.Context, *models.Service, string)) {
	if os.Getenv("PROJECT_ONE_BROWSER") != "1" {
		t.Skip("set PROJECT_ONE_BROWSER=1 for the bounded real browser suite")
	}
	var s *identity.Service
	if strings.HasPrefix(script, "nodes/") {
		s = fixture.New(t)
	} else if script == "components/maintenance-flow.mjs" {
		s = fixture.New(t)
	} else if script == "components/component-flow.mjs" {
		s = fixture.New(t)
	} else if strings.HasPrefix(script, "workspaces/") {
		s = fixture.New(t)
	} else {
		s = fixture.New(t)
	}
	staticLocalizationMode := script == "localization/static-flow.mjs"
	localizationMode := strings.HasPrefix(script, "localization/")
	if localizationMode {
		policy := config.DefaultLocalizationPolicy()
		policy.CacheSeconds = 0
		policy.MaximumLocales = 4
		directory := os.Getenv("PROJECT_ONE_LOCALE_PACKAGE_DIRECTORY")
		required := []string{"en", "zh-CN"}
		if staticLocalizationMode {
			directory = os.Getenv("PROJECT_ONE_STATIC_LOCALE_PACKAGE_DIRECTORY")
			required = []string{"en", "fr", "ar"}
		}
		registry, e := localization.New(config.LocalizationConfiguration{PackageDirectory: directory, RequiredLocales: required}, policy)
		if e != nil {
			t.Fatal("actual offline browser locale package", e)
		}
		if !staticLocalizationMode && len(registry.Catalog()[0].Fonts) != 2 {
			t.Fatal("actual original fonts required")
		}
		if staticLocalizationMode {
			found := false
			for _, choice := range registry.Catalog() {
				if choice.Locale == "ar" && choice.Direction == "rtl" {
					for _, font := range choice.Fonts {
						if font.Family == "Noto Sans Arabic" {
							found = true
						}
					}
				}
			}
			if len(registry.Catalog()) != 4 || !found {
				t.Fatal("actual source-current Arabic package and original declared font required")
			}
		}
		s.Locales = registry
		s.Mail.Templates, e = mail.LoadTemplatesWithRegistry(registry)
		if e != nil {
			t.Fatal(e)
		}
	}
	componentMode := strings.HasPrefix(script, "components/")
	maintenanceMode := strings.Contains(script, "maintenance-flow")
	modelMode := strings.HasPrefix(script, "models/")
	if modelMode {
		s.Config.Models.ArtifactRoot = t.TempDir()
		if e := os.Chmod(s.Config.Models.ArtifactRoot, 0700); e != nil {
			t.Fatal(e)
		}
	}
	if maintenanceMode {
		s.Config.Components.NodeExporterCatalogDirectory = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/components/node-exporter")
	}
	if componentMode {
		s.Config.Components.NodeExporterArchiveFile = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/components/node-exporter/1.12.1/linux-amd64/node_exporter-1.12.1.linux-amd64.tar.gz")
	}
	password := fixture.Bootstrap(t, s)
	memberPassword := fixture.Password()
	if workspace {
		admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
		for _, email := range []string{"alice@example.invalid", "bob@example.invalid", "empty@example.invalid"} {
			i, e := s.Invite(context.Background(), admin, "", "createInvitation", uuid.NewString(), email, "en", 0, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Register(context.Background(), fixture.InvitationToken(t, s, i.ID), "Workspace browser fixture", memberPassword, uuid.NewString()); e != nil {
				t.Fatal(e)
			}
			if email == "empty@example.invalid" {
				actor := fixture.Login(t, s, email, memberPassword, "portal")
				ws := &workspaceModule.Service{DB: s.DB, Identity: s}
				if _, e = ws.ChangeMember(context.Background(), admin, admin.Me.Workspace.ID, actor.Me.User.ID, "", 1, "Synthetic empty-account fixture", uuid.NewString(), true); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	root, e := filepath.Abs("../../../../../")
	if e != nil {
		t.Fatal(e)
	}
	browserAssets := make(map[string]string)
	for app, directory := range map[string]string{"portal": "portal", "admin": "console"} {
		assets := os.Getenv("PROJECT_ONE_BROWSER_" + strings.ToUpper(app) + "_ASSETS")
		if assets == "" {
			assets = filepath.Join(root, "web/apps", directory, "dist")
		}
		if !filepath.IsAbs(assets) || filepath.Clean(assets) != assets {
			t.Fatal("canonical production asset reference required")
		}
		browserAssets[app] = assets
		if _, e = os.Stat(filepath.Join(assets, "index.html")); e != nil {
			t.Fatal("production UI build required")
		}
	}
	dir := t.TempDir()
	nodeMode := strings.HasPrefix(script, "nodes/")
	names := []string{}
	if nodeMode {
		names = []string{"localhost", "host.docker.internal"}
	}
	cert, caFile, certFile, keyFile := smtpsink.Certificate(t, dir, names...)
	if localizationMode {
		missing := "fr"
		if staticLocalizationMode {
			missing = "de"
		}
		if _, e := s.DB.Exec(context.Background(), "UPDATE users SET locale=$1,version=version+1 WHERE email_canonical='alice@example.invalid'", missing); e != nil {
			t.Fatal(e)
		}
	}
	if strings.HasPrefix(script, "portal/") {
		policy := s.Config.Policy()
		policy.UI.PageSize = 1
		policy.Workflows.HeartbeatSeconds = 1 // Explicit cancellation fault profile, below the3s SQL lock budget.
		s.Config.Runtime = &policy
	}
	pluginMode := strings.HasPrefix(script, "plugins/") || strings.HasPrefix(script, "portal/")
	knowledgeMode := strings.HasPrefix(script, "knowledge/") || (pluginMode && os.Getenv("PROJECT_ONE_PLUGIN_BROWSER_TOOL_ONLY") != "1")
	var pluginCapsules []string
	if pluginMode {
		pluginCapsules = configurePluginBrowser(t, s, dir)
	}
	trainingMode := strings.HasPrefix(script, "training/")
	datasetMode := strings.HasPrefix(script, "datasets/") || strings.HasPrefix(script, "model-lifecycle/") || trainingMode
	if datasetMode {
		datasetRoot := t.TempDir()
		if e = os.Chmod(datasetRoot, 0700); e != nil {
			t.Fatal(e)
		}
		binary := os.Getenv("PROJECT_ONE_DATA_PYTHON")
		if binary == "" {
			t.Fatal("explicit native CPython required")
		}
		s.Config.Datasets = config.DatasetConfiguration{ArtifactRoot: datasetRoot, PythonExecutable: binary, ProcessorFile: filepath.Join(root, "runtimes/data-processing/process.py")}
		if strings.HasPrefix(script, "datasets/") {
			marker := filepath.Join(dir, "dataset.fail-next")
			wrapper := filepath.Join(dir, "dataset-processor-fault.py")
			body := fmt.Sprintf("import os,sys\nmarker=%q\nif os.path.exists(marker):\n os.unlink(marker)\n sys.exit(70)\nos.execv(%q,[%q,'-I','-B',%q,*sys.argv[1:]])\n", marker, binary, binary, s.Config.Datasets.ProcessorFile)
			if e = os.WriteFile(wrapper, []byte(body), 0600); e != nil {
				t.Fatal(e)
			}
			s.Config.Datasets.ProcessorFile = wrapper
		}

		var tenant string
		if e = s.DB.QueryRow(context.Background(), "SELECT tenant_id::text FROM installation_state WHERE id=1").Scan(&tenant); e != nil {
			t.Fatal(e)
		}
		if e = datasets.InitializeStore(datasetRoot, tenant, os.Geteuid(), os.Getegid()); e != nil {
			t.Fatal(e)
		}
	}
	if knowledgeMode {
		scratch := t.TempDir()
		if e = os.Chmod(scratch, 0700); e != nil {
			t.Fatal(e)
		}
		s.Config.Knowledge = config.KnowledgeConfiguration{Enabled: true, PythonExecutable: os.Getenv("PROJECT_ONE_KNOWLEDGE_PYTHON"), WorkerFile: filepath.Join(root, "runtimes/knowledge/embed.py"), ModelDirectory: os.Getenv("PROJECT_ONE_KNOWLEDGE_MODEL_DIRECTORY"), ScratchRoot: scratch}
		policy := s.Config.Policy()
		policy.HTTP.RequestSeconds = 90
		policy.HTTP.StartupSeconds = 60
		policy.HTTP.ShutdownSeconds = 20
		s.Config.Runtime = &policy
	}
	var portalSchemaReadiness func(context.Context) error
	if strings.HasPrefix(script, "portal/") {
		portalSchemaReadiness = portalBrowserGate(t, s, filepath.Join(os.Getenv("PROJECT_ONE_EVIDENCE_ROOT"), evidence))
	}
	var staticMachineListener net.Listener
	if staticLocalizationMode {
		staticMachineListener, e = net.Listen("tcp4", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		defer staticMachineListener.Close()
		_, machinePort, _ := net.SplitHostPort(staticMachineListener.Addr().String())
		s.Config.MachineServer = &config.MachineServerConfiguration{ListenAddress: staticMachineListener.Addr().String(), Origin: "https://localhost:" + machinePort, TLSCertificateFile: certFile, TLSPrivateKeyFile: keyFile}
		s.Config.Server.TransportMode = config.TransportHTTP
		s.Config.Server.PortalOrigin = "http://localhost:19441"
		s.Config.Server.AdminOrigin = "http://127.0.0.1:19441"
		policy := s.Config.Policy()
		policy.HTTP.ShutdownSeconds = 20
		s.Config.Runtime = &policy
	}
	handler := transport.New(s)
	if staticLocalizationMode {
		machine := httptest.NewUnstartedServer(handler.MachineHandler())
		machine.Listener.Close()
		machine.Listener = staticMachineListener
		machine.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
		machine.StartTLS()
		defer machine.Close()
	}
	if portalSchemaReadiness != nil {
		handler.SchemaReadiness = portalSchemaReadiness
	}
	if datasetMode {
		if e = handler.Datasets.Service.OpenConfiguredStore(context.Background()); e != nil {
			t.Fatal(e)
		}
		if trainingMode {
			defer handler.Datasets.Service.Store.Close()
		}
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	s.Config.Server.PortalOrigin = "https://localhost:" + port
	s.Config.Server.AdminOrigin = "https://127.0.0.1:" + port
	if staticLocalizationMode {
		s.Config.Server.PortalOrigin = "http://localhost:" + port
		s.Config.Server.AdminOrigin = "http://127.0.0.1:" + port
	}
	if nodeMode {
		s.Config.Server.AdminOrigin = "https://host.docker.internal:" + port
	}
	s.Config.Server.PortalAssets = browserAssets["portal"]
	s.Config.Server.AdminAssets = browserAssets["admin"]
	sink := smtpsink.Start(t, false, false)
	smtpHost, smtpPort, _ := net.SplitHostPort(sink.Listener.Addr().String())
	s.Mail.Config = s.Config
	s.Mail.Config.SMTP.Host = smtpHost
	s.Mail.Config.SMTP.Port, _ = strconv.Atoi(smtpPort)
	s.Mail.Config.SMTP.TLSMode = "starttls_required"
	s.Mail.Config.SMTP.CAFile = sink.CAFile
	s.Mail.Config.SMTP.ConnectTimeoutSeconds = 5
	s.Mail.Config.SMTP.OperationTimeoutSeconds = 20
	inbox := filepath.Join(dir, "inbox")
	os.Mkdir(inbox, 0700)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	{
		if e = handler.Notifications.Service.Start(ctx); e != nil {
			t.Fatal(e)
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			if e := handler.Notifications.Service.Stop(cleanup); e != nil {
				t.Error(e)
			}
		}()
	}
	if knowledgeMode {
		if e = handler.Knowledge.Service.OpenConfiguredProcessor(ctx); e != nil {
			t.Fatal(e)
		}
		done := make(chan struct{})
		go func() { handler.Knowledge.Service.Run(ctx); close(done) }()
		defer func() { cancel(); handler.Knowledge.Service.Processor.Close(); <-done }()
	}
	if pluginMode {
		if e = handler.Plugins.Service.Start(ctx); e != nil {
			t.Fatal(e)
		}
		defer func() {
			stop, finish := context.WithTimeout(context.Background(), 10*time.Second)
			defer finish()
			if e := handler.Plugins.Service.Stop(stop); e != nil {
				t.Error(e)
			}
		}()
		if e = handler.Workflows.Service.Start(ctx); e != nil {
			t.Fatal(e)
		}
		defer func() {
			stop, finish := context.WithTimeout(context.Background(), 10*time.Second)
			defer finish()
			if e := handler.Workflows.Service.Stop(stop); e != nil {
				t.Error(e)
			}
		}()
	}
	if datasetMode && !trainingMode {
		done := make(chan struct{})
		go func() { handler.Datasets.Service.Run(ctx); close(done) }()
		defer func() { cancel(); <-done; handler.Datasets.Service.Store.Close() }()
	}
	if !trainingMode {
		mailDone := make(chan struct{})
		go func() { defer close(mailDone); s.Mail.Run(ctx) }()
		defer func() { cancel(); <-mailDone }()
	}
	if modelMode {
		done := make(chan struct{})
		go func() { handler.Models.Service.Run(ctx); close(done) }()
		defer func() { cancel(); <-done }()
	}
	inboxDone := make(chan struct{})
	go func() {
		defer close(inboxDone)
		n := 0
		for {
			select {
			case <-ctx.Done():
				return
			case b := <-sink.Messages:
				n++
				os.WriteFile(filepath.Join(inbox, fmt.Sprintf("%04d.eml", n)), b, 0600)
			}
		}
	}()
	defer func() { cancel(); <-inboxDone }()
	if staticLocalizationMode {
		server.Start()
	} else {
		server.StartTLS()
	}
	defer server.Close()
	leaf, _ := x509.ParseCertificate(cert.Certificate[0])
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	output := os.Getenv("PROJECT_ONE_EVIDENCE_ROOT")
	if output == "" {
		t.Fatal("PROJECT_ONE_EVIDENCE_ROOT is required")
	}
	output = filepath.Join(output, evidence)
	os.MkdirAll(output, 0700)
	manifest := map[string]any{"browser_locale": os.Getenv("PROJECT_ONE_BROWSER_LOCALE"), "topology": s.Config.Topology.EffectiveSize(), "portal_assets": s.Config.Server.PortalAssets, "portal": s.Config.Server.PortalOrigin, "admin": s.Config.Server.AdminOrigin, "admin_email": "admin@example.invalid", "admin_password": password, "member_email": "alice@example.invalid", "member_password": memberPassword, "second_email": "bob@example.invalid", "new_password": fixture.Password(), "inbox": inbox, "spki": base64.StdEncoding.EncodeToString(pin[:]), "output": output}
	if staticLocalizationMode {
		manifest["static_locale_directory"] = os.Getenv("PROJECT_ONE_STATIC_LOCALE_PACKAGE_DIRECTORY")
		manifest["transport"] = "http"
	}
	if strings.HasPrefix(script, "knowledge/") {
		configureKnowledgeBrowserActors(t, s, password, memberPassword, manifest)
	}
	if strings.HasPrefix(script, "datasets/") {
		configureDatasetBrowserActors(t, s, password, memberPassword, manifest)
		manifest["dataset_fault_file"] = filepath.Join(dir, "dataset.fail-next")
	}
	if pluginMode {
		manifest["plugin_capsules"] = pluginCapsules
		manifest["plugin_tool_only"] = os.Getenv("PROJECT_ONE_PLUGIN_BROWSER_TOOL_ONLY") == "1"
	}
	if modelMode {
		manifest["model_bundle"] = filepath.Join(os.Getenv("PROJECT_ONE_STATE_ROOT"), "dependencies/models/qwen3-0.6b/c1899de289a04d12100db370d81485cdf75e47ca/qwen3-0.6b.tar")
		if script == "models/recovery-tail.mjs" {
			seedModelRecoveryTail(t, ctx, handler.Models.Service, password, manifest)
		}
	}
	if nodeMode {
		binary := os.Getenv("PROJECT_ONE_AGENT_AMD64_BINARY")
		if binary == "" {
			t.Fatal("explicit amd64 agent build required")
		}
		disk, e := filepath.EvalSymlinks("/Volumes/DevData/DevData/Docker/DockerDesktop/Docker.raw")
		if e != nil || !strings.HasPrefix(disk, "/Volumes/DevData/") {
			t.Fatal("external Docker VM disk required")
		}
		shared := t.TempDir()
		os.Chmod(shared, 0700)
		ca, _ := os.ReadFile(caFile)
		os.WriteFile(filepath.Join(shared, "ca.pem"), ca, 0600)
		identifier := uuid.NewString()[:8]
		container := "projectone-m03-browser-" + identifier
		volume := "projectone-m03-browser-state-" + identifier
		manifest["agent_binary"] = binary
		manifest["agent_shared_directory"] = shared
		manifest["agent_container"] = container
		manifest["agent_volume"] = volume
		defer func() {
			label, _ := exec.Command("docker", "inspect", "--format", `{{index .Config.Labels "projectone.owner"}}`, container).Output()
			if strings.TrimSpace(string(label)) == "mvp-development" {
				exec.Command("docker", "rm", "--force", container).Run()
			}
			label, _ = exec.Command("docker", "volume", "inspect", "--format", `{{index .Labels "projectone.owner"}}`, volume).Output()
			if strings.TrimSpace(string(label)) == "mvp-development" {
				exec.Command("docker", "volume", "rm", volume).Run()
			}
		}()
	}
	if componentMode {
		admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
		var nodeID string
		var stop func()
		if maintenanceMode {
			nodeID, stop = startSyntheticMaintenanceMachine(t, server, handler.Nodes.Service, admin)
		} else {
			nodeID, stop = startSyntheticComponentMachine(t, server, handler.Nodes.Service, admin)
		}
		defer stop()
		manifest["component_node_id"] = nodeID
	}
	if strings.HasPrefix(script, "deployments/") {
		admin := fixture.Login(t, s, "admin@example.invalid", password, "admin")
		nodeID, modelID, stop := startSyntheticInferenceMachine(t, server, handler.Nodes.Service, admin)
		defer stop()
		manifest["inference_node_id"] = nodeID
		manifest["model_id"] = modelID
	}
	if strings.HasPrefix(script, "distribution/") {
		configureDistributionBrowser(t, server, handler, manifest, dir, caFile, certFile, keyFile, password)
	}
	if trainingMode {
		configureDatasetBrowserActors(t, s, password, memberPassword, manifest)
		cleanup := configureTrainingBrowser(t, handler, manifest)
		defer cleanup()
	}
	b, _ := json.Marshal(manifest)
	path := filepath.Join(dir, "browser-fixture.json")
	os.WriteFile(path, b, 0600)
	timeout, stop := context.WithTimeout(context.Background(), 150*time.Second)
	defer stop()
	if script == "models/recovery-flow.mjs" || script == "models/model-flow.mjs" {
		stop()
		deadline, ok := t.Deadline()
		if !ok {
			t.Fatal("explicit whole-test timeout required for bounded model recovery")
		}
		timeout, stop = context.WithDeadline(context.Background(), deadline.Add(-20*time.Second))
		defer stop()
	}
	command := exec.CommandContext(timeout, "node", filepath.Join(root, "tests/acceptance", script), path)
	command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+caFile)
	command.Cancel = func() error { return command.Process.Signal(syscall.SIGTERM) }
	command.WaitDelay = 10 * time.Second
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if e = command.Run(); e != nil {
		t.Fatal("BROWSER_ACCEPTANCE_FAILED")
	}
	for _, check := range verify {
		check(ctx, handler.Models.Service, output)
	}
	if localizationMode {
		expected := map[string]string{"admin@example.invalid": "zh-CN", "alice@example.invalid": "fr"}
		if staticLocalizationMode {
			expected = map[string]string{"admin@example.invalid": "fr", "alice@example.invalid": "de"}
		}
		for email, want := range expected {
			var actual string
			if e = s.DB.QueryRow(context.Background(), "SELECT locale FROM users WHERE email_canonical=$1", email).Scan(&actual); e != nil || actual != want {
				t.Fatal("browser locale persistence/fallback integrity", e)
			}
		}
	}
}
