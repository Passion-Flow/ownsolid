// Package compose runs the explicit local offline CLI subset without shared client state.
package compose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type executable struct {
	path   string
	info   os.FileInfo
	digest string
}
type Client struct {
	settings configuration.Compose
	mount    string
	socket   string
	docker   executable
	plugin   executable
}
type Observation struct {
	Status        string             `json:"status"`
	Version       string             `json:"version"`
	DockerSHA256  string             `json:"docker_sha256"`
	PluginSHA256  string             `json:"plugin_sha256"`
	Engine        engine.Observation `json:"engine"`
	Qualification string             `json:"qualification"`
}

func failure(code string) error { return errors.New(code) }
func owner(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && (s.Uid == 0 || int(s.Uid) == os.Geteuid())
}
func mountpoint(path string) bool {
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != path || path == "/" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	var a, b syscall.Stat_t
	return syscall.Stat(path, &a) == nil && syscall.Stat(filepath.Dir(path), &b) == nil && a.Dev != b.Dev
}
func binary(path string) (executable, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return executable{}, failure("OFFLINE_COMPOSE_EXPLICIT_BINARY_REQUIRED")
	}
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil {
		return executable{}, failure("OFFLINE_COMPOSE_BINARY_UNAVAILABLE")
	}
	f, e := os.Open(resolved)
	if e != nil {
		return executable{}, failure("OFFLINE_COMPOSE_BINARY_UNAVAILABLE")
	}
	defer f.Close()
	before, e := f.Stat()
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0022 != 0 || before.Mode().Perm()&0100 == 0 || !owner(before) {
		return executable{}, failure("OFFLINE_COMPOSE_BINARY_OWNER_OR_MODE_INVALID")
	}
	parent, e := os.Stat(filepath.Dir(resolved))
	if e != nil || !parent.IsDir() || parent.Mode().Perm()&0022 != 0 || !owner(parent) {
		return executable{}, failure("OFFLINE_COMPOSE_BINARY_PARENT_UNSAFE")
	}
	hash := sha256.New()
	if _, e = io.CopyBuffer(hash, f, make([]byte, 65536)); e != nil {
		return executable{}, failure("OFFLINE_COMPOSE_BINARY_READ_FAILED")
	}
	after, e := os.Stat(resolved)
	if e != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		return executable{}, failure("OFFLINE_COMPOSE_BINARY_CHANGED")
	}
	return executable{resolved, before, hex.EncodeToString(hash.Sum(nil))}, nil
}
func (b executable) check() error {
	i, e := os.Stat(b.path)
	if e != nil || !os.SameFile(b.info, i) || b.info.Size() != i.Size() || b.info.ModTime() != i.ModTime() || b.info.Mode() != i.Mode() || !owner(i) {
		return failure("OFFLINE_COMPOSE_BINARY_CHANGED")
	}
	return nil
}
func New(c configuration.Config) (*Client, error) {
	if c.Compose == nil || c.Engine == nil {
		return nil, failure("OFFLINE_COMPOSE_EXPLICIT_CONFIGURATION_REQUIRED")
	}
	if e := c.Compose.Validate(); e != nil {
		return nil, e
	}
	if e := c.Engine.Validate(); e != nil {
		return nil, e
	}
	if !mountpoint(c.Verification.MountRoot) {
		return nil, failure("OFFLINE_COMPOSE_GENUINE_EXTERNAL_MOUNT_REQUIRED")
	}
	docker, e := binary(c.Compose.DockerBinary)
	if e != nil {
		return nil, e
	}
	plugin, e := binary(filepath.Join(c.Compose.PluginDirectory, "docker-compose"))
	if e != nil {
		return nil, e
	}
	socket, e := filepath.EvalSymlinks(c.Engine.SocketFile)
	if e != nil {
		return nil, failure("OFFLINE_COMPOSE_LOCAL_SOCKET_REQUIRED")
	}
	return &Client{settings: *c.Compose, mount: c.Verification.MountRoot, socket: socket, docker: docker, plugin: plugin}, nil
}

type limitedOutput struct {
	bytes.Buffer
	remaining int64
	exceeded  bool
	cancel    context.CancelFunc
}

func (b *limitedOutput) Write(raw []byte) (int, error) {
	if int64(len(raw)) > b.remaining {
		b.exceeded = true
		b.cancel()
		return 0, failure("OFFLINE_COMPOSE_OUTPUT_LIMIT")
	}
	b.remaining -= int64(len(raw))
	return b.Buffer.Write(raw)
}
func (c *Client) execute(ctx context.Context, args []string, input []byte) ([]byte, error) {
	raw, _, err := c.executeSubmission(ctx, args, input)
	return raw, err
}
func (c *Client) executeSubmission(ctx context.Context, args []string, input []byte) (raw []byte, submitted bool, result error) {
	if !mountpoint(c.mount) {
		return nil, submitted, failure("OFFLINE_COMPOSE_GENUINE_EXTERNAL_MOUNT_REQUIRED")
	}
	if e := c.docker.check(); e != nil {
		return nil, submitted, e
	}
	if e := c.plugin.check(); e != nil {
		return nil, submitted, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.settings.CommandSeconds)*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return nil, submitted, failure("OFFLINE_COMPOSE_OPERATION_INTERRUPTED")
	}
	directory, e := os.MkdirTemp(c.mount, ".projectone-compose-")
	if e != nil {
		return nil, submitted, failure("OFFLINE_COMPOSE_PRIVATE_CLIENT_CONFIG_UNAVAILABLE")
	}
	configPath := filepath.Join(directory, "config.json")
	config, _ := json.Marshal(map[string]any{"cliPluginsExtraDirs": []string{filepath.Dir(c.plugin.path)}})
	defer func() {
		// Never recursively delete unknown client/plugin content, even in our new directory.
		info, e := os.Lstat(directory)
		entries, re := os.ReadDir(directory)
		if e != nil || re != nil || info.Mode() != os.ModeDir|0700 || !owner(info) || len(entries) > 1 || len(entries) == 1 && entries[0].Name() != "config.json" {
			result = failure("OFFLINE_COMPOSE_TEMPORARY_STATE_PRESERVED")
			return
		}
		if len(entries) == 1 {
			i, e := os.Lstat(configPath)
			content, readError := os.ReadFile(configPath)
			if e != nil || readError != nil || i.Mode() != 0600 || !owner(i) || !bytes.Equal(content, config) || os.Remove(configPath) != nil {
				result = failure("OFFLINE_COMPOSE_TEMPORARY_STATE_PRESERVED")
				return
			}
		}
		if os.Remove(directory) != nil {
			result = failure("OFFLINE_COMPOSE_TEMPORARY_STATE_PRESERVED")
		}
	}()
	f, e := os.OpenFile(configPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, submitted, failure("OFFLINE_COMPOSE_PRIVATE_CLIENT_CONFIG_UNAVAILABLE")
	}
	_, we := f.Write(config)
	se := f.Sync()
	ce := f.Close()
	if we != nil || se != nil || ce != nil {
		return nil, submitted, failure("OFFLINE_COMPOSE_PRIVATE_CLIENT_CONFIG_UNAVAILABLE")
	}
	global := []string{"--config", directory, "--host", "unix://" + c.socket, "compose", "--ansi", "never", "--parallel", "1"}
	cmd := exec.Command(c.docker.path, append(global, args...)...)
	cmd.Dir = directory
	cmd.Env = []string{"PATH=" + filepath.Dir(c.docker.path) + ":/usr/bin:/bin:/usr/sbin:/sbin", "LANG=C", "LC_ALL=C", "DOCKER_API_VERSION=1.49", "DOCKER_CLI_HINTS=false", "TMPDIR=" + directory}
	cmd.Stdin = bytes.NewReader(input)
	stdout := &limitedOutput{remaining: c.settings.OutputBytes, cancel: cancel}
	stderr := &limitedOutput{remaining: c.settings.OutputBytes, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e = cmd.Start(); e != nil {
		return nil, submitted, failure("OFFLINE_COMPOSE_PROCESS_START_FAILED")
	}
	submitted = true
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case e = <-done:
		// Also terminate any exact inherited child group left behind by a failed tool.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	case <-ctx.Done():
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		if stdout.exceeded || stderr.exceeded {
			return nil, submitted, failure("OFFLINE_COMPOSE_OUTPUT_LIMIT")
		}
		return nil, submitted, failure("OFFLINE_COMPOSE_OPERATION_INTERRUPTED")
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, submitted, failure("OFFLINE_COMPOSE_OUTPUT_LIMIT")
	}
	if e != nil {
		return nil, submitted, failure("OFFLINE_COMPOSE_PROCESS_FAILED")
	}
	if e = c.docker.check(); e != nil {
		return nil, submitted, e
	}
	if e = c.plugin.check(); e != nil {
		return nil, submitted, e
	}
	return append([]byte(nil), stdout.Bytes()...), submitted, nil
}
func (c *Client) Version(ctx context.Context) (string, error) {
	raw, e := c.execute(ctx, []string{"version", "--format", "json"}, nil)
	if e != nil {
		return "", e
	}
	var reply struct {
		Version string `json:"version"`
	}
	if wire.UniqueJSON(raw, &reply) != nil {
		return "", failure("OFFLINE_COMPOSE_VERSION_RESPONSE_INVALID")
	}
	version := strings.TrimPrefix(reply.Version, "v")
	var policy wire.ComposePolicy
	if wire.StrictJSON(wire.ComposeProfile, &policy) != nil {
		return "", failure("OFFLINE_COMPOSE_POLICY_INVALID")
	}
	for _, allowed := range policy.Versions {
		if version == allowed {
			return version, nil
		}
	}
	return "", failure("OFFLINE_COMPOSE_VERSION_UNSUPPORTED")
}
func Preflight(ctx context.Context, c configuration.Config) (Observation, error) {
	client, e := New(c)
	if e != nil {
		return Observation{}, e
	}
	engineClient, e := engine.New(*c.Engine)
	if e != nil {
		return Observation{}, e
	}
	defer engineClient.Close()
	observed, e := engineClient.Preflight(ctx)
	if e != nil {
		return Observation{}, e
	}
	version, e := client.Version(ctx)
	if e != nil {
		return Observation{}, e
	}
	return Observation{Status: "passed_explicit_offline_Compose_interface_preflight_only", Version: version, DockerSHA256: client.docker.digest, PluginSHA256: client.plugin.digest, Engine: observed, Qualification: "CLI_and_engine_metadata_only_not_render_installation_or_target"}, nil
}
