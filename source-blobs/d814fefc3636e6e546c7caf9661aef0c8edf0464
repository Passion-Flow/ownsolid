//go:build repository_integration && task_events_integration && task_events_restart_integration

package taskdeliveries

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/taskbus"
	strict "github.com/Passion-Flow/ownsolid/api/datasets/v1"
)

type eventRestartDescriptor struct {
	DockerExecutable         string `json:"docker_executable"`
	DockerContext            string `json:"docker_context"`
	ContainerID              string `json:"container_id"`
	ImageID                  string `json:"image_id"`
	FixtureID                string `json:"fixture_id"`
	NetworkName              string `json:"network_name"`
	StoreSource              string `json:"store_source"`
	StoreDestination         string `json:"store_destination"`
	ConfigurationSource      string `json:"configuration_source"`
	ConfigurationDestination string `json:"configuration_destination"`
	ConfigurationSHA256      string `json:"configuration_sha256"`
	ServerPath               string `json:"server_path"`
	ClientContainerPort      int    `json:"client_container_port"`
	CommandSeconds           int    `json:"command_seconds"`
	StopSeconds              int    `json:"stop_seconds"`
}
type eventDockerInspection struct {
	ID, Image, Name, Path string
	Args                  []string
	State                 struct {
		Running, OOMKilled bool
		Pid                int
		StartedAt          string
	}
	Config     struct{ Labels map[string]string }
	HostConfig struct {
		Memory, NanoCpus, PidsLimit                 int64
		ReadonlyRootfs, Privileged, PublishAllPorts bool
		CapDrop, SecurityOpt                        []string
		NetworkMode                                 string
		RestartPolicy                               struct{ Name string }
	}
	Mounts []struct {
		Type, Source, Destination string
		RW                        bool
	}
	NetworkSettings struct {
		Networks map[string]json.RawMessage
		Ports    map[string][]struct{ HostIP, HostPort string }
	}
}
type eventBoundedOutput struct {
	buffer  bytes.Buffer
	maximum int
}

func (w *eventBoundedOutput) Write(b []byte) (int, error) {
	if len(b) > w.maximum-w.buffer.Len() {
		return 0, io.ErrShortBuffer
	}
	return w.buffer.Write(b)
}

func (d eventRestartDescriptor) command(ctx context.Context, args ...string) ([]byte, error) {
	bounded, cancel := context.WithTimeout(ctx, time.Duration(d.CommandSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, d.DockerExecutable, append([]string{"--context", d.DockerContext}, args...)...)
	out, stderr := &eventBoundedOutput{maximum: 1 << 20}, &eventBoundedOutput{maximum: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if cmd.Run() != nil {
		return nil, errors.New("ACTUAL_BROKER_FIXTURE_CONTROL_FAILED")
	}
	return out.buffer.Bytes(), nil
}
func (d eventRestartDescriptor) inspect(ctx context.Context, c taskbus.Configuration) (eventDockerInspection, error) {
	bad := errors.New("EXACT_PRIVATE_DISPOSABLE_BROKER_REQUIRED")
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(d.ContainerID) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(d.ImageID) || !taskbus.ValidID(d.FixtureID) || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).MatchString(d.DockerContext) || d.CommandSeconds < 1 || d.CommandSeconds > 60 || d.StopSeconds < 1 || d.StopSeconds >= d.CommandSeconds || d.ClientContainerPort < 1 || d.ClientContainerPort > 65535 {
		return eventDockerInspection{}, bad
	}
	canonical, e := filepath.EvalSymlinks(d.DockerExecutable)
	if e != nil || canonical != d.DockerExecutable || !filepath.IsAbs(canonical) {
		return eventDockerInspection{}, bad
	}
	executable, e := os.Stat(canonical)
	if e != nil || !executable.Mode().IsRegular() || executable.Mode().Perm()&0022 != 0 || executable.Mode().Perm()&0111 == 0 {
		return eventDockerInspection{}, bad
	}
	state, e := filepath.EvalSymlinks(os.Getenv("PROJECT_ONE_STATE_ROOT"))
	if e != nil {
		return eventDockerInspection{}, bad
	}
	for _, path := range []string{d.StoreSource, d.ConfigurationSource} {
		actual, e := filepath.EvalSymlinks(path)
		if e != nil || actual != path || !strings.HasPrefix(path, state+string(os.PathSeparator)) {
			return eventDockerInspection{}, bad
		}
	}
	store, e := os.Stat(d.StoreSource)
	if e != nil || !store.IsDir() {
		return eventDockerInspection{}, bad
	}
	stateInfo, e := os.Stat(state)
	if e != nil {
		return eventDockerInspection{}, bad
	}
	stateDevice, ok := stateInfo.Sys().(*syscall.Stat_t)
	if !ok {
		return eventDockerInspection{}, bad
	}
	storeDevice, ok := store.Sys().(*syscall.Stat_t)
	if !ok || storeDevice.Dev != stateDevice.Dev {
		return eventDockerInspection{}, bad
	}

	configInfo, e := os.Lstat(d.ConfigurationSource)
	if e != nil || !configInfo.Mode().IsRegular() || configInfo.Mode().Perm() != 0600 {
		return eventDockerInspection{}, bad
	}
	configurationDevice, ok := configInfo.Sys().(*syscall.Stat_t)
	if !ok || configurationDevice.Dev != stateDevice.Dev || configurationDevice.Uid != uint32(os.Geteuid()) || configurationDevice.Nlink != 1 {
		return eventDockerInspection{}, bad
	}

	if len(d.ConfigurationSHA256) != 64 || !digestHex(d.ConfigurationSHA256) {
		return eventDockerInspection{}, bad
	}
	file, e := os.Open(d.ConfigurationSource)
	if e != nil {
		return eventDockerInspection{}, bad
	}
	raw, e := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	after, se := file.Stat()
	ce := file.Close()
	sum := sha256.Sum256(raw)
	if e != nil || se != nil || ce != nil || len(raw) > 1<<20 || !os.SameFile(configInfo, after) || configInfo.Size() != after.Size() || !configInfo.ModTime().Equal(after.ModTime()) || hex.EncodeToString(sum[:]) != d.ConfigurationSHA256 {
		return eventDockerInspection{}, bad
	}
	for _, path := range []string{d.StoreDestination, d.ConfigurationDestination, d.ServerPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
			return eventDockerInspection{}, bad
		}
	}
	contextRaw, e := d.command(ctx, "context", "inspect", d.DockerContext)
	if e != nil {
		return eventDockerInspection{}, e
	}
	var contextInfo []struct {
		Endpoints map[string]struct{ Host string }
	}
	if json.Unmarshal(contextRaw, &contextInfo) != nil || len(contextInfo) != 1 {
		return eventDockerInspection{}, bad
	}
	endpoint, e := url.Parse(contextInfo[0].Endpoints["docker"].Host)
	if e != nil || endpoint.Scheme != "unix" || endpoint.Host != "" || endpoint.Path == "" || endpoint.User != nil || endpoint.RawQuery != "" {
		return eventDockerInspection{}, bad
	}
	raw, e = d.command(ctx, "container", "inspect", d.ContainerID)
	if e != nil {
		return eventDockerInspection{}, e
	}
	var values []eventDockerInspection
	if json.Unmarshal(raw, &values) != nil || len(values) != 1 {
		return eventDockerInspection{}, bad
	}
	v := values[0]
	if v.ID != d.ContainerID || v.Image != d.ImageID || v.Name != "/m25-nats-fixture-"+strings.ReplaceAll(d.FixtureID, "-", "") || v.Path != d.ServerPath || len(v.Args) != 2 || v.Args[0] != "-c" || v.Args[1] != d.ConfigurationDestination || !v.State.Running || v.State.OOMKilled || v.State.Pid < 1 || v.State.StartedAt == "" || v.Config.Labels["com.projectone.fixture.kind"] != "m25-task-events" || v.Config.Labels["com.projectone.fixture.id"] != d.FixtureID || v.Config.Labels["com.projectone.fixture.config-sha256"] != d.ConfigurationSHA256 {
		return eventDockerInspection{}, bad
	}
	h := v.HostConfig
	if h.Memory < 1 || h.Memory > 256<<20 || h.NanoCpus < 1 || h.NanoCpus > 1e9 || h.PidsLimit < 1 || h.PidsLimit > 128 || !h.ReadonlyRootfs || h.Privileged || h.PublishAllPorts || len(h.CapDrop) != 1 || h.CapDrop[0] != "ALL" || len(h.SecurityOpt) != 1 || h.SecurityOpt[0] != "no-new-privileges:true" || h.RestartPolicy.Name != "no" || h.NetworkMode != d.NetworkName || d.NetworkName != "m25-task-events-"+strings.ReplaceAll(d.FixtureID, "-", "") || len(v.NetworkSettings.Networks) != 1 || v.NetworkSettings.Networks[d.NetworkName] == nil {
		return eventDockerInspection{}, bad
	}
	u, e := url.Parse(c.Endpoint)
	if e != nil || len(c.DialAddresses) != 1 || c.DialAddresses[0] != "127.0.0.1" {
		return eventDockerInspection{}, bad
	}
	ports := 0
	for port, bindings := range v.NetworkSettings.Ports {
		if len(bindings) == 0 {
			continue
		}
		if port != strconv.Itoa(d.ClientContainerPort)+"/tcp" || len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" || bindings[0].HostPort != u.Port() {
			return eventDockerInspection{}, bad
		}
		ports++
	}
	if ports != 1 {
		return eventDockerInspection{}, bad
	}
	storeMount, configurationMount := false, false
	for _, m := range v.Mounts {
		if m.Type != "bind" || !strings.HasPrefix(m.Source, state+string(os.PathSeparator)) {
			return eventDockerInspection{}, bad
		}
		if m.Destination == d.StoreDestination {
			if m.Source != d.StoreSource || !m.RW || storeMount {
				return eventDockerInspection{}, bad
			}
			storeMount = true
		} else if m.Destination == d.ConfigurationDestination {
			if m.Source != d.ConfigurationSource || m.RW || configurationMount {
				return eventDockerInspection{}, bad
			}
			configurationMount = true
		} else if m.RW {
			return eventDockerInspection{}, bad
		}
	}
	if !storeMount || !configurationMount {
		return eventDockerInspection{}, bad
	}
	return v, nil
}
func (d eventRestartDescriptor) restart(t *testing.T, g *eventStory) {
	t.Helper()
	before, e := d.inspect(g.ctx, g.s.Configuration)
	if e != nil {
		t.Fatal(e)
	}
	store, e := os.Stat(d.StoreSource)
	if e != nil {
		t.Fatal("actual broker store binding")
	}
	if _, e = d.command(g.ctx, "container", "restart", "--timeout", strconv.Itoa(d.StopSeconds), d.ContainerID); e != nil {
		t.Fatal(e)
	}
	after, e := d.inspect(g.ctx, g.s.Configuration)
	if e != nil || after.State.StartedAt == before.State.StartedAt {
		t.Fatal("actual broker process restart unconfirmed", e)
	}
	current, e := os.Stat(d.StoreSource)
	if e != nil || !os.SameFile(store, current) {
		t.Fatal("broker persistence source replaced")
	}
	t.Logf("TASK_EVENT_ACTUAL_BROKER_RESTART fixture=%s container=%s image=%s started_before=%s started_after=%s", d.FixtureID, d.ContainerID, d.ImageID, before.State.StartedAt, after.State.StartedAt)
}

func TestActualTaskEventsRestart(t *testing.T) {
	for _, leaf := range []string{"broker_restart_prepared", "broker_restart_receipt_before_ack", "broker_restart_acknowledged"} {
		t.Run(leaf, func(t *testing.T) {
			g := eventSetup(t)
			var control eventRestartDescriptor
			var restart func()
			if os.Getenv("PROJECT_ONE_ACTUAL_TASK_EVENTS_PROFILE_MODE") == "native_owned" {
				if g.nativeRestart == nil || len(g.restart) != 0 {
					t.Fatal("EXACT_OWNED_NATIVE_BROKER_RESTART_REQUIRED")
				}
				restart = g.nativeRestart
			} else {
				if g.nativeRestart != nil || strict.DecodeStrict(g.restart, &control) != nil {
					t.Fatal("EXACT_OWNED_BROKER_RESTART_DESCRIPTOR_REQUIRED")
				}
				restart = func() { control.restart(t, g) }
			}
			_, value := g.task(t)
			if leaf != "broker_restart_prepared" {
				g.publish(t)
			}
			if leaf == "broker_restart_receipt_before_ack" {
				message, e := g.s.Transport.Next(g.ctx)
				if e != nil || message == nil {
					t.Fatal(e)
				}
				if e = g.s.consumeEnvelope(g.ctx, g.f.Store, message, envelope(value)); e != nil {
					t.Fatal(e)
				}
				// The real receipt/audit has committed; deliberately close the original
				// consumer owner without sending its positive ACK to the broker.
				if e = g.s.Close(g.ctx); e != nil {
					t.Fatal(e)
				}
			} else if leaf == "broker_restart_acknowledged" {
				g.consume(t)
			}
			before := g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events")
			restart()
			if leaf == "broker_restart_receipt_before_ack" {
				c := g.s.Configuration
				var transport *taskbus.JetStream
				var e error
				for attempt := 0; attempt < c.MaximumReconnectAttempts; attempt++ {
					transport, e = taskbus.NewJetStream(g.ctx, c, g.f.Tenant)
					if e == nil {
						break
					}
					if attempt+1 < c.MaximumReconnectAttempts {
						waitEventTime(t, g.ctx, time.Now().Add(time.Duration(c.ReconnectSeconds)*time.Second))
					}
				}
				if e != nil {
					t.Fatal("bounded actual consumer owner reopen failed", e)
				}
				reopened, e := NewService(g.ctx, g.owner, g.n.Workspaces, g.n, c, transport)
				if e != nil {
					transport.Close()
					t.Fatal(e)
				}
				g.s = reopened
				g.n.TaskEvents = reopened
				waitEventTime(t, g.ctx, time.Now().Add(time.Duration(c.AckWaitSeconds)*time.Second+time.Millisecond))
				g.consume(t)
			} else {
				if e := g.s.recoverTransport(g.ctx, fail("TASK_BUS_CLOSED")); e != nil {
					t.Fatal("actual bounded broker reconnection", e)
				}
				if leaf == "broker_restart_prepared" {
					g.publish(t)
					g.consume(t)
				} else if worked, e := g.s.ConsumeNext(g.ctx); e != nil || worked {
					t.Fatal("acknowledged event repeated after broker restart", worked, e)
				}
			}
			if e := g.s.VerifyStored(g.ctx); e != nil {
				t.Fatal(e)
			}
			if g.count(t, "SELECT count(*) FROM task_event_receipts", "SELECT count(*) FROM task_event_receipts") != 1 {
				t.Fatal("broker restart lost/duplicated receipt")
			}
			if leaf != "broker_restart_prepared" && g.count(t, "SELECT count(*) FROM audit_events", "SELECT count(*) FROM audit_events") != before {
				t.Fatal("broker/consumer reopen repeated receipt effect")
			}
		})
	}
}
