package install

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func portalConfig(t *testing.T) Config {
	t.Helper()
	c := validConfig(t)
	c.ComposeFile = filepath.Join("..", "..", "..", "compose", "compose.yaml")
	c.Network.PortalListener = &PortalListenerConfig{BindAddress: "127.0.0.1", HTTPSPort: 19443, ContainerPort: 9443}
	return c
}

func TestPortalListenerRequiresExplicitValidConfiguration(t *testing.T) {
	base := validYAML(t, filepath.Join(shortTempDir(t), "install"))
	for name, group := range map[string]string{
		"null": "null", "empty": "{}", "scalar": "disabled", "sequence": "[]",
		"missing":     "{bind_address: 127.0.0.1, https_port: 19443}",
		"unknown":     "{bind_address: 127.0.0.1, https_port: 19443, container_port: 9443, enabled: true}",
		"port-string": "{bind_address: 127.0.0.1, https_port: '19443', container_port: 9443}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(base + "network:\n  portal_listener: " + group + "\n")); err == nil {
				t.Fatal("invalid optional listener admitted")
			}
		})
	}
	if _, err := Decode([]byte(base + "network: &network\n  portal_listener: null\n")); err == nil {
		t.Fatal("anchored explicit null became omission")
	}
	if _, err := Decode([]byte(base + "<<: {network: {portal_listener: null}}\n")); err == nil {
		t.Fatal("merged explicit null became omission")
	}
	for _, test := range []struct {
		name string
		edit func(*Config)
	}{
		{"bind", func(c *Config) { c.Network.PortalListener.BindAddress = "portal.example.invalid" }},
		{"published-low", func(c *Config) { c.Network.PortalListener.HTTPSPort = 0 }},
		{"published-high", func(c *Config) { c.Network.PortalListener.HTTPSPort = 65536 }},
		{"privileged-target", func(c *Config) { c.Network.PortalListener.ContainerPort = 443 }},
		{"shared-target", func(c *Config) { c.Network.PortalListener.ContainerPort = 8443 }},
		{"target-high", func(c *Config) { c.Network.PortalListener.ContainerPort = 65536 }},
		{"wildcard-collision", func(c *Config) {
			c.Network.PortalListener.BindAddress = "0.0.0.0"
			c.Network.PortalListener.HTTPSPort = c.Network.HTTPSPort
		}},
		{"same-host", func(c *Config) { c.Origins.Portal = "https://admin.ownsolid.test:19443" }},
		{"same-host-case", func(c *Config) { c.Origins.Portal = "https://ADMIN.ownsolid.test.:19443" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := portalConfig(t)
			// Validate requires the absolute source used in real installation inputs.
			c.ComposeFile, _ = filepath.Abs(c.ComposeFile)
			test.edit(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("invalid listener policy admitted")
			}
		})
	}
	c := portalConfig(t)
	c.ComposeFile, _ = filepath.Abs(c.ComposeFile)
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.Network.PortalListener = nil
	raw, _ := json.Marshal(c.Network)
	if bytes.Contains(raw, []byte("PortalListener")) {
		t.Fatal("omission changed the existing pending-upgrade site fingerprint")
	}
}

func TestPortalMaterializationPreservesOtherServicesKeysAndSharedBytes(t *testing.T) {
	c := portalConfig(t)
	canonical, err := os.ReadFile(c.ComposeFile)
	if err != nil {
		t.Fatal(err)
	}
	portal := c.Network.PortalListener
	c.Network.PortalListener = nil
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	keyPaths := []string{"auth-hmac", "mail-keyring.json", "certificate.pem", "private-key.pem", agentCAFile, agentCAKeyFile, gatewayCAFile, gatewayCAKeyFile}
	keys := map[string][]byte{}
	for _, name := range keyPaths {
		keys[name], err = os.ReadFile(filepath.Join(c.Layout().Config, name))
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, split := range []bool{true, true, false} {
		c.Network.PortalListener = nil
		if split {
			c.Network.PortalListener = portal
		}
		if err := Materialize(c); err != nil {
			t.Fatal(err)
		}
		for name, before := range keys {
			after, err := os.ReadFile(filepath.Join(c.Layout().Config, name))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("key changed across listener projection: %s %v", name, err)
			}
		}
		for _, path := range []string{filepath.Join(c.Layout().Config, "control.yaml"), filepath.Join(c.Layout().Operator, "operator.yaml")} {
			raw, err := os.ReadFile(path)
			var value map[string]any
			if err != nil || yaml.Unmarshal(raw, &value) != nil {
				t.Fatalf("generated snapshot unreadable: %s %v", path, err)
			}
			address, present := value["server"].(map[string]any)["portal_listen_address"]
			if present != split || split && address != "0.0.0.0:9443" {
				t.Fatalf("wrong Portal projection: %v", value["server"])
			}
		}
		raw, err := os.ReadFile(c.Layout().ComposeFile)
		if err != nil {
			t.Fatal(err)
		}
		if !split {
			if !bytes.Equal(raw, canonical) {
				t.Fatal("shared serving did not restore canonical source bytes")
			}
			continue
		}
		var before, after map[string]any
		if yaml.Unmarshal(canonical, &before) != nil || yaml.Unmarshal(raw, &after) != nil {
			t.Fatal("Compose projection is not YAML")
		}
		control := after["services"].(map[string]any)["control"].(map[string]any)
		ports := control["ports"].([]any)
		if len(ports) != 2 || !reflect.DeepEqual(ports[1], map[string]any{"target": 9443, "published": "19443", "host_ip": "127.0.0.1", "protocol": "tcp"}) {
			t.Fatalf("unexpected Portal publication: %v", ports)
		}
		control["ports"] = ports[:1]
		if !reflect.DeepEqual(before, after) {
			t.Fatal("projection changed other Compose services or security settings")
		}
		args, err := c.servingArguments(containerConfigDir + "/control.yaml")
		var intent map[string]any
		if err != nil || json.Unmarshal([]byte(args[4]), &intent) != nil || len(intent) != 9 || intent["portal_listen_address"] != "0.0.0.0:9443" {
			t.Fatalf("explicit serving intent lost the new socket: %v %v", intent, err)
		}
	}
}

func TestPortalProjectionRefusesAmbiguousTemplatesBeforeWriting(t *testing.T) {
	c := portalConfig(t)
	raw, err := os.ReadFile(c.ComposeFile)
	if err != nil {
		t.Fatal(err)
	}
	for name, template := range map[string][]byte{
		"missing":         []byte("name: broken\n"),
		"target":          bytes.Replace(raw, []byte("target: 8443"), []byte("target: 8444"), 1),
		"duplicate":       bytes.Replace(raw, []byte("    ports:\n"), []byte("    ports: []\n    ports:\n"), 1),
		"anchored":        bytes.Replace(raw, []byte("    ports:\n"), []byte("    ports: &ports\n"), 1),
		"second-document": append(bytes.Clone(raw), []byte("\n---\nname: other\n")...),
	} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(shortTempDir(t), "not-created")
			c.InstallRoot = root
			c.ComposeFile = filepath.Join(t.TempDir(), "compose.yaml")
			if err := os.WriteFile(c.ComposeFile, template, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := Materialize(c); err == nil || !strings.Contains(err.Error(), "INSTALL_COMPOSE_PORTS_INVALID") {
				t.Fatalf("ambiguous template admitted: %v", err)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatalf("template refusal changed the installation: %v", err)
			}
		})
	}
}

func TestPortalOperationalMaterializationReusesOnlyItsExactProjection(t *testing.T) {
	c := portalConfig(t)
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	c.ComposeFile = c.Layout().ComposeFile
	before, err := os.ReadFile(c.ComposeFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := Materialize(c); err != nil {
		t.Fatalf("operational restore cannot reuse its generated Compose: %v", err)
	}
	after, err := os.ReadFile(c.ComposeFile)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("operational materialization changed the selected projection: %v", err)
	}
	c.Network.PortalListener.HTTPSPort++
	if err := Materialize(c); err == nil || !strings.Contains(err.Error(), "INSTALL_COMPOSE_PORTS_INVALID") {
		t.Fatalf("operational source silently accepted a different publication: %v", err)
	}
}

func TestPortalProbeSelectsThePublishedAddressFamily(t *testing.T) {
	c := portalConfig(t)
	for _, test := range []struct{ bind, origin, want string }{
		{"0.0.0.0", c.Origins.Portal, "127.0.0.1:19443"},
		{"::", c.Origins.Portal, "[::1]:19443"},
		{"::1", c.Origins.Portal, "[::1]:19443"},
	} {
		c.Network.PortalListener.BindAddress = test.bind
		if address, err := c.probeAddress(test.origin); err != nil || address != test.want {
			t.Fatalf("%s: %s %v", test.bind, address, err)
		}
	}
	c.Network.BindAddress = "::"
	if address, err := c.probeAddress(c.Origins.Admin); err != nil || address != "[::1]:8443" {
		t.Fatalf("Admin address: %s %v", address, err)
	}
	if _, err := c.probeAddress("https://unconfigured.example.invalid"); err == nil {
		t.Fatal("unconfigured origin admitted")
	}
}

func TestPortalProbesUseSeparatePublicationsWithVerifiedTLS(t *testing.T) {
	c := portalConfig(t)
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.LoadX509KeyPair(filepath.Join(c.Layout().Config, "certificate.pem"), filepath.Join(c.Layout().Config, "private-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	serve := func(origin string) *httptest.Server {
		t.Helper()
		u, _ := url.Parse(origin)
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != u.Host || r.TLS.ServerName != u.Hostname() {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
		server.StartTLS()
		t.Cleanup(server.Close)
		return server
	}
	admin, portal := serve(c.Origins.Admin), serve(c.Origins.Portal)
	c.Network.HTTPSPort = admin.Listener.Addr().(*net.TCPAddr).Port
	c.Network.PortalListener.HTTPSPort = portal.Listener.Addr().(*net.TCPAddr).Port
	for _, origin := range []string{c.Origins.Admin, c.Origins.Portal} {
		if err := ProbeOrigin(t.Context(), c, origin); err != nil {
			t.Fatalf("verified %s readiness failed: %v", origin, err)
		}
	}
	c.Network.PortalListener.HTTPSPort = c.Network.HTTPSPort
	if err := ProbeOrigin(t.Context(), c, c.Origins.Portal); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("Portal readiness used the wrong audience publication: %v", err)
	}
	foreign := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer foreign.Close()
	c.Network.PortalListener.HTTPSPort = foreign.Listener.Addr().(*net.TCPAddr).Port
	if err := ProbeOrigin(t.Context(), c, c.Origins.Portal); err == nil {
		t.Fatal("untrusted server certificate admitted")
	}
	redirect := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health/ready" {
			http.Redirect(w, r, "/sign-in", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	redirect.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	redirect.StartTLS()
	defer redirect.Close()
	c.Network.HTTPSPort = redirect.Listener.Addr().(*net.TCPAddr).Port
	if err := ProbeOrigin(t.Context(), c, c.Origins.Admin); err == nil || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirected readiness was accepted: %v", err)
	}
}

type listenerRunner func(context.Context, string, ...string) (string, error)

func (r listenerRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return r(ctx, name, args...)
}

func TestPortalCompatibilityRefusalKeepsTheInstalledConfiguration(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(strconv.FormatBool(interrupted), func(t *testing.T) {
			c := portalConfig(t)
			portal := c.Network.PortalListener
			c.Network.PortalListener = nil
			if err := Materialize(c); err != nil {
				t.Fatal(err)
			}
			paths := []string{c.Layout().ComposeFile, c.Layout().ComposeEnv, filepath.Join(c.Layout().Config, "control.yaml"), filepath.Join(c.Layout().Operator, "operator.yaml")}
			before := make(map[string][]byte)
			for _, path := range paths {
				var err error
				before[path], err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			c.Network.PortalListener = portal
			calls := 0
			admitted := false
			runner := listenerRunner(func(_ context.Context, name string, args ...string) (string, error) {
				line := name + " " + strings.Join(args, " ")
				switch {
				case strings.Contains(line, "ps --all --quiet control"):
					if interrupted {
						return "", nil
					}
					return strings.Repeat("a", 64), nil
				case strings.Contains(line, "inspect --type container"):
					raw, _ := json.Marshal(map[string]string{"Image": "sha256:" + strings.Repeat("b", 64), "Project": c.Project, "Service": "control", "ConfigFile": c.Layout().ComposeFile})
					return string(raw), nil
				case strings.Contains(line, "image inspect"):
					return "sha256:" + strings.Repeat("b", 64), nil
				case strings.Contains(line, "verify-serving-transport"):
					calls++
					for n, arg := range args {
						if arg == "--config" {
							directory := c.Layout().Operator
							if strings.HasPrefix(args[n+1], containerConfigDir+"/") {
								directory = c.Layout().Config
							}
							raw, err := os.ReadFile(filepath.Join(directory, filepath.Base(args[n+1])))
							if err != nil || !bytes.Contains(raw, []byte("portal_listen_address: 0.0.0.0:9443")) {
								t.Fatalf("desired snapshot missing: %v", err)
							}
						}
					}
					if admitted {
						return servingTransportSuccess + "\n", nil
					}
					return "SERVING_TRANSPORT_EXPECTATION_INVALID\n", errors.New("old image rejects optional intent")
				case strings.Contains(line, "stop control"), strings.Contains(line, "up --detach"), strings.Contains(line, " migrate "):
					if !admitted {
						t.Fatalf("listener refusal crossed the writer boundary: %s", line)
					}
				}
				return successOutput(line), nil
			})
			var out bytes.Buffer
			if err := testInstaller(c, runner, &out).apply(t.Context()); err == nil || !strings.Contains(err.Error(), "listener-compatibility") || calls != 1 {
				t.Fatalf("old source admission was bypassed: calls=%d %v", calls, err)
			}
			for path, original := range before {
				raw, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(raw, original) {
					t.Fatalf("refusal replaced installed files: %s %v", path, err)
				}
			}
			for _, pattern := range []string{"serving-image-*", "serving-intent-*"} {
				files, err := filepath.Glob(filepath.Join(c.Layout().Operator, pattern))
				if err != nil || len(files) != 0 {
					t.Fatalf("temporary serving inputs remain: %v %v", files, err)
				}
			}
			admitted = true
			if err := testInstaller(c, runner, &out).apply(t.Context()); err != nil || calls != 3 {
				t.Fatalf("explicit retry did not check desired and generated snapshots: calls=%d %v", calls, err)
			}
		})
	}
}

func TestPortalCompatibilityRefusesForeignOrAmbiguousContainerIdentity(t *testing.T) {
	c := portalConfig(t)
	for _, test := range []struct{ name, ids, project, service, config, image string }{
		{"foreign-project", strings.Repeat("a", 64), "other", "control", c.Layout().ComposeFile, "sha256:" + strings.Repeat("b", 64)},
		{"foreign-service", strings.Repeat("a", 64), c.Project, "postgres", c.Layout().ComposeFile, "sha256:" + strings.Repeat("b", 64)},
		{"foreign-source", strings.Repeat("a", 64), c.Project, "control", "/other/compose.yaml", "sha256:" + strings.Repeat("b", 64)},
		{"mutable-image", strings.Repeat("a", 64), c.Project, "control", c.Layout().ComposeFile, "ownsolid/control:1"},
		{"ambiguous", strings.Repeat("a", 64) + "\n" + strings.Repeat("b", 64), c.Project, "control", c.Layout().ComposeFile, "sha256:" + strings.Repeat("b", 64)},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := listenerRunner(func(_ context.Context, _ string, args ...string) (string, error) {
				if strings.Contains(strings.Join(args, " "), "ps --all --quiet") {
					return test.ids, nil
				}
				raw, _ := json.Marshal(map[string]string{"Project": test.project, "Service": test.service, "ConfigFile": test.config, "Image": test.image})
				return string(raw), nil
			})
			if _, err := (&installer{config: c, runner: runner}).listenerSourceImage(t.Context()); err == nil {
				t.Fatal("unowned or ambiguous Control image admitted")
			}
		})
	}
}

func TestPortalSelectionDoesNotMigrateAnExistingAdvertisedOrigin(t *testing.T) {
	c := portalConfig(t)
	portal := c.Network.PortalListener
	c.Network.PortalListener = nil
	if err := Materialize(c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(c.Layout().Config, "control.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	c.Network.PortalListener = portal
	c.Origins.Portal = "https://portal.ownsolid.test:19443"
	runner := &fakeRunner{}
	err = (&installer{config: c, runner: runner}).listenerCompatibility(t.Context())
	if err == nil || !strings.HasPrefix(err.Error(), "INSTALL_LISTENER_ADDRESS_CHANGE_REQUIRED") || len(runner.commands) != 0 {
		t.Fatalf("listener selection bypassed address migration: %v %v", err, runner.commands)
	}
	after, err := os.ReadFile(filepath.Join(c.Layout().Config, "control.yaml"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("address refusal changed the installed snapshot: %v", err)
	}
}

func TestPortalPreflightChecksBothPublicationsInOneDockerSnapshot(t *testing.T) {
	listen := func() net.Listener {
		t.Helper()
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		return listener
	}
	admin, portal := listen(), listen()
	c := portalConfig(t)
	c.Network.HTTPSPort = admin.Addr().(*net.TCPAddr).Port
	c.Network.PortalListener.HTTPSPort = portal.Addr().(*net.TCPAddr).Port
	for _, foreign := range []bool{false, true} {
		calls := 0
		runner := &fakeRunner{answer: func(line string) (string, error) {
			calls++
			if strings.Contains(line, " ps ") {
				return "admin-id\nportal-id\n", nil
			}
			var answer strings.Builder
			for n, port := range []int{c.Network.HTTPSPort, c.Network.PortalListener.HTTPSPort} {
				owner := portOwner{Project: c.Project, Service: "control", ConfigFile: c.Layout().ComposeFile,
					Ports: map[string][]portPublication{"8443/tcp": {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(port)}}}}
				if n == 1 && foreign {
					owner.Project = "other"
				}
				raw, _ := json.Marshal(owner)
				answer.Write(raw)
				answer.WriteByte('\n')
			}
			return answer.String(), nil
		}}
		err := checkPublishedPort(t.Context(), c, runner)
		if calls != 2 || (err != nil) != foreign || foreign && !strings.Contains(err.Error(), "network.portal_listener.https_port") {
			t.Fatalf("foreign=%t calls=%d: %v", foreign, calls, err)
		}
	}
}

func TestPortalPreflightReleasesItsFirstReservationOnSecondFailure(t *testing.T) {
	c := portalConfig(t)
	first, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	c.Network.HTTPSPort = first.Addr().(*net.TCPAddr).Port
	second, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	defer second.Close()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	c.Network.PortalListener.HTTPSPort = second.Addr().(*net.TCPAddr).Port
	if err := checkPublishedPort(t.Context(), c, &fakeRunner{}); err == nil {
		t.Fatal("occupied Portal host port admitted")
	}
	probe, err := net.Listen("tcp4", net.JoinHostPort(c.Network.BindAddress, strconv.Itoa(c.Network.HTTPSPort)))
	if err != nil {
		t.Fatalf("first publication reservation leaked: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
}
