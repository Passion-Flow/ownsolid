package e2e

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

//go:embed testdata/gateway-sockets.py
var gatewaySocketScript string

const gatewayObservationEvery = 5 * time.Second

type gatewayObservation struct {
	At      time.Time       `json:"at"`
	Phase   string          `json:"phase"`
	Gateway string          `json:"gateway"`
	Stopped bool            `json:"stopped"`
	Sockets map[string]int  `json:"sockets,omitempty"`
	Stats   json.RawMessage `json:"docker_cli_stats,omitempty"`
}

type gatewayObservationRunner func(context.Context, ...string) ([]byte, error)

// observeGateway reads physical sockets, including probes and draining peers.
// A container that deliberately stopped has no resource measurement.
func observeGateway(ctx context.Context, run gatewayObservationRunner, gateway, phase string, mayStop bool) (gatewayObservation, error) {
	row := gatewayObservation{At: time.Now().UTC(), Phase: phase, Gateway: gateway}
	running := func() (bool, error) {
		raw, err := run(ctx, "inspect", "--format", "{{.State.Running}}", gateway)
		if err != nil {
			return false, fmt.Errorf("inspect gateway %s: %w", gateway, err)
		}
		return strconv.ParseBool(strings.TrimSpace(string(raw)))
	}
	live, err := running()
	if err != nil {
		return row, err
	}
	if !live {
		if !mayStop {
			return row, fmt.Errorf("gateway %s unexpectedly stopped in %s", gateway, phase)
		}
		row.Stopped = true
		return row, nil
	}
	stats, err := run(ctx, "stats", "--no-stream", "--format", "{{json .}}", gateway)
	if err == nil {
		var fields map[string]string
		if json.Unmarshal(stats, &fields) != nil || fields["Name"] != gateway ||
			fields["CPUPerc"] == "" || fields["MemUsage"] == "" || fields["PIDs"] == "" || fields["NetIO"] == "" {
			err = fmt.Errorf("gateway %s returned incomplete Docker stats", gateway)
		}
	}
	if err == nil {
		var raw []byte
		raw, err = run(ctx, "exec", gateway, "python3", "-c", gatewaySocketScript, "--port", strconv.Itoa(gatewayPort))
		if err == nil {
			if json.Unmarshal(raw, &row.Sockets) != nil || row.Sockets["listen"] < 1 {
				err = fmt.Errorf("gateway %s returned incomplete serving-port observation", gateway)
			}
			for _, count := range row.Sockets {
				if count < 0 {
					err = fmt.Errorf("gateway %s returned a negative socket count", gateway)
				}
			}
		}
	}
	if err != nil {
		// A kill can race either command. Only a confirmed, permitted stop is
		// expected; a missing container or a broken running observer still fails.
		if mayStop {
			if live, inspectErr := running(); inspectErr == nil && !live {
				row.Stopped, row.Sockets = true, nil
				return row, nil
			}
		}
		return row, fmt.Errorf("observe gateway %s: %w", gateway, err)
	}
	row.Stats = append(json.RawMessage(nil), stats...)
	return row, nil
}

// gatewayObserver owns only fixture observations and joins before container cleanup.
type gatewayObserver struct {
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	gateways []agentGateway
	sampleMu sync.Mutex
	mu       sync.Mutex
	phase    string
	rows     []gatewayObservation
	err      error
}

func startGatewayObserver(t *testing.T, gateways ...agentGateway) *gatewayObserver {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	o := &gatewayObserver{ctx: ctx, cancel: cancel, done: make(chan struct{}), gateways: gateways, phase: "enrollment"}
	go func() {
		defer close(o.done)
		ticker := time.NewTicker(gatewayObservationEvery)
		defer ticker.Stop()
		for {
			o.sample()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-o.done
		o.mu.Lock()
		defer o.mu.Unlock()
		maxima := map[string]int{}
		for _, row := range o.rows {
			raw, err := json.Marshal(row)
			if err != nil {
				t.Error(err)
				continue
			}
			t.Logf("gateway_observation %s", raw)
			for state, count := range row.Sockets {
				key := row.Gateway + "/" + state
				if count > maxima[key] {
					maxima[key] = count
				}
			}
		}
		t.Logf("gateway_sample_interval=%s sampled_socket_maxima=%v memory_representation=docker_cli_cache_adjusted", gatewayObservationEvery, maxima)
		if o.err != nil {
			t.Error(o.err)
		}
	})
	return o
}

func (o *gatewayObserver) checkpoint(t *testing.T, phase string) {
	t.Helper()
	o.mu.Lock()
	o.phase = phase
	o.mu.Unlock()
	o.sample()
	o.mu.Lock()
	err := o.err
	o.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
}

func (o *gatewayObserver) sample() {
	o.sampleMu.Lock()
	defer o.sampleMu.Unlock()
	o.mu.Lock()
	phase, prior := o.phase, o.err
	o.mu.Unlock()
	if prior != nil || o.ctx.Err() != nil {
		return
	}
	run := func(parent context.Context, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.WaitDelay = time.Second
		return cmd.CombinedOutput()
	}
	for index, gateway := range o.gateways {
		row, err := observeGateway(o.ctx, run, gateway.name, phase, phase == "gateway_a_stopped" && index == 0)
		o.mu.Lock()
		if err != nil {
			if o.ctx.Err() == nil {
				o.err = err
			}
			o.mu.Unlock()
			return
		}
		o.rows = append(o.rows, row)
		o.mu.Unlock()
	}
}

func TestGatewayObservationAdmission(t *testing.T) {
	const stats = `{"Name":"fixture-gateway","CPUPerc":"1.2%","MemUsage":"8MiB / 1GiB","PIDs":"4","NetIO":"1KiB / 2KiB"}`
	for _, tc := range []struct {
		name, stats, sockets string
		running              []string
		mayStop, execFailure bool
		wantError, stopped   bool
	}{
		{name: "live", stats: stats, sockets: `{"listen":1,"established":3,"time_wait":2,"unknown_FF":1}`},
		{name: "intentional_stop", running: []string{"false"}, mayStop: true, stopped: true},
		{name: "unexpected_stop", running: []string{"false"}, wantError: true},
		{name: "missing_stats", sockets: `{"listen":1}`, wantError: true},
		{name: "wrong_container", stats: strings.Replace(stats, "fixture-gateway", "another-gateway", 1), wantError: true},
		{name: "invalid_sockets", stats: stats, sockets: "{", wantError: true},
		{name: "negative_sockets", stats: stats, sockets: `{"listen":1,"established":-1}`, wantError: true},
		{name: "missing_listener", stats: stats, sockets: `{"established":3}`, wantError: true},
		{name: "kill_during_exec", stats: stats, running: []string{"true", "false"}, mayStop: true, execFailure: true, stopped: true},
		{name: "broken_live_observer", stats: stats, mayStop: true, execFailure: true, wantError: true},
		{name: "lost_container_after_exec", stats: stats, running: []string{"true", "missing"}, mayStop: true, execFailure: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inspections, commands := 0, 0
			run := func(ctx context.Context, args ...string) ([]byte, error) {
				if ctx != t.Context() || args[len(args)-1] == "" {
					t.Fatal("observation lost its context or named scope")
				}
				commands++
				switch args[0] {
				case "inspect":
					state := "true"
					if inspections < len(tc.running) {
						state = tc.running[inspections]
					}
					inspections++
					if state == "missing" {
						return nil, errors.New("fixture container missing")
					}
					return []byte(state), nil
				case "stats":
					return []byte(tc.stats), nil
				case "exec":
					if tc.execFailure {
						return nil, errors.New("fixture observation failed")
					}
					return []byte(tc.sockets), nil
				default:
					t.Fatal("unexpected observation command", args[0])
					return nil, nil
				}
			}
			row, err := observeGateway(t.Context(), run, "fixture-gateway", "fixture_phase", tc.mayStop)
			if (err != nil) != tc.wantError || row.Stopped != tc.stopped {
				t.Fatalf("unexpected observation: stopped=%t error=%v", row.Stopped, err)
			}
			if row.Stopped && (len(row.Stats) != 0 || len(row.Sockets) != 0) {
				t.Fatal("stopped gateway was reported as a resource measurement")
			}
			if tc.name == "intentional_stop" && commands != 1 {
				t.Fatal("observation executed in a stopped container")
			}
			if tc.name == "live" && (row.Sockets["established"] != 3 || row.Sockets["time_wait"] != 2 || row.Sockets["unknown_FF"] != 1 || len(row.Stats) == 0) {
				t.Fatal("observation discarded physical socket states or raw stats")
			}
		})
	}
}
