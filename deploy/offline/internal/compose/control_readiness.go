package compose

import (
	"context"
	"encoding/json"
	"net/netip"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type ControlReadinessInput struct {
	Configuration  string `json:"configuration"`
	DialAddress    string `json:"dial_address"`
	CAFile         string `json:"ca_file"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}
type ControlReadiness struct {
	ControlID   string   `json:"control_id"`
	PID         int      `json:"pid"`
	StartedAt   string   `json:"started_at"`
	ObservedAt  string   `json:"observed_at"`
	Audiences   []string `json:"audiences"`
	InputSHA256 string   `json:"input_sha256"`
}

// This is a fixed read-only native probe, not a shell/exec interface. The exact
// observed Control identity and model must survive both entrance checks.
func (c *Client) ProbeControlReadiness(ctx context.Context, r RuntimeReader, scope RenderContext, model Model, recorded RuntimeStatus, input ControlReadinessInput) (ControlReadiness, error) {
	bad := func() (ControlReadiness, error) {
		return ControlReadiness{}, failure("OFFLINE_COMPOSE_CONTROL_READINESS_UNAVAILABLE")
	}
	address, e := netip.ParseAddrPort(input.DialAddress)
	if e != nil || !address.Addr().IsLoopback() || address.Addr().Zone() != "" || address.Port() == 0 || address.String() != input.DialAddress || !containerTarget(input.Configuration) || input.TimeoutSeconds < 1 || input.TimeoutSeconds > 60 || input.TimeoutSeconds >= c.settings.CommandSeconds || input.CAFile != "" && !containerTarget(input.CAFile) {
		return bad()
	}
	if _, e := c.Version(ctx); e != nil {
		return bad()
	}
	service, ok := model.Services["control"]
	if !ok || len(service.Command) != 3 || service.Command[0] != "serve" || service.Command[1] != "--config" || service.Command[2] != input.Configuration {
		return bad()
	}
	mounted := false
	for _, m := range service.Mounts {
		if m.Type == "volume" && m.ReadOnly && input.Configuration == path.Join(m.Target, "private", "runtime.yaml") && (input.CAFile == "" || path.Dir(input.CAFile) == path.Join(m.Target, "private")) {
			mounted = true
		}
	}
	if !mounted {
		return bad()
	}
	observed, e := c.ObserveRuntime(ctx, r, scope, model)
	if e != nil || !processIdentity(recorded, observed) || observed.Status != "observed_complete_compose_resources" || observed.Services["control"].State != "running" {
		return bad()
	}
	before, found, e := r.InspectRuntime(ctx, scope.ScopeID, "control")
	if e != nil || !found || before.ID != recorded.Services["control"].ID || before.State.PID < 1 || !before.State.Running || before.State.Status != "running" || before.State.OOMKilled || before.State.Restarting || before.State.Dead || before.State.Paused || before.State.ExitCode != 0 {
		return bad()
	}
	document, e := c.document(scope, model)
	if e != nil {
		return bad()
	}
	var literal any
	if json.Unmarshal(document, &literal) != nil {
		return bad()
	}
	document, _ = json.Marshal(escapeInterpolation(literal))
	audiences := []string{"portal", "admin"}
	for _, audience := range audiences {
		args := []string{"--project-name", engine.RuntimeProject(scope.ScopeID), "--project-directory", scope.ScopeRoot, "-f", "-", "exec", "--no-tty", "--interactive=false", "control", "/usr/local/bin/control", "health-probe", "--config", input.Configuration, "--audience", audience, "--kind", "ready", "--dial-address", input.DialAddress, "--timeout-seconds", strconv.Itoa(input.TimeoutSeconds)}
		if input.CAFile != "" {
			args = append(args, "--ca-file", input.CAFile)
		}
		output, e := c.execute(ctx, args, document)
		if e != nil || strings.TrimSpace(string(output)) != "CONTROL_HEALTH_PROBE_OK" {
			return bad()
		}
	}
	after, found, e := r.InspectRuntime(ctx, scope.ScopeID, "control")
	if e != nil || !found || before.ID != after.ID || before.State != after.State {
		return bad()
	}
	observed, e = c.ObserveRuntime(ctx, r, scope, model)
	if e != nil || !processIdentity(recorded, observed) || observed.Services["control"].State != "running" {
		return bad()
	}
	return ControlReadiness{ControlID: before.ID, PID: before.State.PID, StartedAt: before.State.StartedAt, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), Audiences: audiences, InputSHA256: wire.Digest(encodeReadiness(input))}, nil
}
func encodeReadiness(v any) []byte { b, _ := json.Marshal(v); return append(b, '\n') }
