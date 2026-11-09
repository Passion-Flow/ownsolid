package install

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const catalogFixtureImage = "sha256:45dc9bef07c0c404255790e41f2c22c62ff55a690a1d556e0f9cb6f6e2556f1b"

const catalogSourceFixture = `services:
  worker:
    image: ` + catalogFixtureImage + `
    platform: linux/amd64
    pull_policy: never
    network_mode: none
    entrypoint: ["/bin/echo"]
    command: ["fixture"]
    environment:
      APP_MODE: "offline"
    read_only: true
`

const catalogModelFixture = `{"name":"lane-del-catalog","services":{"worker":{"command":["fixture"],"entrypoint":["/bin/echo"],"environment":{"APP_MODE":"offline"},"image":"` +
	catalogFixtureImage + `","network_mode":"none","platform":"linux/amd64","pull_policy":"never","read_only":true}}}`

func catalogWriteFixture(t *testing.T, name, value string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, []byte(value), mode); err != nil {
		t.Fatal(err)
	}
}

func catalogStageFixture(t *testing.T) (*os.Root, CatalogComposePolicy) {
	t.Helper()
	created, err := os.MkdirTemp("", "ownsolid-catalog-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(created) })
	base, err := filepath.EvalSymlinks(created)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "source")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	catalogWriteFixture(t, filepath.Join(dir, catalogComposeArtifact), catalogSourceFixture, 0o600)
	tool := filepath.Join(base, "compose")
	catalogWriteFixture(t, tool, "protected Compose fixture", 0o700)
	socket := filepath.Join(base, "engine.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return root, CatalogComposePolicy{
		Executable: tool, ExecutableSHA256: catalogSHA256([]byte("protected Compose fixture")),
		Workspace: base, DockerSocket: socket, Project: "lane-del-catalog", Timeout: 5 * time.Second,
	}
}

func TestCatalogComposeRefusesExternalInputsBeforeRender(t *testing.T) {
	root, policy := catalogStageFixture(t)
	source := filepath.Join(root.Name(), catalogComposeArtifact)
	refused := []struct {
		name, source string
	}{
		{"include", "include: [oci://registry.invalid/profile]\n" + catalogSourceFixture},
		{"env file", catalogSourceFixture + "    env_file: [/private/site.env]\n"},
		{"label file", catalogSourceFixture + "    label_file: [/private/site.labels]\n"},
		{"host mount", catalogSourceFixture + "    volumes: [/root:/outside]\n"},
		{"secret", catalogSourceFixture + "secrets: {site: {file: /private/site-secret}}\n"},
		{"privilege", catalogSourceFixture + "    privileged: true\n"},
		{"provider", catalogSourceFixture + "    provider: {type: echo}\n"},
		{"build", catalogSourceFixture + "    build: .\n"},
		{"extends", catalogSourceFixture + "    extends: {file: other.yaml, service: worker}\n"},
		{"profiles", catalogSourceFixture + "    profiles: [hidden]\n"},
		{"alias", strings.Replace(catalogSourceFixture, "worker:", "worker: &worker", 1) + "  second: *worker\n"},
		{"tag", strings.Replace(catalogSourceFixture, "command:", "command: !override", 1)},
		{"multiple documents", catalogSourceFixture + "---\n" + catalogSourceFixture},
		{"duplicate", catalogSourceFixture + "    network_mode: host\n"},
		{"interpolation", strings.Replace(catalogSourceFixture, "offline", "${PRIVATE_ENV}", 1)},
		{"implicit host env", strings.Replace(catalogSourceFixture, "APP_MODE: \"offline\"", "APP_MODE:", 1)},
		{"unscoped network", strings.Replace(catalogSourceFixture, "network_mode: none", "network_mode: bridge", 1)},
		{"mutable image", strings.Replace(catalogSourceFixture, catalogFixtureImage, "registry.invalid/app:v1", 1)},
		{"pull enabled", strings.Replace(catalogSourceFixture, "pull_policy: never", "pull_policy: always", 1)},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			catalogWriteFixture(t, source, tc.source, 0o600)
			stage, err := stageCatalogCompose(t.Context(), root, catalogSHA256([]byte(tc.source)), policy, func(*exec.Cmd) error {
				t.Fatal("disallowed input reached the renderer")
				return nil
			})
			if stage != nil || !errors.Is(err, errCatalogCompose) {
				t.Fatalf("want input refusal, got %v", err)
			}
		})
	}
}

func TestCatalogComposeBindsSourceAndModelBeforeLaunch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*testing.T, *StagedCatalogCompose, *CatalogComposeBinding)
		want     error
		launcher bool
	}{
		{"exact private model", func(_ *testing.T, stage *StagedCatalogCompose, _ *CatalogComposeBinding) {
			stage.Rendered()[0] = '!'
		}, nil, true},
		{"wrong artifact digest", func(_ *testing.T, _ *StagedCatalogCompose, binding *CatalogComposeBinding) {
			binding.ArtifactSHA256 = catalogSHA256([]byte("another release"))
		}, errCatalogModel, false},
		{"changed model bytes", func(_ *testing.T, stage *StagedCatalogCompose, _ *CatalogComposeBinding) {
			stage.model = bytes.Replace(stage.model, []byte("fixture"), []byte("changed"), 1)
		}, errCatalogModel, false},
		{"same-inode source change", func(t *testing.T, stage *StagedCatalogCompose, _ *CatalogComposeBinding) {
			catalogWriteFixture(t, filepath.Join(stage.root.Name(), catalogComposeArtifact),
				strings.Replace(catalogSourceFixture, "fixture", "changed", 1), 0o600)
		}, errCatalogSource, false},
		{"replacement inode with identical bytes", func(t *testing.T, stage *StagedCatalogCompose, _ *CatalogComposeBinding) {
			other := filepath.Join(stage.root.Name(), "replacement.yaml")
			catalogWriteFixture(t, other, catalogSourceFixture, 0o600)
			if err := os.Rename(other, filepath.Join(stage.root.Name(), catalogComposeArtifact)); err != nil {
				t.Fatal(err)
			}
		}, errCatalogSource, false},
		{"symlink source", func(t *testing.T, stage *StagedCatalogCompose, _ *CatalogComposeBinding) {
			source := filepath.Join(stage.root.Name(), catalogComposeArtifact)
			other := filepath.Join(stage.root.Name(), "replacement.yaml")
			if err := os.Rename(source, other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, source); err != nil {
				t.Fatal(err)
			}
		}, errCatalogSource, false},
		{"changed executable bytes", func(t *testing.T, stage *StagedCatalogCompose, _ *CatalogComposeBinding) {
			catalogWriteFixture(t, stage.policy.Executable, "different Compose bytes", 0o700)
		}, errCatalogRuntime, false},
		{"replacement executable name stays pinned", func(t *testing.T, stage *StagedCatalogCompose, _ *CatalogComposeBinding) {
			old := stage.policy.Executable + ".previous"
			if err := os.Rename(stage.policy.Executable, old); err != nil {
				t.Fatal(err)
			}
			catalogWriteFixture(t, stage.policy.Executable, "different Compose bytes", 0o700)
		}, nil, true},
		{"launcher failure", func(_ *testing.T, _ *StagedCatalogCompose, _ *CatalogComposeBinding) {}, errCatalogLaunch, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, policy := catalogStageFixture(t)
			calls := 0
			stage, err := stageCatalogCompose(t.Context(), root, catalogSHA256([]byte(catalogSourceFixture)), policy, func(command *exec.Cmd) error {
				calls++
				input, err := io.ReadAll(command.Stdin)
				if err != nil {
					t.Fatal(err)
				}
				if command.Path != "/proc/self/fd/3" || len(command.ExtraFiles) != 1 ||
					!slices.Equal(command.Args[1:11], []string{"--ansi", "never", "--project-name", policy.Project,
						"--project-directory", command.Dir, "--env-file", os.DevNull, "-f", "-"}) {
					t.Fatal("Compose must use the pinned binary, explicit project and stdin alone")
				}
				if calls == 1 {
					if string(input) != catalogSourceFixture || slices.Contains(command.Env, "DOCKER_HOST=unix://"+policy.DockerSocket) {
						t.Fatal("renderer did not use the signed source in an isolated context")
					}
					_, err := io.WriteString(command.Stdout, catalogModelFixture)
					return err
				}
				if string(input) != catalogModelFixture ||
					!slices.Equal(command.Args[11:], []string{"up", "--detach", "--pull", "never", "--no-build"}) ||
					len(command.Env) != 4 || !slices.Contains(command.Env, "DOCKER_HOST=unix://"+policy.DockerSocket) {
					t.Fatal("launcher did not receive the exact admitted model and restricted execution context")
				}
				if tc.want == errCatalogLaunch {
					return errors.New("fixture launch failed")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = stage.Close() })
			binding := CatalogComposeBinding{ArtifactSHA256: catalogSHA256([]byte(catalogSourceFixture)), ModelSHA256: catalogSHA256([]byte(catalogModelFixture))}
			tc.change(t, stage, &binding)
			if err := stage.Start(t.Context(), binding); !errors.Is(err, tc.want) || (calls == 2) != tc.launcher {
				t.Fatalf("start: %v, calls %d, expected %v, launcher %t", err, calls, tc.want, tc.launcher)
			}
		})
	}
}

func TestCatalogComposeRefusesUntrustedSourceAndRenderOutput(t *testing.T) {
	root, policy := catalogStageFixture(t)
	sourceDigest := catalogSHA256([]byte(catalogSourceFixture))
	for _, tc := range []struct {
		name, output string
	}{
		{"changed command", strings.Replace(catalogModelFixture, "fixture", "changed", 1)},
		{"different project", strings.Replace(catalogModelFixture, policy.Project, "other-project", 1)},
		{"extra property", strings.Replace(catalogModelFixture, `"read_only":true`, `"read_only":true,"privileged":true`, 1)},
		{"duplicate", strings.Replace(catalogModelFixture, `"read_only":true`, `"read_only":true,"read_only":false`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage, err := stageCatalogCompose(t.Context(), root, sourceDigest, policy, func(command *exec.Cmd) error {
				_, err := io.WriteString(command.Stdout, tc.output)
				return err
			})
			if stage != nil || !errors.Is(err, errCatalogModel) {
				t.Fatalf("want model refusal, got %v", err)
			}
		})
	}
	t.Run("output limit applies to io.Copy", func(t *testing.T) {
		stage, err := stageCatalogCompose(t.Context(), root, sourceDigest, policy, func(command *exec.Cmd) error {
			_, err := io.Copy(command.Stdout, strings.NewReader(strings.Repeat("x", maxCatalogComposeBytes+1)))
			return err
		})
		if stage != nil || !errors.Is(err, errCatalogRender) {
			t.Fatalf("want output limit refusal, got %v", err)
		}
	})
	noCommand := func(*exec.Cmd) error {
		t.Fatal("unverified input reached Compose")
		return nil
	}
	t.Run("wrong digest before rendering", func(t *testing.T) {
		_, err := stageCatalogCompose(t.Context(), root, catalogSHA256([]byte("previous artifact")), policy, noCommand)
		if !errors.Is(err, errCatalogSource) {
			t.Fatal(err)
		}
	})
	t.Run("wrong executable digest", func(t *testing.T) {
		bad := policy
		bad.ExecutableSHA256 = catalogSHA256([]byte("another executable"))
		_, err := stageCatalogCompose(t.Context(), root, sourceDigest, bad, noCommand)
		if !errors.Is(err, errCatalogRuntime) {
			t.Fatal(err)
		}
	})
	t.Run("hardlink source", func(t *testing.T) {
		if err := os.Link(filepath.Join(root.Name(), catalogComposeArtifact), filepath.Join(root.Name(), "linked.yaml")); err != nil {
			t.Fatal(err)
		}
		_, err := stageCatalogCompose(t.Context(), root, sourceDigest, policy, noCommand)
		if !errors.Is(err, errCatalogSource) {
			t.Fatal(err)
		}
	})
}

func TestCatalogComposeDisconnectedNativeRenderer(t *testing.T) {
	compose := os.Getenv("OWNSOLID_CATALOG_COMPOSE_TEST_BINARY")
	pin := os.Getenv("OWNSOLID_CATALOG_COMPOSE_TEST_SHA256")
	if compose == "" || pin == "" {
		t.Skip("requires a pinned native Compose executable in a disconnected Linux namespace")
	}
	root, policy := catalogStageFixture(t)
	policy.Executable, policy.ExecutableSHA256 = compose, pin
	policy.Timeout = 20 * time.Second
	t.Setenv("COMPOSE_FILE", "https://registry.invalid/other.yaml")
	t.Setenv("DOCKER_HOST", "tcp://192.0.2.22:2375")
	t.Setenv("APP_MODE", "wrong")
	catalogWriteFixture(t, filepath.Join(root.Name(), ".env"), "COMPOSE_FILE=https://registry.invalid/from-dotenv\n", 0o600)
	sourceDigest := catalogSHA256([]byte(catalogSourceFixture))
	rendered, err := StageCatalogCompose(t.Context(), root, sourceDigest, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer rendered.Close()
	want := rendered.Rendered()
	calls := 0
	rendered.run = func(command *exec.Cmd) error {
		calls++
		raw, err := io.ReadAll(command.Stdin)
		if err != nil || !bytes.Equal(want, raw) {
			t.Fatal("launch did not get the native renderer's exact bytes")
		}
		return nil
	}
	binding := CatalogComposeBinding{ArtifactSHA256: sourceDigest, ModelSHA256: catalogSHA256(want)}
	if err := rendered.Start(t.Context(), binding); err != nil || calls != 1 {
		t.Fatalf("native renderer or guarded launch failed: %v", err)
	}
	t.Log("native Compose rendered the signed source with isolated context and the launcher received its exact JSON")
	// Cancelled and changed operations must not contact the launcher again.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := rendered.Start(ctx, binding); err == nil || calls != 1 {
		t.Fatal("cancelled operation reached the launcher")
	}
	catalogWriteFixture(t, filepath.Join(root.Name(), catalogComposeArtifact), catalogSourceFixture+"    env_file: [outside.env]\n", 0o600)
	if err := rendered.Start(t.Context(), binding); !errors.Is(err, errCatalogSource) || calls != 1 {
		t.Fatalf("changed source reached startup: %v", err)
	}
}
