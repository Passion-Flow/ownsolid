//go:build helm_integration

package helm_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
)

const nodeJoinPolicyName = "custom-node-join-policy.env"

func nodeJoinPolicyFiles(t *testing.T) string {
	t.Helper()
	root, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(root, 0700) != nil {
		t.Fatal("private policy test root")
	}
	private := filepath.Join(root, "private")
	if os.Mkdir(private, 0700) != nil {
		t.Fatal("private policy directory")
	}
	var c config.Config
	c.Server.ListenAddress = "127.0.0.1:19443"
	c.Server.PortalOrigin = "https://portal.example.invalid"
	c.Server.AdminOrigin = "https://admin.example.invalid"
	c.Database.MaxOpenConnections, c.Database.MaxIdleConnections, c.Database.StatementTimeoutSeconds = 4, 1, 5
	c.SMTP.Host, c.SMTP.Port, c.SMTP.FromAddress = "smtp.example.invalid", 465, "platform@example.invalid"
	c.SMTP.TLSMode, c.SMTP.ConnectTimeoutSeconds, c.SMTP.OperationTimeoutSeconds = "implicit_tls", 5, 20
	p := config.DefaultRuntimePolicy()
	p.Nodes.MaximumLiveJoinTokens = 5
	p.Nodes.MaximumLiveNodes = 13
	c.Runtime = &p
	raw, e := yaml.Marshal(c)
	if e != nil || os.WriteFile(filepath.Join(private, "control.yaml"), raw, 0600) != nil || os.WriteFile(filepath.Join(private, nodeJoinPolicyName), []byte("PROJECT_ONE_NODE_MAXIMUM_LIVE_JOIN_TOKENS=7\nPROJECT_ONE_NODE_MAXIMUM_LIVE_NODES=11\n"), 0600) != nil {
		t.Fatal("actual policy files")
	}
	t.Setenv("PROJECT_ONE_NODE_MAXIMUM_LIVE_JOIN_TOKENS", "999")
	t.Setenv("PROJECT_ONE_NODE_MAXIMUM_LIVE_NODES", "999")
	return root
}
func nodeJoinPolicyLoad(t *testing.T, configuration, policy string, expected, expectedLiveNodes int) {
	t.Helper()
	c, e := config.LoadWithEnvironment(configuration, policy)
	if e != nil || c.Policy().Nodes.MaximumLiveJoinTokens != expected || c.Policy().Nodes.MaximumLiveNodes != expectedLiveNodes {
		t.Fatal("rendered actual typed capacity mismatch", e)
	}
}
func nodeJoinPolicyUnsafeFiles(t *testing.T, root string) {
	t.Helper()
	configuration, policy := filepath.Join(root, "private", "control.yaml"), filepath.Join(root, "private", nodeJoinPolicyName)
	for _, body := range []string{"PROJECT_ONE_NODE_MAXIMUM_LIVE_JOIN_TOKENS=0\n", "PROJECT_ONE_NODE_MAXIMUM_LIVE_JOIN_TOKENS=SYNTHETIC_PRIVATE_NODE_POLICY_VALUE\n",
		"PROJECT_ONE_NODE_MAXIMUM_LIVE_NODES=0\n", "PROJECT_ONE_NODE_MAXIMUM_LIVE_NODES=10001\n", "PROJECT_ONE_NODE_MAXIMUM_LIVE_NODES=SYNTHETIC_PRIVATE_NODE_POLICY_VALUE\n"} {
		if os.WriteFile(policy, []byte(body), 0600) != nil {
			t.Fatal("invalid private test policy")
		}
		if _, e := config.LoadWithEnvironment(configuration, policy); e == nil || strings.Contains(e.Error(), "SYNTHETIC_PRIVATE") {
			t.Fatal("invalid policy accepted or private value exposed")
		}
	}
	if os.WriteFile(policy, []byte("PROJECT_ONE_NODE_MAXIMUM_LIVE_JOIN_TOKENS=7\nPROJECT_ONE_NODE_MAXIMUM_LIVE_NODES=11\n"), 0600) != nil || os.Chmod(policy, 0644) != nil {
		t.Fatal("unsafe mode test policy")
	}
	if _, e := config.LoadWithEnvironment(configuration, policy); e == nil {
		t.Fatal("public policy accepted")
	}
	if os.Chmod(policy, 0600) != nil || os.Remove(policy) != nil {
		t.Fatal("missing policy fixture")
	}
	if _, e := config.LoadWithEnvironment(configuration, policy); e == nil {
		t.Fatal("missing policy accepted")
	}
}

func TestNodeJoinPolicyHelmRenderedPrivateOverride(t *testing.T) {
	for _, selected := range []bool{false, true} {
		root := nodeJoinPolicyFiles(t)
		v := values(t)
		set(v, "configuration.privateMountPath", root)
		expected := []any{"serve", "--config", filepath.Join(root, "private", "control.yaml")}
		if selected {
			set(v, "configuration.policyEnvironmentFile", nodeJoinPolicyName)
			set(v, "secrets.files", append(array(t, at(t, v, "secrets", "files")), nodeJoinPolicyName))
			expected = append(expected, "--env-file", filepath.Join(root, "private", nodeJoinPolicyName))
		}
		run(t, v, true, "lint")
		docs := documents(t, run(t, v, true, "template"))
		validateOfficial(t, docs)
		pod := at(t, docs["Deployment"], "spec", "template", "spec")
		control := array(t, at(t, pod, "containers"))[0]
		args := array(t, at(t, control, "args"))
		if !reflect.DeepEqual(args, expected) {
			t.Fatal("actual Helm policy argv changed")
		}
		for _, entry := range array(t, at(t, control, "env")) {
			if strings.HasPrefix(at(t, entry, "name").(string), "PROJECT_ONE_NODE_") {
				t.Fatal("private policy became ambient environment")
			}
		}
		init := array(t, at(t, pod, "initContainers"))[0]
		initArgs := array(t, at(t, init, "args"))
		declared := false
		for n, arg := range initArgs {
			if arg == "--secret-files-json" {
				var files []string
				if n+1 >= len(initArgs) || json.Unmarshal([]byte(initArgs[n+1].(string)), &files) != nil {
					t.Fatal("actual init file declaration")
				}
				for _, file := range files {
					declared = declared || file == nodeJoinPolicyName
				}
			}
		}
		if declared != selected {
			t.Fatal("actual init omitted or exposed selected policy")
		}
		policy, capacity, liveNodes := "", 5, 13
		if selected {
			policy, capacity = args[4].(string), 7
			liveNodes = 11
		}
		nodeJoinPolicyLoad(t, args[2].(string), policy, capacity, liveNodes)
		if selected {
			nodeJoinPolicyUnsafeFiles(t, root)
		}
	}
}

func TestNodeJoinPolicyComposeRenderedPrivateOverride(t *testing.T) {
	root := nodeJoinPolicyFiles(t)
	private := filepath.Join(root, "private")
	selectedContext := os.Getenv("PROJECT_ONE_FIXTURE_DOCKER_CONTEXT")
	if selectedContext == "" {
		t.Fatal("explicit isolated Docker context required")
	}
	environment := []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "PROJECT_ONE_") {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, "PROJECT_ONE_COMPOSE_PROJECT=projectone-node-join-render", "PROJECT_ONE_DATA_ROOT="+filepath.Join(root, "render-data"), "PROJECT_ONE_CONFIG_ROOT="+private, "PROJECT_ONE_CONTROL_IMAGE=projectone/control@sha256:"+strings.Repeat("a", 64), "PROJECT_ONE_PLATFORM=linux/amd64", "PROJECT_ONE_RUNTIME_UID="+strconv.Itoa(os.Geteuid()), "PROJECT_ONE_RUNTIME_GID="+strconv.Itoa(os.Getegid()))
	compose := func(name string, success bool) map[string]any {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "docker", "--context", selectedContext, "compose", "--env-file", "/dev/null", "-f", filepath.Join(repository(t), "deploy/compose/base/compose.yaml"), "-f", filepath.Join(repository(t), "deploy/compose/base/workspace-policy.yaml"), "config", "--format", "json")
		command.Env = append(append([]string{}, environment...), "PROJECT_ONE_POLICY_ENVIRONMENT_BASENAME="+name)
		raw, e := command.Output()
		if !success {
			if e == nil {
				t.Fatal("missing Compose private policy accepted")
			}
			return nil
		}
		var doc map[string]any
		if e != nil || json.Unmarshal(raw, &doc) != nil {
			t.Fatal("actual isolated Compose config failed")
		}
		return doc
	}
	doc := compose(nodeJoinPolicyName, true)
	control := at(t, doc, "services", "control")
	args := array(t, at(t, control, "command"))
	if !reflect.DeepEqual(args, []any{"serve", "--config", "/run/projectone/control.yaml", "--env-file", "/run/projectone/" + nodeJoinPolicyName}) {
		t.Fatal("actual Compose policy argv")
	}
	found := false
	for _, mount := range array(t, at(t, control, "volumes")) {
		if at(t, mount, "target") == "/run/projectone" {
			found = at(t, mount, "type") == "bind" && at(t, mount, "source") == private && at(t, mount, "read_only") == true && at(t, mount, "bind", "create_host_path") == false
		}
	}
	if !found {
		t.Fatal("actual Compose protected bind mapping changed")
	}
	if environment, exists := object(t, control)["environment"]; exists {
		for key := range object(t, environment) {
			if strings.HasPrefix(key, "PROJECT_ONE_NODE_") {
				t.Fatal("Compose exposed private capacity through ambient environment")
			}
		}
	}

	mapPath := func(value string) string {
		const prefix = "/run/projectone/"
		if !strings.HasPrefix(value, prefix) {
			t.Fatal("unexpected rendered private path")
		}
		return private + "/" + strings.TrimPrefix(value, prefix)
	}
	nodeJoinPolicyLoad(t, mapPath(args[2].(string)), mapPath(args[4].(string)), 7, 11)
	compose("", false)
	if os.WriteFile(filepath.Join(root, "escape.env"), []byte("PROJECT_ONE_NODE_MAXIMUM_LIVE_JOIN_TOKENS=7\n"), 0600) != nil {
		t.Fatal("owned escape test fixture")
	}
	unsafe := array(t, at(t, compose("../escape.env", true), "services", "control", "command"))
	if _, e := config.LoadWithEnvironment(mapPath(unsafe[2].(string)), mapPath(unsafe[4].(string))); e == nil {
		t.Fatal("actual serving policy loader allowed rendered traversal")
	}
	nodeJoinPolicyUnsafeFiles(t, root)
}
