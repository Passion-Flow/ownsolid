package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/database/transaction"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/liveness"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/retention"
)

// runtimeLifecycle owns every composition goroutine and its shutdown result.
// joined closes only after confirmed Stop calls, all loops and file closers.
type runtimeLifecycle struct {
	ctx        context.Context
	cancel     context.CancelCauseFunc
	mu         sync.Mutex
	failure    error
	closing    bool
	workers    sync.WaitGroup
	stops      []func(context.Context) error
	interrupts []func()
	closers    []func() error
	once       sync.Once
	finished   chan struct{}
	joined     chan struct{}
}

func newRuntimeLifecycle(ctx context.Context) *runtimeLifecycle {
	worker, cancel := context.WithCancelCause(ctx)
	return &runtimeLifecycle{ctx: worker, cancel: cancel, finished: make(chan struct{}), joined: make(chan struct{})}
}
func (g *runtimeLifecycle) Fail(e error) {
	if e == nil {
		return
	}
	g.mu.Lock()
	if g.failure == nil {
		g.failure = e
	} else if !errors.Is(g.failure, e) && g.failure.Error() != e.Error() {
		g.failure = errors.Join(g.failure, e)
	}
	g.mu.Unlock()
	g.cancel(e)
}
func (g *runtimeLifecycle) Failure() error { g.mu.Lock(); defer g.mu.Unlock(); return g.failure }
func (g *runtimeLifecycle) Err() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failure != nil {
		return g.failure
	}
	if g.closing {
		return errors.New("CONTROL_STOPPING")
	}
	return g.ctx.Err()
}
func (g *runtimeLifecycle) Go(name string, fn func(context.Context) error) {
	g.workers.Add(1)
	go func() {
		defer g.workers.Done()
		e := fn(g.ctx)
		if g.ctx.Err() != nil && (e == nil || transaction.IsCancellationOnly(e)) {
			return
		}
		if e != nil {
			g.Fail(runtimeWorkerFailure(name, e))
		} else {
			g.Fail(errors.New(name + "_STOPPED"))
		}
	}()
}

func runtimeWorkerFailure(name string, e error) error {
	for _, kind := range []transaction.ErrorKind{transaction.CommitUnknown, transaction.CommitRolledBack} {
		if runtimeContainsOutcome(e, kind) {
			return errors.New(string(kind))
		}
	}
	return errors.New(name + "_FAILED")
}

func runtimeContainsOutcome(e error, kind transaction.ErrorKind) bool {
	if e == nil {
		return false
	}
	if e.Error() == string(kind) {
		return true
	}
	if outcome, ok := e.(*transaction.Error); ok && outcome.Kind == kind {
		return true
	}
	if joined, ok := e.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if runtimeContainsOutcome(child, kind) {
				return true
			}
		}
	}
	if wrapped, ok := e.(interface{ Unwrap() error }); ok {
		return runtimeContainsOutcome(wrapped.Unwrap(), kind)
	}
	return false
}
func (g *runtimeLifecycle) Loop(name string, fn func(context.Context)) {
	g.Go(name, func(ctx context.Context) error { fn(ctx); return nil })
}
func (g *runtimeLifecycle) Start(start func(context.Context) error, stop func(context.Context) error) error {
	if e := start(g.ctx); e != nil {
		return e
	}
	g.stops = append(g.stops, stop)
	return nil
}
func (g *runtimeLifecycle) Close(ctx context.Context) error {
	g.once.Do(func() {
		g.mu.Lock()
		g.closing = true
		g.mu.Unlock()
		g.cancel(context.Canceled)
		go func() {
			defer close(g.finished)
			for i := len(g.interrupts) - 1; i >= 0; i-- {
				g.interrupts[i]()
			}
			confirmed := true
			for i := len(g.stops) - 1; i >= 0; i-- {
				if e := g.stops[i](context.WithoutCancel(g.ctx)); e != nil {
					confirmed = false
					g.Fail(e)
				}
			}
			g.workers.Wait()
			if !confirmed {
				return
			}
			for i := len(g.closers) - 1; i >= 0; i-- {
				if e := g.closers[i](); e != nil {
					g.Fail(errors.New("CONTROL_STORE_CLOSE_FAILED"))
				}
			}
			close(g.joined)
		}()
	})
	select {
	case <-g.finished:
		return g.Failure()
	case <-ctx.Done():
		return errors.New("CONTROL_SHUTDOWN_TIMEOUT")
	}
}

func serveSelected(ctx, startup context.Context, c config.Config, selected *selectedDatabase, service *identity.Service, outbox *mail.Outbox) (result error) {
	if e := c.ValidateServing(); e != nil {
		return e
	}
	if e := verifyServingDatabase(startup, c, selected); e != nil {
		return e
	}
	g := newRuntimeLifecycle(ctx)
	selected.workers = g.joined
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(c.Policy().HTTP.ShutdownSeconds))
		defer cancel()
		result = errors.Join(result, g.Close(cleanup))
	}()
	if e := startInstallationLeaseMonitor(g, selected, c); e != nil {
		return e
	}
	ssoService, e := sso.New(service)
	if e != nil {
		return e
	}
	g.closers = append(g.closers, func() error { ssoService.Close(); return nil })
	service.Sources = ssoService
	tlsConfig, e := c.TLSConfig()
	if e != nil {
		return e
	}
	machineTLSConfig, e := c.MachineTLSConfig()
	if e != nil {
		return e
	}
	for _, root := range []string{c.Server.PortalAssets, c.Server.AdminAssets} {
		st, e := os.Stat(filepath.Join(root, "index.html"))
		if e != nil || !st.Mode().IsRegular() {
			return errors.New("STATIC_ASSETS_MISSING")
		}
	}
	h := identityhttp.New(service)
	if e = h.InitializationError(); e != nil {
		return e
	}
	if e = openModelDirectoryCache(c, h); e != nil {
		return e
	}
	if e = validateServingGraph(selected, service, outbox, h); e != nil {
		return e
	}
	h.RuntimeReadiness = g.Err
	h.SchemaReadiness = func(ctx context.Context) error {
		e := verifyServingDatabase(ctx, c, selected)
		if e != nil && ctx.Err() == nil {
			g.Fail(errors.New("CONTROL_SCHEMA_VERIFICATION_FAILED"))
		}
		return e
	}
	if e = h.Licensing.Service.VerifyStored(startup); e != nil {
		return e
	}
	bootstrapped, e := installationBootstrapped(startup, selected.store)
	if e != nil {
		return e
	}
	if !bootstrapped {
		if e = validateUnbootstrappedFeatures(c); e != nil {
			return e
		}
	}
	if bootstrapped {
		if e = h.Models.Service.VerifyStoredVersions(startup); e != nil {
			return e
		}
		if e = h.Datasets.Service.OpenConfiguredStore(startup); e != nil {
			return e
		}
		if h.Datasets.Service.Store != nil {
			g.closers = append(g.closers, h.Datasets.Service.Store.Close)
		}
		if e = h.Training.OpenConfiguredStore(startup); e != nil {
			return e
		}
		if h.Training.Store != nil {
			g.closers = append(g.closers, h.Training.Store.Close)
		}
		if h.ModelPreparations == nil {
			return errors.New("MODEL_PREPARATION_UNAVAILABLE")
		}
		if e = h.ModelPreparations.OpenConfiguredStore(startup); e != nil {
			return e
		}
		if h.ModelPreparations.Store != nil {
			g.closers = append(g.closers, h.ModelPreparations.Store.Close)
		}
		if e = h.Knowledge.Service.OpenConfiguredProcessor(startup); e != nil {
			return e
		}
		if h.Knowledge.Service.Processor != nil {
			g.interrupts = append(g.interrupts, h.Knowledge.Service.Processor.Close)
		}
	}
	if c.ObjectStorage != nil && c.ObjectStorage.Enabled {
		if !bootstrapped {
			return errors.New("BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES")
		}
		h.ArtifactStores, e = openArtifactStores(startup, c, selected.store, h)
		if e != nil {
			return e
		}
		g.stops = append(g.stops, h.ArtifactStores.Close)
	}
	if c.ImageRegistry != nil {
		if !bootstrapped {
			return errors.New("BOOTSTRAP_REQUIRED_FOR_CONFIGURED_FEATURES")
		}
		h.ImageRegistry, e = openImageRegistry(startup, c, selected.store, h)
		if e != nil {
			return e
		}
		registry := h.ImageRegistry
		g.closers = append(g.closers, func() error { registry.Close(); return nil })
	}
	if c.ReleaseViewConfigFile != "" {
		h.Releases, e = openReleaseView(startup, c, selected, h)
		if e != nil {
			return e
		}
	}
	if c.MiddlewareConfigFile != "" {
		h.Middleware, e = openMiddleware(startup, c, selected, h)
		if e != nil {
			return e
		}
	}
	if c.PrivateClusterConfigFile != "" {
		h.PrivateClusters, e = openPrivateClusters(startup, c, selected, h)
		if e != nil {
			return e
		}
	}
	fence := installationLeaseMiddleware(g, selected, c.Policy().HTTP)
	var serving http.Handler = fence(h)
	var machineServing http.Handler
	if c.MachineServer != nil {
		machineServing = fence(h.MachineHandler())
	}
	if c.MonitoringConfigFile != "" {
		logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
		monitoring, recorder, err := openMonitoring(startup, c, selected, h, logger)
		if err != nil {
			return err
		}
		h.Monitoring = monitoring
		serving, machineServing = monitoringServingHandlers(recorder, serving, machineServing)
	}
	if c.TaskEvents.Enabled {
		h.TaskDeliveries, e = openTaskDeliveries(startup, c, selected.store, h)
		if e != nil {
			return e
		}
		deliveries := h.TaskDeliveries
		g.stops = append(g.stops, func(ctx context.Context) error { return closeTaskDeliveries(ctx, deliveries) })
	}
	listeners, e := httpserver.ListenConfigured(c.Server, tlsConfig, serving, c.MachineServer, machineTLSConfig, machineServing, c.Policy().HTTP)
	if e != nil {
		return errors.New("SERVER_LISTEN_FAILED")
	}
	defer listeners.Close()
	if e = g.Start(h.Notifications.Service.Start, h.Notifications.Service.Stop); e != nil {
		return e
	}
	if e = g.Start(h.Plugins.Service.Start, h.Plugins.Service.Stop); e != nil {
		return e
	}
	if e = g.Start(h.Workflows.Service.Start, h.Workflows.Service.Stop); e != nil {
		return e
	}
	if bootstrapped {
		if e = h.VerifyStored(startup); e != nil {
			return e
		}
	}
	if e = retention.PassSelected(startup, selected.store, c.Policy().Retention); e != nil {
		return e
	}
	g.Loop("SSO_MAINTENANCE", func(ctx context.Context) {
		ssoService.Run(ctx, func(error) { g.Fail(errors.New("SSO_MAINTENANCE_FAILED")) })
	})
	g.Loop("MAIL_WORKER", outbox.Run)
	if h.TelemetryLogs != nil {
		g.Go("TELEMETRY_WRITER", h.TelemetryLogs.Run)
	}
	if h.TelemetryMetrics != nil {
		h.TelemetryMetrics.Add(runtimeSamples)
		if selected.postgres != nil {
			h.TelemetryMetrics.Add(databasePoolSamples(selected.postgres))
		}
		if bootstrapped {
			h.TelemetryMetrics.Add(h.Nodes.Service.LivenessSamples)
			h.TelemetryMetrics.Add(h.Nodes.Service.WakeSamples)
		}
		g.Go("TELEMETRY_METRICS", h.TelemetryMetrics.Run)
	}
	g.Go("RETENTION_WORKER", func(ctx context.Context) error {
		return retention.RunSelected(ctx, selected.store, c.Policy().Retention)
	})
	if h.Middleware != nil {
		g.Go("MIDDLEWARE_WORKER", h.Middleware.Service.Run)
	}
	if h.ArtifactStores != nil {
		g.Go("ARTIFACT_INGRESS", func(ctx context.Context) error {
			return h.ArtifactStores.Run(ctx, func(e error) {
				if ctx.Err() != nil && transaction.IsCancellationOnly(e) {
					return
				}
				g.Fail(runtimeWorkerFailure("ARTIFACT_INGRESS", e))
			})
		})
	}
	if h.TaskDeliveries != nil {
		g.Go("TASK_DELIVERY_WORKER", h.TaskDeliveries.Run)
	}
	if bootstrapped {
		g.Loop("NODE_RECONCILIATION", h.Nodes.Service.Run)
		if selected.postgres != nil {
			if c.LivenessReplica != nil {
				replica, e := openLivenessReplica(*c.LivenessReplica, c.Policy().Nodes)
				if e != nil {
					return e
				}
				h.Nodes.Service.LivenessReplica = replica
				g.Loop("NODE_LIVENESS_REPLICA", func(ctx context.Context) {
					defer replica.Close()
					h.Nodes.Service.RunLivenessReplica(ctx)
				})
			}
			g.Loop("NODE_LIVENESS", h.Nodes.Service.RunLiveness)
			g.Loop("NODE_TASK_WAKES", h.Nodes.Service.RunWakes)
		}
		if c.Models.ArtifactRoot != "" {
			g.Loop("MODEL_IMPORT", h.Models.Service.Run)
		}
		if h.Datasets.Service.Store != nil {
			g.Loop("DATASET_PROCESSOR", h.Datasets.Service.Run)
		}
		if h.Training.Store != nil {
			g.Loop("TRAINING_PROCESSOR", h.Training.Run)
		}
		if h.ModelPreparations.Store != nil {
			g.Loop("PREPARATION_PROCESSOR", h.ModelPreparations.Run)
		}
		if h.Knowledge.Service.Processor != nil {
			g.Loop("KNOWLEDGE_PROCESSOR", h.Knowledge.Service.Run)
		}
	}
	if e = g.Err(); e != nil {
		return e
	}
	if bootstrapped {
		fmt.Println("CONTROL_STARTED")
	} else {
		fmt.Println("CONTROL_STARTED_BOOTSTRAP_REQUIRED")
	}
	if e = listeners.Serve(g.ctx); e != nil && !errors.Is(e, http.ErrServerClosed) {
		result = errors.New("SERVER_FAILED")
	}
	return errors.Join(result, g.Failure())
}

// openLivenessReplica connects to the store gateways share sightings through (ADR 0022).
func openLivenessReplica(c config.LivenessReplicaConfiguration, policy config.NodesPolicy) (*liveness.JetStreamReplica, error) {
	settings, e := c.Settings(config.Seconds(policy.OfflineSeconds))
	if e != nil {
		return nil, e
	}
	return liveness.NewJetStream(settings)
}
