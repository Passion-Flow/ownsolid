package compose_test

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/google/uuid"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/plugins"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/operations/databaseaccess"
	fixture "github.com/Passion-Flow/ownsolid/services/control/internal/testing/identityfixture"
	"github.com/Passion-Flow/ownsolid/services/control/internal/testing/smtpsink"
	licensewire "github.com/Passion-Flow/ownsolid/pkg/license/v1"
)

func TestM01DeclaredComposeInstall(t *testing.T) {
	localizationOnly := os.Getenv("PROJECT_ONE_COMPOSE_LOCALIZATION_ONLY") == "1"
	portalOnly := os.Getenv("PROJECT_ONE_COMPOSE_PORTAL_ONLY") == "1" || localizationOnly
	pluginsOnly := os.Getenv("PROJECT_ONE_COMPOSE_PLUGINS_ONLY") == "1" || portalOnly
	toolOnly := os.Getenv("PROJECT_ONE_COMPOSE_PLUGIN_TOOL_ONLY") == "1"
	workflowsOnly := os.Getenv("PROJECT_ONE_COMPOSE_WORKFLOWS_ONLY") == "1" || pluginsOnly
	if toolOnly && !pluginsOnly {
		t.Fatal("TOOL_ONLY_COMPOSE_REQUIRES_CUMULATIVE_PLUGINS")
	}
	knowledgeOnly := os.Getenv("PROJECT_ONE_COMPOSE_KNOWLEDGE_ONLY") == "1" || workflowsOnly
	ssoOnly := os.Getenv("PROJECT_ONE_COMPOSE_SSO_ONLY") == "1" || knowledgeOnly
	lifecycleOnly := os.Getenv("PROJECT_ONE_COMPOSE_LIFECYCLE_ONLY") == "1" || ssoOnly
	trainingOnly := os.Getenv("PROJECT_ONE_COMPOSE_TRAINING_ONLY") == "1" || lifecycleOnly
	datasetsOnly := os.Getenv("PROJECT_ONE_COMPOSE_DATASETS_ONLY") == "1" || trainingOnly
	accountOnly := os.Getenv("PROJECT_ONE_COMPOSE_ACCOUNT_ONLY") == "1"
	gatewayOnly := os.Getenv("PROJECT_ONE_COMPOSE_GATEWAY_ONLY") == "1"
	modelsOnly := os.Getenv("PROJECT_ONE_COMPOSE_MODELS_ONLY") == "1" || gatewayOnly || datasetsOnly
	var composeLicenseKey licensewire.PrivateKey
	image := os.Getenv("PROJECT_ONE_COMPOSE_IMAGE")
	if image == "" {
		t.Skip("set PROJECT_ONE_COMPOSE_IMAGE for the explicit isolated Compose profile")
	}
	root, _ := filepath.Abs("../../../../../")
	state := os.Getenv("PROJECT_ONE_STATE_ROOT")
	if !strings.HasPrefix(state, "/Volumes/DevData/") {
		t.Fatal("EXTERNAL_STATE_ROOT_REQUIRED")
	}
	id := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	project := "projectone-m01-" + id
	runRoot := filepath.Join(state, "m01-compose", id)
	if modelsOnly {
		t.Logf("M06_COMPOSE_OWNED_ROOT=%s", runRoot)
	}
	configRoot := filepath.Join(runRoot, "config")
	dataRoot := filepath.Join(runRoot, "data")
	if e := os.MkdirAll(filepath.Dir(runRoot), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Mkdir(runRoot, 0700); e != nil {
		t.Fatal("REFUSE_EXISTING_OR_UNWRITABLE_COMPOSE_SCOPE_ROOT")
	}
	t.Logf("COMPOSE_SCOPE_CREATED=%s", runRoot)
	if e := os.Mkdir(configRoot, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(dataRoot, "postgres"), 0700); e != nil {
		t.Fatal(e)
	}
	write := func(name string, b []byte) {
		if e := os.WriteFile(filepath.Join(configRoot, name), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	pgPassword := uuid.NewString()
	runtimePassword, e := databaseaccess.NewCredential()
	if e != nil {
		t.Fatal(e)
	}
	write("postgres-password", []byte(pgPassword))
	write("operator-dsn", []byte("postgres://projectone_migrator:"+pgPassword+"@postgres:5432/projectone?sslmode=disable"))
	write("runtime-dsn", []byte("postgres://projectone_runtime:"+runtimePassword+"@postgres:5432/projectone?sslmode=disable"))
	key := make([]byte, 32)
	rand.Read(key)
	write("auth-hmac", key)
	ring, _ := json.Marshal(map[string]any{"active": "test", "keys": map[string]string{"test": base64.StdEncoding.EncodeToString(key)}})
	write("mail-keyring.json", ring)
	_, ca, certFile, keyFile := smtpsink.Certificate(t, t.TempDir())
	certBytes, _ := os.ReadFile(certFile)
	keyBytes, _ := os.ReadFile(keyFile)
	write("certificate.pem", certBytes)
	write("private-key.pem", keyBytes)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	listener.Close()
	origin := "https://127.0.0.1:" + port
	conf := map[string]any{"server": map[string]any{"listen_address": "0.0.0.0:8443", "portal_origin": origin, "admin_origin": "https://localhost:" + port, "tls_certificate_file": "/run/projectone/certificate.pem", "tls_private_key_file": "/run/projectone/private-key.pem", "portal_assets": "/opt/projectone/portal", "admin_assets": "/opt/projectone/admin"}, "database": map[string]any{"dsn_file": "/run/projectone/runtime-dsn", "max_open_connections": 8, "max_idle_connections": 1, "statement_timeout_seconds": 5}, "security": map[string]any{"auth_hmac_key_file": "/run/projectone/auth-hmac", "mail_keyring_file": "/run/projectone/mail-keyring.json", "password_blocklist_file": "/opt/projectone/security/password-blocklist.txt"}, "smtp": map[string]any{"host": "127.0.0.1", "port": 1, "from_address": "platform@example.invalid", "tls_mode": "starttls_required", "connect_timeout_seconds": 1, "operation_timeout_seconds": 2}}
	ssoMode := os.Getenv("PROJECT_ONE_COMPOSE_SSO") == "1" || ssoOnly
	if ssoMode {
		if e = sso.InitializeKeyring(filepath.Join(configRoot, "sso-keyring.json"), ""); e != nil {
			t.Fatal("COMPOSE_PRIVATE_SSO_KEYRING_INITIALIZATION_FAILED")
		}
		caBytes, e := os.ReadFile(ca)
		if e != nil {
			t.Fatal(e)
		}
		write("provider-ca.pem", caBytes)
		conf["sso"] = map[string]any{"enabled": true, "keyring_file": "/run/projectone/sso-keyring.json", "providers": []any{map[string]any{"origin": "https://provider.example.invalid", "issuer": "https://provider.example.invalid", "dial_addresses": []string{"127.0.0.1"}, "ca_file": "/run/projectone/provider-ca.pem"}}}
	}
	knowledgeMode := (os.Getenv("PROJECT_ONE_COMPOSE_KNOWLEDGE") == "1" || knowledgeOnly) && !toolOnly
	if knowledgeMode {
		conf["knowledge"] = map[string]any{"enabled": true, "python_executable": "/opt/projectone/knowledge-venv/bin/python3.12", "worker_file": "/opt/projectone/knowledge/embed.py", "model_directory": "/opt/projectone/knowledge-encoder", "scratch_root": "/var/lib/projectone/knowledge-work", "gateways": []any{}}
		conf["runtime"] = map[string]any{"http": map[string]any{"request_seconds": 90, "startup_seconds": 60, "shutdown_seconds": 20}}
	}
	if toolOnly {
		conf["knowledge"] = map[string]any{"enabled": false}
		conf["runtime"] = map[string]any{"http": map[string]any{"request_seconds": 90, "startup_seconds": 60, "shutdown_seconds": 30}}
	}
	workflowMode := os.Getenv("PROJECT_ONE_COMPOSE_WORKFLOWS") == "1" || workflowsOnly
	if workflowMode {
		if !knowledgeMode && !pluginsOnly {
			t.Fatal("WORKFLOW_COMPOSE_REQUIRES_KNOWLEDGE_PROFILE")
		}
		conf["workflows"] = map[string]any{"enabled": !toolOnly}
		runtime := conf["runtime"].(map[string]any)
		runtime["http"].(map[string]any)["shutdown_seconds"] = 30
		runtime["workflows"] = map[string]any{"concurrency": 1, "workspace_concurrency": 1, "maximum_workflows": 50, "retention_days": 14, "maintenance_seconds": 45}
		if workflowsOnly {
			// Different valid YAML proves the protected typed env overlay is
			// consumed by the actual installed process, including after restart.
			runtime["workflows"] = map[string]any{"concurrency": 2, "workspace_concurrency": 2, "maximum_workflows": 100, "retention_days": 30, "maintenance_seconds": 60}
			write("workflows.env", []byte("PROJECT_ONE_WORKFLOWS_ENABLED=true\nPROJECT_ONE_WORKFLOWS_CONCURRENCY=1\nPROJECT_ONE_WORKFLOWS_WORKSPACE_CONCURRENCY=1\nPROJECT_ONE_WORKFLOWS_MAXIMUM_WORKFLOWS=50\nPROJECT_ONE_WORKFLOWS_RETENTION_DAYS=14\nPROJECT_ONE_WORKFLOWS_MAINTENANCE_SECONDS=45\n"))
		}
	}
	pluginMode := os.Getenv("PROJECT_ONE_COMPOSE_PLUGINS") == "1" || pluginsOnly
	var pluginCapsule []byte
	if pluginMode {
		if !workflowMode {
			t.Fatal("plugin Compose profile requires real workflow profile")
		}
		pluginCapsule = configureComposePlugins(t, conf)
		if pluginsOnly {
			keys := conf["plugins"].(map[string]any)["trusted_keys"]
			public, _ := json.Marshal(keys)
			write("plugin-public-trust.json", public)
			conf["plugins"] = map[string]any{"enabled": false, "trusted_keys": []any{}}
			policy := conf["runtime"].(map[string]any)["plugins"].(map[string]any)
			policy["maximum_models"] = 11
			policy["maximum_references"] = 201
			old, e := os.ReadFile(filepath.Join(configRoot, "workflows.env"))
			if e != nil {
				t.Fatal(e)
			}
			write("workflows.env", append(old, []byte("PROJECT_ONE_PLUGINS_ENABLED=true\nPROJECT_ONE_PLUGINS_TRUSTED_KEYS_FILE=/run/projectone/plugin-public-trust.json\nPROJECT_ONE_PLUGINS_MAXIMUM_MODELS=10\nPROJECT_ONE_PLUGINS_MAXIMUM_REFERENCES=200\n")...))
		}

	}
	datasetsMode := os.Getenv("PROJECT_ONE_COMPOSE_DATASETS") == "1"
	if datasetsMode {
		conf["datasets"] = map[string]any{"artifact_root": "/var/lib/projectone/datasets", "python_executable": "/usr/local/bin/python3.12", "processor_file": "/opt/projectone/data-processing/process.py"}
	}
	modelsMode := os.Getenv("PROJECT_ONE_COMPOSE_MODELS") == "1"
	if modelsMode {
		conf["models"] = map[string]any{"artifact_root": "/var/lib/projectone/models"}
	}
	trainingMode := os.Getenv("PROJECT_ONE_COMPOSE_TRAINING") == "1"
	if trainingMode {
		if !datasetsMode || !modelsMode {
			t.Fatal("training Compose profile requires actual input stores")
		}
		conf["training"] = map[string]any{"artifact_root": "/var/lib/projectone/training", "image_reference": os.Getenv("PROJECT_ONE_TRAINING_IMAGE_REFERENCE"), "image_id": os.Getenv("PROJECT_ONE_TRAINING_IMAGE_ID"), "worker_source_sha256": os.Getenv("PROJECT_ONE_TRAINING_WORKER_SOURCE")}
	}
	preparationMode := os.Getenv("PROJECT_ONE_COMPOSE_PREPARATION") == "1"
	if preparationMode {
		if !trainingMode {
			t.Fatal("preparation Compose profile requires all real input stores")
		}
		conf["model_preparation"] = map[string]any{"artifact_root": "/var/lib/projectone/preparations", "image_reference": os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_REFERENCE"), "image_id": os.Getenv("PROJECT_ONE_PREPARATION_IMAGE_ID"), "worker_source_sha256": os.Getenv("PROJECT_ONE_PREPARATION_WORKER_SOURCE")}
	}
	if accountOnly || modelsOnly {
		conf["server"].(map[string]any)["application_profile"] = "account"
		key, trust, e := licensewire.GenerateKey("synthetic-account-compose")
		if e != nil {
			t.Fatal(e)
		}
		composeLicenseKey = key
		if modelsOnly {
			if !modelsMode || accountOnly || (trainingMode && !trainingOnly) || (preparationMode && !lifecycleOnly) || (datasetsMode && !datasetsOnly) || (knowledgeMode && !knowledgeOnly) || (ssoMode && !ssoOnly) || (workflowMode && !workflowsOnly) || (pluginMode && !pluginsOnly) {
				t.Fatal("MODELS_ONLY_COMPOSE_PROFILE_CONFLICT")
			}
			conf["server"].(map[string]any)["application_profile"] = "models"
			if datasetsOnly {
				if !datasetsMode {
					t.Fatal("DATASETS_ONLY_REQUIRES_DATASET_STORE")
				}
				conf["server"].(map[string]any)["application_profile"] = "datasets"
			}
			if trainingOnly {
				if !trainingMode || !datasetsMode {
					t.Fatal("TRAINING_ONLY_REQUIRES_TRAINING_AND_DATASET_STORES")
				}
				conf["server"].(map[string]any)["application_profile"] = "training"
			}
			if gatewayOnly {
				conf["server"].(map[string]any)["application_profile"] = "gateway"
			}
			if lifecycleOnly {
				if !preparationMode || !trainingMode || !datasetsMode || gatewayOnly || accountOnly {
					t.Fatal("LIFECYCLE_ONLY_REQUIRES_FOUR_EXACT_STORES")
				}
				conf["server"].(map[string]any)["application_profile"] = "lifecycle"
				if ssoOnly {
					conf["server"].(map[string]any)["application_profile"] = "sso"
					if knowledgeOnly {
						conf["server"].(map[string]any)["application_profile"] = "knowledge"
						if workflowsOnly {
							conf["server"].(map[string]any)["application_profile"] = "workflows"
							if pluginsOnly {
								conf["server"].(map[string]any)["application_profile"] = "plugins"
								if portalOnly {
									conf["server"].(map[string]any)["application_profile"] = "portal"
								}
							}
						}
					}
				}
			}

		}
		raw, e := json.Marshal(trust)
		if e != nil {
			t.Fatal(e)
		}
		write("license-trust.json", raw)
		conf["licensing"] = map[string]any{"trust_file": "/run/projectone/license-trust.json"}
	}
	if localizationOnly {
		conf["server"].(map[string]any)["application_profile"] = "localization"
		conf["localization"] = map[string]any{"package_directory": "/var/lib/projectone/locale-projection/private", "required_locales": []string{"en", "fr", "ar"}}
		write("locale-policy.json", []byte("{}"))
	}
	b, _ := json.Marshal(conf)
	write("control.yaml", b)
	conf["database"].(map[string]any)["dsn_file"] = "/run/projectone/operator-dsn"
	b, _ = json.Marshal(conf)
	write("operator.yaml", b)
	env := append(os.Environ(), "PROJECT_ONE_DATA_ROOT="+dataRoot, "PROJECT_ONE_CONFIG_ROOT="+configRoot, "PROJECT_ONE_CONTROL_IMAGE="+image, "PROJECT_ONE_PLATFORM=linux/arm64", "PROJECT_ONE_RUNTIME_UID="+strconv.Itoa(os.Getuid()), "PROJECT_ONE_RUNTIME_GID="+strconv.Itoa(os.Getgid()), "PROJECT_ONE_HTTPS_PORT="+port)
	base := []string{"compose", "-p", project, "-f", filepath.Join(root, "deploy/compose/base/compose.yaml")}
	if accountOnly {
		if ssoMode || knowledgeMode || workflowMode || pluginMode || datasetsMode || modelsMode || trainingMode || preparationMode {
			t.Fatal("ACCOUNT_ONLY_COMPOSE_FUTURE_FEATURE_REFUSED")
		}
		base = append(base, "-f", filepath.Join(root, "deploy/compose/account/compose.yaml"))
		// Exercise the supported base/account topology, including host-published
		// HTTPS and its declared internal-relay mail network. Docker suppresses
		// host publication when every attached bridge is internal. T065's separate
		// kernel-egress-denied native browser root retains the full TLS-mail path.
		// Both images must already be local; this profile never pulls them.
	}
	if knowledgeMode || pluginsOnly {
		vectorImage := os.Getenv("PROJECT_ONE_KNOWLEDGE_POSTGRES_IMAGE")
		if vectorImage == "" {
			t.Fatal("EXACT_DECLARED_VECTOR_IMAGE_REQUIRED")
		}
		env = append(env, "PROJECT_ONE_KNOWLEDGE_CONTROL_IMAGE="+image, "PROJECT_ONE_KNOWLEDGE_POSTGRES_IMAGE="+vectorImage)
	}
	// Docker Desktop's shared-directory UID translation cannot provide PostgreSQL ownership.
	// This is an explicitly declared local filesystem profile, not Linux bind-mount qualification.
	disk, e := filepath.EvalSymlinks("/Volumes/DevData/DevData/Docker/DockerDesktop/Docker.raw")
	if e != nil || !strings.HasPrefix(disk, "/Volumes/DevData/") {
		t.Fatal("EXTERNAL_DOCKER_VM_DISK_REQUIRED")
	}
	if opened, e := exec.Command("lsof", "-t", disk).Output(); e != nil || len(bytes.TrimSpace(opened)) == 0 {
		t.Fatal("EXTERNAL_DOCKER_VM_DISK_NOT_OPEN")
	}
	volume := project + "-postgres"
	if exec.Command("docker", "volume", "inspect", volume).Run() == nil {
		t.Fatal("REFUSE_EXISTING_FIXTURE_VOLUME")
	}
	if e = exec.Command("docker", "volume", "create", "--label", "projectone.owner=mvp-development", "--label", "projectone.scope="+id, volume).Run(); e != nil {
		t.Fatal("FIXTURE_VOLUME_CREATE_FAILED")
	}
	env = append(env, "PROJECT_ONE_DOCKER_VOLUME_NAME="+volume)
	base = append(base, "-f", filepath.Join(root, "deploy/compose/local-macos/compose.yaml"))
	ownedVolumes := []string{volume}
	preservedPrivateVolumes := map[string]bool{}
	if modelsMode {
		modelVolume := project + "-models"
		if exec.Command("docker", "volume", "inspect", modelVolume).Run() == nil {
			t.Fatal("REFUSE_EXISTING_MODEL_FIXTURE_VOLUME")
		}
		if e = exec.Command("docker", "volume", "create", "--label", "projectone.owner=mvp-development", "--label", "projectone.scope="+id, modelVolume).Run(); e != nil {
			t.Fatal("MODEL_FIXTURE_VOLUME_CREATE_FAILED")
		}
		ownedVolumes = append(ownedVolumes, modelVolume)
		env = append(env, "PROJECT_ONE_MODEL_DOCKER_VOLUME_NAME="+modelVolume)
		base = append(base, "-f", filepath.Join(root, "deploy/compose/local-macos/models.yaml"))
	}
	if datasetsMode {
		datasetVolume := project + "-datasets"
		if exec.Command("docker", "volume", "inspect", datasetVolume).Run() == nil {
			t.Fatal("REFUSE_EXISTING_DATASET_FIXTURE_VOLUME")
		}
		if e = exec.Command("docker", "volume", "create", "--label", "projectone.owner=mvp-development", "--label", "projectone.scope="+id, datasetVolume).Run(); e != nil {
			t.Fatal("DATASET_FIXTURE_VOLUME_CREATE_FAILED")
		}
		ownedVolumes = append(ownedVolumes, datasetVolume)
		env = append(env, "PROJECT_ONE_DATASET_DOCKER_VOLUME_NAME="+datasetVolume)
		base = append(base, "-f", filepath.Join(root, "deploy/compose/local-macos/datasets.yaml"))
	}
	if trainingMode {
		trainingVolume := project + "-training"
		if exec.Command("docker", "volume", "inspect", trainingVolume).Run() == nil {
			t.Fatal("REFUSE_EXISTING_TRAINING_FIXTURE_VOLUME")
		}
		if e = exec.Command("docker", "volume", "create", "--label", "projectone.owner=mvp-development", "--label", "projectone.scope="+id, trainingVolume).Run(); e != nil {
			t.Fatal("TRAINING_FIXTURE_VOLUME_CREATE_FAILED")
		}
		ownedVolumes = append(ownedVolumes, trainingVolume)
		env = append(env, "PROJECT_ONE_TRAINING_DOCKER_VOLUME_NAME="+trainingVolume)
		base = append(base, "-f", filepath.Join(root, "deploy/compose/local-macos/training.yaml"))
	}
	if preparationMode {
		preparationVolume := project + "-preparations"
		if exec.Command("docker", "volume", "inspect", preparationVolume).Run() == nil {
			t.Fatal("REFUSE_EXISTING_PREPARATION_FIXTURE_VOLUME")
		}
		if e = exec.Command("docker", "volume", "create", "--label", "projectone.owner=mvp-development", "--label", "projectone.scope="+id, preparationVolume).Run(); e != nil {
			t.Fatal("PREPARATION_FIXTURE_VOLUME_CREATE_FAILED")
		}
		ownedVolumes = append(ownedVolumes, preparationVolume)
		env = append(env, "PROJECT_ONE_PREPARATION_DOCKER_VOLUME_NAME="+preparationVolume)
		base = append(base, "-f", filepath.Join(root, "deploy/compose/local-macos/model-lifecycle.yaml"))
	}
	if knowledgeMode {
		// Keep the explicit CPU resource/scratch profile after every store overlay.
		base = append(base, "-f", filepath.Join(root, "deploy/compose/knowledge/compose.yaml"))
	}
	if workflowMode {
		env = append(env, "PROJECT_ONE_WORKFLOWS_CONTROL_IMAGE="+image)
		if !toolOnly {
			base = append(base, "-f", filepath.Join(root, "deploy/compose/workflows/compose.yaml"))
		}
		if workflowsOnly {
			env = append(env, "PROJECT_ONE_POLICY_ENVIRONMENT_BASENAME=workflows.env")
			base = append(base, "-f", filepath.Join(root, "deploy/compose/base/workspace-policy.yaml"))
		}
	}
	if pluginMode {
		env = append(env, "PROJECT_ONE_PLUGINS_CONTROL_IMAGE="+image)
		if toolOnly {
			base = append(base, "-f", filepath.Join(root, "deploy/compose/plugins/tool-only.yaml"))
		} else {
			base = append(base, "-f", filepath.Join(root, "deploy/compose/plugins/compose.yaml"))
		}
	}
	command := func(args ...string) *exec.Cmd {
		c := exec.Command("docker", append(append([]string{}, base...), args...)...)
		c.Env = env
		return c
	}
	run := func(args ...string) {
		t.Helper()
		c := command(args...)
		output, e := c.CombinedOutput()
		if e != nil {
			safe := strings.ReplaceAll(strings.ReplaceAll(string(output), pgPassword, "[redacted]"), runtimePassword, "[redacted]")
			t.Log(safe)
			logs, _ := command("logs", "--no-color", "--tail", "20", "postgres").CombinedOutput()
			t.Log(strings.ReplaceAll(strings.ReplaceAll(string(logs), pgPassword, "[redacted]"), runtimePassword, "[redacted]"))
			// Failure reporting must not start an undeclared interpreter or
			// another control process. Native health is its own acceptance step.
			t.Fatal("COMPOSE_OPERATION_FAILED: " + args[0])
		}
	}
	t.Cleanup(func() {
		if command("down", "--timeout", "10", "--remove-orphans").Run() != nil {
			t.Error("OWNED_COMPOSE_CLEANUP_FAILED")
			return
		}
		for _, volume := range ownedVolumes {
			if preservedPrivateVolumes[volume] {
				t.Logf("Private configuration recovery inventory: %s; inspect exact owner labels and the retained intent before explicit recovery/cleanup; volume creation may itself be uncertain", volume)
				continue
			}
			label, _ := exec.Command("docker", "volume", "inspect", "--format", `{{index .Labels "projectone.scope"}}`, volume).Output()
			if strings.TrimSpace(string(label)) == id {
				if exec.Command("docker", "volume", "rm", volume).Run() != nil {
					t.Error("OWNED_VOLUME_CLEANUP_FAILED")
				}
			}
		}
	})
	if localizationOnly {
		var localeVolume string
		env, base, localeVolume = configureComposeLocalization(t, root, configRoot, id, project, env, base)
		ownedVolumes = append(ownedVolumes, localeVolume)
	}
	if ssoMode || os.Getenv("PROJECT_ONE_COMPOSE_PRIVATE_INSTALLER") != "" {
		privateScopeID := strings.ReplaceAll(uuid.NewString(), "-", "")
		privateOperationID := strings.ReplaceAll(uuid.NewString(), "-", "")
		configurationVolume := "projectone-" + privateScopeID + "-control-config"
		if exec.Command("docker", "volume", "inspect", configurationVolume).Run() == nil {
			t.Fatal("REFUSE_EXISTING_PRIVATE_CONFIGURATION_VOLUME")
		}
		ownedVolumes = append(ownedVolumes, configurationVolume)
		preservedPrivateVolumes[configurationVolume] = true
		env = append(env, "PROJECT_ONE_PRIVATE_CONFIGURATION_VOLUME="+configurationVolume)
		base = append(base, "-f", filepath.Join(root, "deploy/compose/account/private-configuration.yaml"))
		initializeSyntheticPrivateConfiguration(t, configRoot, configurationVolume, image, privateScopeID, privateOperationID, id)
		delete(preservedPrivateVolumes, configurationVolume)
	}
	run("config", "--quiet")
	if modelsMode {
		run("run", "--rm", "--no-deps", "--user", "0:0", "--cap-add", "CHOWN", "control", "initialize-model-store", "--model-store-root", "/var/lib/projectone/models", "--owner-uid", strconv.Itoa(os.Getuid()), "--owner-gid", strconv.Itoa(os.Getgid()), "--confirm-empty-model-store")
	}
	run("up", "-d", "postgres")
	deadline := time.Now().Add(20 * time.Second)
	healthy := false
	for time.Now().Before(deadline) {
		if command("exec", "-T", "postgres", "sh", "-c", `PGPASSWORD="$(cat /run/secrets/postgres_password)" psql -X -h 127.0.0.1 -U projectone_migrator -d projectone -v ON_ERROR_STOP=1 -tAc 'SELECT 1'`).Run() == nil {
			healthy = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !healthy {
		t.Fatal("COMPOSE_DATABASE_TIMEOUT")
	}
	if knowledgeMode || pluginsOnly {
		// Only the generated fresh Compose database is eligible for this prerequisite.
		install := command("exec", "-T", "postgres", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "projectone_migrator", "-d", "projectone")
		install.Stdin = strings.NewReader("CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; REVOKE CREATE ON SCHEMA public FROM PUBLIC;")
		if e = install.Run(); e != nil {
			t.Fatal("FRESH_COMPOSE_VECTOR_PREREQUISITE_FAILED")
		}
	}
	if localizationOnly {
		run("run", "--rm", "--no-deps", "localization-init")
	}
	run("run", "--rm", "--no-deps", "control", "migrate", "--config", "/run/projectone/operator.yaml")
	if modelsOnly {
		run("run", "--rm", "--no-deps", "control", "prepare-runtime-access", "--config", "/run/projectone/operator.yaml", "--runtime-config", "/run/projectone/control.yaml")
		run("run", "--rm", "--no-deps", "control", "check-runtime-access", "--config", "/run/projectone/operator.yaml", "--runtime-config", "/run/projectone/control.yaml")
		count := command("exec", "-T", "postgres", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "projectone_migrator", "-d", "projectone", "-tAc", "SELECT (SELECT count(*) FROM pg_tables WHERE schemaname='public')::text||'/'||(SELECT count(*) FROM schema_migrations)::text")
		expectedCatalog := "28/11"
		if datasetsOnly {
			expectedCatalog = "42/14"
		}
		if trainingOnly {
			expectedCatalog = "50/15"
		}
		if gatewayOnly {
			expectedCatalog = "35/12"
		}
		if lifecycleOnly {
			expectedCatalog = "63/16"
			if ssoOnly {
				expectedCatalog = "67/17"
				if knowledgeOnly {
					expectedCatalog = "74/18"
					if workflowsOnly {
						expectedCatalog = "80/19"
						if pluginsOnly {
							expectedCatalog = "84/20"
							if portalOnly {
								expectedCatalog = "86/21"
								if localizationOnly {
									expectedCatalog = "86/22"
								}
							}
						}
					}
				}
			}
		}
		if raw, e := count.Output(); e != nil || strings.TrimSpace(string(raw)) != expectedCatalog {
			t.Fatal("MODELS_FRESH_SCHEMA_REQUIRED", e)
		}
	} else {
		grants := "CREATE ROLE projectone_runtime LOGIN PASSWORD '" + runtimePassword + "'; GRANT USAGE ON SCHEMA public TO projectone_runtime; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO projectone_runtime; REVOKE INSERT,UPDATE,DELETE ON schema_migrations FROM projectone_runtime;"
		grant := command("exec", "-T", "postgres", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "projectone_migrator", "-d", "projectone")
		grant.Stdin = strings.NewReader(grants)
		if e = grant.Run(); e != nil {
			t.Fatal("RUNTIME_ROLE_PROVISION_FAILED")
		}
	}

	password := fixture.Password()
	bootstrap := command("run", "--rm", "--no-deps", "control", "bootstrap-admin", "--config", "/run/projectone/operator.yaml")
	terminal, e := pty.Start(bootstrap)
	if e != nil {
		t.Fatal("BOOTSTRAP_TERMINAL_FAILED")
	}
	defer terminal.Close()
	chunks := make(chan string, 32)
	go func() {
		buffer := make([]byte, 1024)
		for {
			n, e := terminal.Read(buffer)
			if n > 0 {
				chunks <- string(buffer[:n])
			}
			if e != nil {
				close(chunks)
				return
			}
		}
	}()
	pending := ""
	wait := func(marker string) {
		t.Helper()
		deadline := time.NewTimer(30 * time.Second)
		defer deadline.Stop()
		for !strings.Contains(pending, marker) {
			select {
			case chunk, ok := <-chunks:
				if !ok {
					t.Fatal("BOOTSTRAP_EXITED_BEFORE_PROMPT")
				}
				pending += chunk
				if len(pending) > 65536 {
					t.Fatal("BOOTSTRAP_OUTPUT_BOUND_EXCEEDED")
				}
			case <-deadline.C:
				t.Fatal("BOOTSTRAP_PROMPT_TIMEOUT")
			}
		}
		pending = ""
	}
	for _, input := range []struct{ prompt, value string }{{"Administrator email:", "admin@example.invalid"}, {"Display name:", "Compose Admin"}, {"Installation name:", "Compose Test"}, {"Workspace name:", "Default"}, {"Password:", password}, {"Confirm password:", password}} {
		wait(input.prompt)
		time.Sleep(20 * time.Millisecond)
		if _, e = io.WriteString(terminal, input.value+"\n"); e != nil {
			t.Fatal("BOOTSTRAP_INPUT_FAILED")
		}
	}
	wait("BOOTSTRAP_COMPLETE")
	if e = bootstrap.Wait(); e != nil {
		t.Fatal("BOOTSTRAP_COMMAND_FAILED")
	}
	if datasetsMode {
		run("run", "--rm", "--no-deps", "--user", "0:0", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "control", "initialize-dataset-store", "--config", "/run/projectone/operator.yaml", "--dataset-store-root", "/var/lib/projectone/datasets", "--confirm-empty-dataset-store", "--owner-uid", strconv.Itoa(os.Getuid()), "--owner-gid", strconv.Itoa(os.Getgid()), "--reason", "Initialize isolated current Compose dataset namespace")
	}
	if trainingMode {
		run("run", "--rm", "--no-deps", "--user", "0:0", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "control", "initialize-training-store", "--config", "/run/projectone/operator.yaml", "--training-store-root", "/var/lib/projectone/training", "--confirm-empty-training-store", "--owner-uid", strconv.Itoa(os.Getuid()), "--owner-gid", strconv.Itoa(os.Getgid()), "--reason", "Initialize isolated actual Compose training storage")
	}
	if preparationMode {
		run("run", "--rm", "--no-deps", "--user", "0:0", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "control", "initialize-preparation-store", "--config", "/run/projectone/operator.yaml", "--preparation-store-root", "/var/lib/projectone/preparations", "--confirm-empty-preparation-store", "--owner-uid", strconv.Itoa(os.Getuid()), "--owner-gid", strconv.Itoa(os.Getgid()), "--reason", "Initialize isolated actual Compose preparation storage")
	}
	run("up", "-d", "control")
	roots := x509.NewCertPool()
	caBytes, _ := os.ReadFile(ca)
	roots.AppendCertsFromPEM(caBytes)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Jar: jar, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	ready := func() {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		lastStatus := 0
		lastConnectionError := ""
		for time.Now().Before(deadline) {
			response, e := client.Get(origin + "/health/ready")
			if e != nil {
				lastConnectionError = e.Error()
				if len(lastConnectionError) > 512 {
					lastConnectionError = lastConnectionError[:512]
				}
			}
			if e == nil {
				lastStatus = response.StatusCode
				response.Body.Close()
				if response.StatusCode == 200 {
					return
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
		if logs, e := command("logs", "--no-color", "--tail", "40", "control").CombinedOutput(); e == nil {
			t.Log(strings.ReplaceAll(strings.ReplaceAll(string(logs), pgPassword, "[redacted]"), runtimePassword, "[redacted]"))
		}
		t.Logf("last readiness HTTP status: %d; zero means no verified HTTP response", lastStatus)
		t.Logf("last synthetic readiness connection error: %s", lastConnectionError)
		t.Fatal("COMPOSE_READINESS_TIMEOUT")
	}
	ready()
	csrf, e := client.Get(origin + "/api/v1/auth/csrf")
	if e != nil {
		t.Fatal("COMPOSE_CSRF_FAILED")
	}
	var envelope struct {
		Data struct {
			Token string `json:"token"`
		}
	}
	json.NewDecoder(csrf.Body).Decode(&envelope)
	csrf.Body.Close()
	body, _ := json.Marshal(map[string]any{"email": "admin@example.invalid", "password": password})
	req, _ := http.NewRequest("POST", origin+"/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("X-CSRF-Token", envelope.Data.Token)
	response, e := client.Do(req)
	if e != nil {
		t.Fatal("COMPOSE_LOGIN_TRANSPORT_FAILED")
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("COMPOSE_LOGIN_FAILED")
	}
	expectedPluginModels := []plugins.ModelReference{}
	if modelsMode {
		var licenses [][]byte
		if modelsOnly {
			query := command("exec", "-T", "postgres", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "projectone_migrator", "-d", "projectone", "-tAc", "SELECT deployment_id::text FROM platform_license_state WHERE id=1")
			raw, e := query.Output()
			if e != nil {
				t.Fatal(e)
			}
			deployment := strings.TrimSpace(string(raw))
			if parsed, e := uuid.Parse(deployment); e != nil || parsed.String() != deployment {
				t.Fatal("COMPOSE_LICENSE_SCOPE_REQUIRED")
			}
			now := time.Now().UTC().Truncate(time.Second)
			artifact, e := licensewire.Issue(licensewire.Claims{FormatVersion: 1, LicenseID: uuid.NewString(), CustomerReference: "Synthetic isolated M06 Compose fixture", DeploymentID: deployment, IssuedAt: now.Add(-time.Minute).Format(time.RFC3339), NotBefore: now.Add(-time.Minute).Format(time.RFC3339), ExpiresAt: now.Add(time.Hour).Format(time.RFC3339), GraceUntil: now.Add(2 * time.Hour).Format(time.RFC3339), Scope: licensewire.WholePlatformScope}, composeLicenseKey)
			if e != nil {
				t.Fatal(e)
			}
			licenses = append(licenses, artifact)
		}
		actualModel := exerciseComposeModelImport(t, client, "https://localhost:"+port, origin, password, func() { run("restart", "control"); ready() }, licenses...)
		expectedPluginModels = append(expectedPluginModels, plugins.ModelReference{ID: actualModel.ID, Name: actualModel.Name, Kind: actualModel.Kind, ModelKey: actualModel.ModelKey, CatalogDigest: actualModel.CatalogDigest})
	}
	if datasetsMode {
		exerciseComposeDataset(t, client, origin, password, func() { run("restart", "control"); ready() }, func(job string) {
			if parsed, e := uuid.Parse(job); e != nil || parsed.String() != job {
				t.Fatal("DATASET_JOB_ID_INVALID")
			}
			probe := command("exec", "-T", "postgres", "psql", "-X", "-v", "ON_ERROR_STOP=1", "-U", "projectone_migrator", "-d", "projectone", "-tA")
			probe.Stdin = strings.NewReader("SELECT memory_enforcement FROM dataset_job_attempts WHERE job_id='" + job + "'::uuid ORDER BY attempt_number DESC LIMIT 1;")
			output, e := probe.Output()
			if e != nil || strings.TrimSpace(string(output)) != "linux_rlimit_as_cpu_fsize_wall" {
				t.Fatal("ACTUAL_LINUX_PROCESS_LIMIT_MODE_REQUIRED")
			}
		})
	}

	if trainingMode {
		exerciseComposeTrainingStore(t, client, origin, func() { run("restart", "control"); ready() }, func() {
			run("stop", "control")
			run("run", "--rm", "--no-deps", "control", "verify-training-files", "--config", "/run/projectone/operator.yaml", "--reason", "Verify actual isolated Compose training namespace")
			run("up", "-d", "control")
			ready()
		})
	}
	if preparationMode {
		exerciseComposePreparationStore(t, client, origin, func() { run("restart", "control"); ready() }, func() {
			run("stop", "control")
			run("run", "--rm", "--no-deps", "control", "verify-preparation-files", "--config", "/run/projectone/operator.yaml", "--reason", "Verify actual isolated Compose preparation namespace")
			run("up", "-d", "control")
			ready()
		})
	}
	if knowledgeMode {
		exerciseComposeKnowledge(t, client, origin, password, func() { run("restart", "control"); ready() })
	}
	if workflowMode {
		if !toolOnly {
			profile := ""
			if workflowsOnly {
				profile = "workflows"
			}
			if pluginsOnly {
				profile = "plugins"
				if portalOnly {
					profile = "portal"
					if localizationOnly {
						profile = "localization"
					}
				}
			}
			exerciseComposeWorkflows(t, client, origin, func() { run("restart", "control"); ready() }, profile)
		}
	}
	if pluginMode {
		exerciseComposePlugins(t, client, origin, "https://localhost:"+port, password, pluginCapsule, func() { run("restart", "control"); ready() }, toolOnly, expectedPluginModels)
	}
	if ssoMode {
		exerciseComposeSSOConfiguration(t, client, origin, func() { run("restart", "control"); ready() })
	}
	run("restart", "control")
	ready()
	response, e = client.Get(origin + "/api/v1/me")
	if e != nil {
		t.Fatal("COMPOSE_RESTART_REQUEST_FAILED")
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("COMPOSE_RESTART_LOST_STATE")
	}
	t.Log("declared Docker Desktop Linux arm64 image with explicit local-macos native-volume overlay installed; authenticated SQL readiness, hidden-input bootstrap, runtime-role login and restart passed; Linux host bind ownership remains target-only; only the new labeled test volume was cleaned")
}
