package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
)

// Executables in this suite are explicit test doubles, never product dependencies.
// Positive semantics include external-volume fsync and child startup. Deadline
// and descendant failure cases retain their separate shorter command profile.
const fixtureCommandSeconds = 5
const fixtureFailureCommandSeconds = 2

func fixture(t *testing.T, body string) (configuration.Config, *Client) {
	t.Helper()
	root := t.TempDir()
	os.Chmod(root, 0700)
	mount := root
	for !mountpoint(mount) {
		next := filepath.Dir(mount)
		if next == mount {
			t.Fatal("external mounted TMPDIR required")
		}
		mount = next
	}
	binary := filepath.Join(root, "docker")
	plugin := filepath.Join(root, "docker-compose")
	socket := filepath.Join(root, "socket")
	if os.WriteFile(binary, []byte("#!/bin/sh\n"+body+"\n"), 0700) != nil || os.WriteFile(plugin, []byte("#!/bin/sh\nexit 0\n"), 0700) != nil || os.WriteFile(socket, []byte("explicit metadata-only test reference"), 0600) != nil {
		t.Fatal("test files unavailable")
	}
	c := configuration.Defaults()
	c.Verification.MountRoot = mount
	c.Engine = &configuration.Engine{SocketFile: socket, Profile: "local-macos-amd64-emulation", DialSeconds: 2, RequestSeconds: 5, LoadSeconds: 30, ResponseBytes: 4096, MaximumExpandedImageBytes: 1048576}
	c.Compose = &configuration.Compose{DockerBinary: binary, PluginDirectory: root, CommandSeconds: fixtureCommandSeconds, OutputBytes: 4096}
	client, e := New(c)
	if e != nil {
		t.Fatal(e)
	}
	return c, client
}
func temporaryEntries(t *testing.T, mount string) map[string]bool {
	t.Helper()
	entries, e := os.ReadDir(mount)
	if e != nil {
		t.Fatal(e)
	}
	out := map[string]bool{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".projectone-compose-") {
			out[entry.Name()] = true
		}
	}
	return out
}
func TestComposeVersionIgnoresAmbientContextCredentialsAndProxy(t *testing.T) {
	for _, key := range []string{"DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_HOST", "COMPOSE_FILE", "COMPOSE_PROJECT_NAME", "HTTPS_PROXY", "ALL_PROXY"} {
		t.Setenv(key, "explicit-untrusted-test-value")
	}
	c, client := fixture(t, `[ "$1" = --config ] || exit 71
[ "$3" = --host ] || exit 72
[ "$5" = compose ] || exit 73
[ -z "${DOCKER_CONTEXT:-}${DOCKER_CONFIG:-}${DOCKER_HOST:-}${COMPOSE_FILE:-}${COMPOSE_PROJECT_NAME:-}${HTTPS_PROXY:-}${ALL_PROXY:-}" ] || exit 74
[ "$DOCKER_API_VERSION" = 1.49 ] || exit 75
[ -f "$2/config.json" ] || exit 76
printf '{"version":"v5.5.1"}'`)
	before := temporaryEntries(t, c.Verification.MountRoot)
	version, e := client.Version(context.Background())
	if e != nil || version != "5.5.1" {
		t.Fatal(version, e)
	}
	after := temporaryEntries(t, c.Verification.MountRoot)
	if len(before) != len(after) {
		t.Fatal("temporary client state leaked")
	}
}
func TestComposeChildFailuresBoundsAndRedaction(t *testing.T) {
	for _, fault := range []string{"exit", "malformed", "duplicate", "wrong-version", "stdout-limit", "stderr-limit", "deadline", "descendant"} {
		t.Run(fault, func(t *testing.T) {
			body := `printf '{"version":"v5.5.1"}'`
			switch fault {
			case "exit":
				body = `printf 'secret-test-value' >&2; exit 3`
			case "malformed":
				body = `printf '{'`
			case "duplicate":
				body = `printf '{"version":"v5.5.1","version":"v5.5.1"}'`
			case "wrong-version":
				body = `printf '{"version":"v1.2.3"}'`
			case "stdout-limit":
				body = `while :; do printf 'explicit-long-synthetic-output-value'; done`
			case "stderr-limit":
				body = `while :; do printf 'explicit-long-synthetic-output-value' >&2; done`
			case "deadline":
				body = `sleep 20`
			case "descendant":
				body = `sleep 20 & wait`
			}
			c, client := fixture(t, body)
			client.settings.CommandSeconds = fixtureFailureCommandSeconds
			before := temporaryEntries(t, c.Verification.MountRoot)
			start := time.Now()
			if _, e := client.Version(context.Background()); e == nil || strings.Contains(e.Error(), "secret-test-value") {
				t.Fatal("failure accepted or leaked", e)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("child group not bounded")
			}
			after := temporaryEntries(t, c.Verification.MountRoot)
			if len(before) != len(after) {
				t.Fatal("client config leaked")
			}
		})
	}
}
func TestComposeBinaryAndMountAdmission(t *testing.T) {
	for _, fault := range []string{"changed", "unsafe-binary", "unsafe-parent", "missing-plugin", "nonmount", "missing-config", "cancelled"} {
		t.Run(fault, func(t *testing.T) {
			c, client := fixture(t, `printf '{"version":"v5.5.1"}'`)
			switch fault {
			case "changed":
				os.WriteFile(client.docker.path, []byte("#!/bin/sh\nexit 0\n"), 0700)
			case "unsafe-binary":
				os.Chmod(client.docker.path, 0777)
			case "unsafe-parent":
				os.Chmod(c.Compose.PluginDirectory, 0777)
				defer os.Chmod(c.Compose.PluginDirectory, 0700)
			case "missing-plugin":
				os.Remove(client.plugin.path)
			case "nonmount":
				c.Verification.MountRoot = c.Compose.PluginDirectory
			case "missing-config":
				c.Compose = nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if fault == "cancelled" {
				cancel()
			}
			if fault == "changed" || fault == "unsafe-binary" || fault == "cancelled" {
				if _, e := client.Version(ctx); e == nil {
					t.Fatal("invalid executable used")
				}
			} else {
				if _, e := New(c); e == nil {
					t.Fatal("unsafe client admitted")
				}
			}
		})
	}
}
