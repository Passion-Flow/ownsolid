package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/agentgateway"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// An agent gateway (ARC-102, ADR 0048) is a process that serves the agent session stream
// beside control, so that the connections, TLS and exchanges of a large fleet are spread over
// several hosts. It runs the same node service as control against the same database, but it
// takes no installation lock, runs none of control's workers and serves nothing else: results,
// renewals, transfers and enrollment stay on control's agent entrance.
const agentGatewayCommand = "serve-agent-gateway"

// installationHolderSQL finds the backend holding the installation lock, which is the control
// process serving this installation or an operator command that owns it for a while.
const installationHolderSQL = `SELECT pid FROM pg_catalog.pg_locks WHERE locktype='advisory' AND classid=0::oid AND objid=$1::bigint::oid AND objsubid=1 AND mode='ExclusiveLock' AND granted AND database=(SELECT oid FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database())`

// installationCheckInterval is how often a gateway looks for the lock holder. It bounds how
// long a gateway goes on serving after control stopped, and how soon it serves once control
// is back. One indexed read of pg_locks per gateway per interval costs nothing measurable.
const installationCheckInterval = 5 * time.Second

// errInstallationNotServing fences a gateway while no control holds the installation.
var errInstallationNotServing = errors.New("SERVICE_NOT_READY")

// installationFence lets a gateway serve only while a control process holds the installation
// and the schema is the one this binary was built for. A new holder means control restarted,
// possibly after an upgrade, so the schema is checked again before the gateway serves.
type installationFence struct {
	pool   *pgxpool.Pool
	store  transaction.Store
	mu     sync.Mutex
	holder int32
	ready  error
}

// Ready reports why the gateway must not serve, or nil.
func (f *installationFence) Ready() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ready
}

func (f *installationFence) set(holder int32, ready error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if (ready == nil) != (f.ready == nil) {
		if ready == nil {
			slog.Info("agent_gateway_serving", "control_backend", holder)
		} else {
			slog.Warn("agent_gateway_fenced", "code", ready.Error())
		}
	}
	f.holder, f.ready = holder, ready
}

// check looks for the lock holder once. It returns an error only when the schema differs,
// which stops the gateway as it stops control.
func (f *installationFence) check(ctx context.Context) error {
	var holder int32
	e := f.pool.QueryRow(ctx, installationHolderSQL, database.InstallationLockID).Scan(&holder)
	if errors.Is(e, pgx.ErrNoRows) {
		f.set(0, errInstallationNotServing)
		return nil
	}
	if e != nil {
		f.set(0, errors.New("STORAGE_UNAVAILABLE"))
		return nil
	}
	f.mu.Lock()
	known := f.holder == holder && f.ready == nil
	f.mu.Unlock()
	if known {
		return nil
	}
	if e = database.CheckRuntimeReady(ctx, f.store); e != nil {
		if database.Unanswered(e) || ctx.Err() != nil {
			f.set(0, errors.New("STORAGE_UNAVAILABLE"))
			return nil
		}
		f.set(0, errInstallationNotServing)
		return errors.New("CONTROL_SCHEMA_VERIFICATION_FAILED")
	}
	f.set(holder, nil)
	return nil
}

func (f *installationFence) run(ctx context.Context, timeout time.Duration) error {
	ticker := time.NewTicker(installationCheckInterval)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, timeout)
		e := f.check(bounded)
		cancel()
		if e != nil {
			return e
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// agentGatewayHandler serves the session stream for the agent origin and the two health
// probes; every other request is not found here.
type agentGatewayHandler struct {
	machine  config.MachineServerConfiguration
	sessions *agentgateway.Gateway
	ready    func() error
	requests agentGatewayRequests
}

func (h *agentGatewayHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestID := uuid.NewString()
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !h.requests.begin() {
		httpserver.Problem(w, requestID, errInstallationNotServing)
		return
	}
	defer h.requests.end()
	connection, e := httpserver.ResolveMachineRequest(r, h.machine)
	if e != nil {
		httpserver.Problem(w, requestID, e)
		return
	}
	if agentgateway.Owns(r) {
		h.sessions.ServeHTTP(w, r, connection.ClientIP)
		return
	}
	if r.Method == http.MethodGet && r.URL.RawQuery == "" && (r.URL.Path == "/health/live" || r.URL.Path == "/health/ready") {
		status := "live"
		if r.URL.Path == "/health/ready" {
			if e = h.ready(); e != nil {
				httpserver.Problem(w, requestID, e)
				return
			}
			status = "ready"
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":%q}`+"\n", status)
		return
	}
	httpserver.Problem(w, requestID, errors.New("RESOURCE_NOT_FOUND"))
}

// runAgentGateway serves `serve-agent-gateway --config <file> [--env-file <file>]`.
func runAgentGateway(args []string) (result error) {
	flags := flag.NewFlagSet(agentGatewayCommand, flag.ContinueOnError)
	path := flags.String("config", "", "Configuration file")
	policyEnvironment := flags.String("env-file", "", "Optional protected serving resource-policy environment file")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if flags.NArg() != 0 {
		return errors.New("OPERATOR_ARGUMENT_INVALID")
	}
	if *path == "" {
		return errors.New("CONFIG_REQUIRED")
	}
	c, e := config.LoadWithEnvironment(*path, *policyEnvironment)
	if e != nil {
		return e
	}
	origin, tlsConfig, e := c.AgentGatewayServing()
	if e != nil {
		return e
	}
	policy := c.Policy()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	startup, stop := context.WithTimeout(ctx, config.Seconds(policy.HTTP.StartupSeconds))
	defer stop()
	pool, e := database.Open(startup, c)
	if e != nil {
		return e
	}
	g := newRuntimeLifecycle(ctx)
	g.closers = append(g.closers, func() error { pool.Close(); return nil })
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(policy.HTTP.ShutdownSeconds))
		defer cancel()
		result = errors.Join(result, g.Close(cleanup))
	}()
	store, e := transaction.FromPostgreSQL(pool)
	if e != nil {
		return e
	}
	if e = database.CheckRuntimeWithin(startup, store); e != nil {
		return e
	}
	bootstrapped, e := installationBootstrapped(startup, store)
	if e != nil {
		return e
	}
	if !bootstrapped {
		return errors.New("BOOTSTRAP_REQUIRED")
	}
	fence := &installationFence{pool: pool, store: store, ready: errInstallationNotServing}
	ready := func() error {
		if e := g.Err(); e != nil {
			return errors.New("SERVICE_NOT_READY")
		}
		return fence.Ready()
	}
	gateway, e := identityhttp.NewAgentGateway(&identity.Service{DB: pool, Config: c}, ready)
	if e != nil {
		return e
	}
	if e = fence.check(startup); e != nil {
		return e
	}
	nodes := gateway.Nodes
	if gateway.Logs != nil {
		g.Go("TELEMETRY_WRITER", gateway.Logs.Run)
	}
	if gateway.Metrics != nil {
		gateway.Metrics.Add(runtimeSamples)
		gateway.Metrics.Add(databasePoolSamples(pool))
		gateway.Metrics.Add(nodes.LivenessSamples)
		gateway.Metrics.Add(nodes.WakeSamples)
		gateway.Metrics.Add(gateway.Sessions.Samples)
		g.Go("TELEMETRY_METRICS", gateway.Metrics.Run)
	}
	g.Go("AGENT_GATEWAY_FENCE", func(ctx context.Context) error {
		return fence.run(ctx, config.Seconds(policy.HTTP.RequestSeconds))
	})
	if c.LivenessReplica != nil {
		replica, e := openLivenessReplica(*c.LivenessReplica, policy.Nodes)
		if e != nil {
			return e
		}
		nodes.LivenessReplica = replica
		g.Loop("NODE_LIVENESS_REPLICA", func(ctx context.Context) {
			defer replica.Close()
			nodes.RunLivenessReplica(ctx)
		})
	}
	g.Loop("NODE_LIVENESS", nodes.RunLiveness)
	nodes.RestoreShed(startup)
	g.Loop("NODE_AGENT_SHED", nodes.RunAgentShed)
	g.Loop("NODE_TASK_WAKES", nodes.RunWakes)
	machine := config.MachineServerConfiguration{ListenAddress: c.AgentGateway.ListenAddress, Origin: origin, TLSCertificateFile: c.AgentGateway.TLSCertificateFile, TLSPrivateKeyFile: c.AgentGateway.TLSPrivateKeyFile}
	handler := &agentGatewayHandler{machine: machine, sessions: gateway.Sessions, ready: ready}
	g.stops = append(g.stops, func(ctx context.Context) error {
		handler.requests.stop()
		gateway.Sessions.Drain()
		return errors.Join(handler.requests.wait(ctx), gateway.Sessions.DrainAndWait(ctx))
	})
	listener, e := net.Listen("tcp", c.AgentGateway.ListenAddress)
	if e != nil {
		return errors.New("SERVER_LISTEN_FAILED")
	}
	// Agents leave a stopping gateway for the next one in their order instead of waiting
	// for the shutdown to cut their streams. Join the drain before store closers.
	g.Loop("AGENT_GATEWAY_DRAIN", func(ctx context.Context) {
		<-ctx.Done()
		handler.requests.stop()
		gateway.Sessions.Drain()
	})
	fmt.Println("AGENT_GATEWAY_STARTED")
	slog.Info("agent_gateway_started", "listen_address", c.AgentGateway.ListenAddress, "origin", origin)
	age := httpserver.ConnectionAge{
		Maximum: c.AgentGateway.MaxConnectionAge(),
		// Allow a peer check, one admitted exchange and a bounded reply.
		Grace: config.Seconds(3*policy.HTTP.RequestSeconds + wire.MaxClaimWaitSeconds),
	}
	e = httpserver.ServeListener(g.ctx, listener, tlsConfig, handler, policy.HTTP, age)
	if e != nil {
		result = errors.New("SERVER_FAILED")
	}
	return errors.Join(result, g.Failure())
}
