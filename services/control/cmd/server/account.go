package main

import (
	"context"
	cryptotls "crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity"
	identityhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/identity/http"
	"github.com/Passion-Flow/ownsolid/services/control/internal/modules/sso"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/config"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/httpserver"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/mail"
	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/retention"
)

// Account mode constructs only identity, signed-license admission, embedded locales,
// encrypted SMTP delivery and bounded account retention. M03 optionally adds the existing node sweep; no later worker is started.
func serveAccount(ctx, startup context.Context, c config.Config, d *selectedDatabase, s *identity.Service, outbox *mail.Outbox) (result error) {
	if e := c.ValidateApplicationProfile(); e != nil {
		return e
	}
	if d == nil || d.postgres == nil || d.profile != c.Server.Profile() || !c.AccountRuntime() || s == nil || outbox == nil || s.Mail != outbox {
		return errors.New("ACCOUNT_COMPOSITION_INVALID")
	}
	g := newRuntimeLifecycle(ctx)
	d.workers = g.joined
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.Seconds(c.Policy().HTTP.ShutdownSeconds))
		defer cancel()
		result = errors.Join(result, g.Close(cleanup))
	}()
	if c.SSORuntime() {
		service, err := sso.New(s)
		if err != nil {
			return err
		}
		s.Sources = service
		g.closers = append(g.closers, func() error { service.Close(); return nil })
	}
	tls, e := c.TLSConfig()
	if e != nil {
		return e
	}
	for _, root := range []string{c.Server.PortalAssets, c.Server.AdminAssets} {
		v, e := os.Stat(filepath.Join(root, "index.html"))
		if e != nil || !v.Mode().IsRegular() {
			return errors.New("STATIC_ASSETS_MISSING")
		}
	}
	h := identityhttp.New(s)
	if e = h.InitializationError(); e != nil {
		return e
	}
	if e = openModelDirectoryCache(c, h); e != nil {
		return e
	}
	h.RuntimeReadiness = g.Err
	h.SchemaReadiness = func(ctx context.Context) error {
		e := d.Verify(ctx)
		if e != nil && ctx.Err() == nil {
			g.Fail(errors.New("CONTROL_SCHEMA_VERIFICATION_FAILED"))
		}
		return e
	}
	if e = d.Verify(startup); e != nil {
		return e
	}
	if e = startInstallationLeaseMonitor(g, d, c); e != nil {
		return e
	}
	if c.DatasetRuntime() {
		if e = h.Datasets.Service.OpenConfiguredStore(startup); e != nil {
			return e
		}
		g.closers = append(g.closers, h.Datasets.Service.Store.Close)
	}
	if c.TrainingRuntime() {
		if e = h.Training.OpenConfiguredStore(startup); e != nil {
			return e
		}
		g.closers = append(g.closers, h.Training.Store.Close)
	}
	if c.LifecycleRuntime() {
		if e = h.ModelPreparations.OpenConfiguredStore(startup); e != nil {
			return e
		}
		g.closers = append(g.closers, h.ModelPreparations.Store.Close)
	}
	if c.KnowledgeRuntime() {
		if e = h.Knowledge.Service.OpenConfiguredProcessor(startup); e != nil {
			return e
		}
		if h.Knowledge.Service.Processor != nil {
			g.interrupts = append(g.interrupts, h.Knowledge.Service.Processor.Close)
		}
	}
	if c.PluginsRuntime() {
		if e = g.Start(h.Plugins.Service.Start, h.Plugins.Service.Stop); e != nil {
			return e
		}
	}
	if c.WorkflowsRuntime() {
		if e = g.Start(h.Workflows.Service.Start, h.Workflows.Service.Stop); e != nil {
			return e
		}
	}
	if c.PortalRuntime() {
		if e = g.Start(h.Notifications.Service.Start, h.Notifications.Service.Stop); e != nil {
			return e
		}
	}
	if c.ReleasesRuntime() {
		h.Releases, e = openReleaseView(startup, c, d, h)
		if e != nil {
			return e
		}
	}
	if e = h.VerifyStored(startup); e != nil {
		return e
	}
	if e = retention.PassSelected(startup, d.store, c.Policy().Retention); e != nil {
		return e
	}
	var machineTLS *cryptotls.Config
	var machineHandler http.Handler
	if c.NodeRuntime() {
		machineTLS, e = c.MachineTLSConfig()
		if e != nil {
			return e
		}
	}
	if c.MachineServer != nil {
		machineHandler = h.MachineHandler()
	}
	fence := installationLeaseMiddleware(g, d, c.Policy().HTTP)
	serving := fence(h)
	if machineHandler != nil {
		machineHandler = fence(machineHandler)
	}
	listeners, e := httpserver.ListenConfigured(c.Server, tls, serving, c.MachineServer, machineTLS, machineHandler, c.Policy().HTTP)
	if e != nil {
		return errors.New("SERVER_LISTEN_FAILED")
	}
	defer listeners.Close()
	if c.NodeRuntime() {
		g.Loop("NODE_WORKER", h.Nodes.Service.Run)
	}
	if c.ModelsRuntime() {
		g.Loop("MODEL_WORKER", h.Models.Service.Run)
	}
	if c.DatasetRuntime() {
		g.Loop("DATASET_PROCESSOR", h.Datasets.Service.Run)
	}
	if c.TrainingRuntime() {
		g.Loop("TRAINING_WORKER", h.Training.Run)
	}
	if c.LifecycleRuntime() {
		g.Loop("PREPARATION_PROCESSOR", h.ModelPreparations.Run)
	}
	if c.SSORuntime() {
		g.Loop("SSO_MAINTENANCE", func(ctx context.Context) {
			h.SSO.Service.Run(ctx, func(error) { g.Fail(errors.New("SSO_MAINTENANCE_FAILED")) })
		})
	}
	if c.KnowledgeRuntime() && h.Knowledge.Service.Processor != nil {
		g.Loop("KNOWLEDGE_PROCESSOR", h.Knowledge.Service.Run)
	}
	g.Loop("MAIL_WORKER", outbox.Run)
	g.Go("RETENTION_WORKER", func(ctx context.Context) error { return retention.RunSelected(ctx, d.store, c.Policy().Retention) })
	fmt.Println("CONTROL_STARTED")
	if e = listeners.Serve(g.ctx); e != nil && !errors.Is(e, http.ErrServerClosed) {
		result = errors.New("SERVER_FAILED")
	}
	return errors.Join(result, g.Failure())
}
