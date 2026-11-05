package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"regexp"
	"strings"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/components"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/discovery"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

type Config struct {
	ImageRegistry    *imageregistry.Config    `json:"-"`
	Runtime          *settings.Policy         `json:"runtime,omitempty"`
	ControlOrigin    string                   `json:"control_origin"`
	CAFile           string                   `json:"ca_file"`
	StateDirectory   string                   `json:"state_directory"`
	DiskPath         string                   `json:"disk_path"`
	JoinTokenFile    string                   `json:"join_token_file"`
	ExecutionProfile string                   `json:"execution_profile,omitempty"`
	Components       components.Config        `json:"components,omitempty"`
	Inference        inference.Config         `json:"inference,omitempty"`
	Training         training.Config          `json:"training,omitempty"`
	Preparation      modelpreparations.Config `json:"model_preparation,omitempty"`
}

func LoadConfig(path string) (Config, error) { return loadConfig(path) }

// LoadPreparationCleanupConfig requires the explicit lifecycle profile for offline cleanup.
func LoadPreparationCleanupConfig(path string) (Config, error) {
	return LoadPreparationCleanupConfigWithEnvironment(path, "")
}

func LoadPreparationCleanupConfigWithEnvironment(path, environment string) (Config, error) {
	c, e := LoadConfigWithEnvironment(path, environment)
	if e != nil {
		return c, e
	}
	if c.ExecutionProfile != wire.DockerModelLifecycleProfile {
		return c, errors.New("AGENT_ARGUMENT_INVALID")
	}
	return c, nil
}
func loadConfig(path string) (Config, error) {
	defaults := settings.Default()
	c := Config{Runtime: &defaults}
	b, e := state.ProtectedFile(path)
	if e != nil {
		return c, e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	return validateConfig(c)
}

func validateConfig(c Config) (Config, error) {
	var e error
	if e = c.Policy().Validate(); e != nil {
		return c, e
	}
	if e = state.Directory(c.StateDirectory); e != nil {
		return c, e
	}
	if c.ExecutionProfile == "" {
		c.ExecutionProfile = wire.ReadOnlyProfile
	}
	if !wire.KnownProfile(c.ExecutionProfile) {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	if !wire.IsManagementProfile(c.ExecutionProfile) && (c.Components.RootDirectory != "" || c.Components.ServiceUser != "") {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	if !wire.IsInferenceProfile(c.ExecutionProfile) && (c.Inference.RootDirectory != "" || c.Inference.DockerSocket != "") {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	if c.ExecutionProfile == wire.DockerAIProfile || c.ExecutionProfile == wire.DockerModelLifecycleProfile {
		if training.ValidateAgentPolicy(c.Training, c.Policy()) != nil || c.Inference.RootDirectory == "" || c.Inference.DockerSocket != c.Training.DockerSocket || overlap(c.Training.RootDirectory, c.StateDirectory) || overlap(c.Training.RootDirectory, c.Inference.RootDirectory) {
			return c, errors.New("AGENT_CONFIG_INVALID")
		}
	} else if c.Training.RootDirectory != "" || c.Training.DockerSocket != "" || c.Training.ImageID != "" || c.Training.ImageReference != "" || c.Training.WorkerSourceSHA256 != "" || c.Training.Runtime != nil {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	if c.ExecutionProfile == wire.DockerModelLifecycleProfile {
		if modelpreparations.ValidateAgentPolicy(c.Preparation, c.Policy()) != nil || c.Preparation.DockerSocket != c.Training.DockerSocket || overlap(c.Preparation.RootDirectory, c.StateDirectory) || overlap(c.Preparation.RootDirectory, c.Training.RootDirectory) || overlap(c.Preparation.RootDirectory, c.Inference.RootDirectory) {
			return c, errors.New("AGENT_CONFIG_INVALID")
		}
	} else if c.Preparation.RootDirectory != "" || c.Preparation.DockerSocket != "" || c.Preparation.ImageID != "" || c.Preparation.ImageReference != "" || c.Preparation.WorkerSourceSHA256 != "" || c.Preparation.Runtime != nil || c.Preparation.OutputSeconds != 0 {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	if !filepath.IsAbs(c.DiskPath) || c.CAFile != "" && !filepath.IsAbs(c.CAFile) || c.JoinTokenFile != "" && !filepath.IsAbs(c.JoinTokenFile) {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	return c, nil
}
func overlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
func profile(c Config) string {
	if c.ExecutionProfile == "" {
		return wire.ReadOnlyProfile
	}
	return c.ExecutionProfile
}
func handshake(f wire.Facts, c Config) wire.Handshake {
	p := profile(c)
	h := wire.Handshake{ProtocolVersion: wire.ProfileVersion(p), Capabilities: wire.Capabilities(p), Facts: f}
	if p != wire.ReadOnlyProfile {
		h.ExecutionProfile = p
	}
	return h
}
func manager(c Config) (componentLifecycle, error) {
	if !wire.IsManagementProfile(profile(c)) {
		return nil, nil
	}
	m, e := components.New(c.Components, c.StateDirectory, c.Policy())
	if e != nil {
		return nil, e
	}
	if profile(c) != wire.SystemdMaintenanceProfile {
		required, e := m.RequiresMaintenance()
		if e != nil {
			return nil, e
		}
		if required {
			return nil, errors.New("COMPONENT_MAINTENANCE_REQUIRED")
		}
	}
	return m, nil
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func token(v string) bool {
	b, e := base64.RawURLEncoding.DecodeString(v)
	return e == nil && len(b) == 32 && len(v) == 43
}

func Enroll(ctx context.Context, c Config) error {
	if _, e := manager(c); e != nil {
		return e
	}
	if wire.IsInferenceProfile(profile(c)) {
		m, e := inference.New(c.Inference, c.StateDirectory, c.Policy())
		if e != nil {
			return e
		}
		m.Close()
	}
	if profile(c) == wire.DockerAIProfile || profile(c) == wire.DockerModelLifecycleProfile {
		m, e := training.New(c.Training, c.StateDirectory, c.Policy())
		if e != nil {
			return e
		}
		e = m.CheckInstallation(ctx)
		m.Close()
		if e != nil {
			return e
		}
	}
	if profile(c) == wire.DockerModelLifecycleProfile {
		m, e := modelpreparations.New(c.Preparation, c.StateDirectory, c.Policy())
		if e != nil {
			return e
		}
		e = m.CheckInstallation(ctx)
		m.Close()
		if e != nil {
			return e
		}
	}
	lock, e := state.Lock(c.StateDirectory)
	if e != nil {
		return e
	}
	defer lock.Close()
	if e := state.Directory(c.StateDirectory); e != nil {
		return e
	}
	if state.Exists(c.StateDirectory) {
		return errors.New("IDENTITY_ALREADY_EXISTS")
	}
	probe, e := os.CreateTemp(c.StateDirectory, ".write-probe-*")
	if e != nil {
		return errors.New("STATE_DIRECTORY_NOT_WRITABLE")
	}
	name := probe.Name()
	e = probe.Sync()
	probe.Close()
	os.Remove(name)
	if e != nil {
		return errors.New("STATE_DIRECTORY_NOT_WRITABLE")
	}
	f, e := discovery.Collect(c.DiskPath)
	if e != nil {
		return e
	}
	b, e := state.ProtectedFile(c.JoinTokenFile)
	if e != nil {
		return e
	}
	join := string(b)
	if len(join) > 0 && join[len(join)-1] == '\n' {
		join = join[:len(join)-1]
	}
	if !token(join) {
		return errors.New("JOIN_TOKEN_INVALID")
	}
	connection, e := client.New(c.ControlOrigin, c.CAFile, c.Policy())
	if e != nil {
		return e
	}
	defer connection.Close()
	var response wire.Enrolled
	if e = connection.Post(ctx, "/agent/v1/enroll", wire.Enrollment{JoinToken: join, Handshake: handshake(f, c)}, &response); e != nil {
		return e
	}
	if !idPattern.MatchString(response.NodeID) || !token(response.Credential) || response.ProtocolVersion != wire.ProfileVersion(profile(c)) || response.HeartbeatSeconds < wire.MinHeartbeatSeconds || response.HeartbeatSeconds > wire.MaxHeartbeatSeconds || !(wire.Scope{SiteID: response.SiteID, CellID: response.CellID}).Valid() {
		return errors.New("ENROLLMENT_RESPONSE_INVALID")
	}
	if e = state.SaveNew(c.StateDirectory, state.Identity{NodeID: response.NodeID, SiteID: response.SiteID, CellID: response.CellID, Credential: response.Credential, ControlOrigin: c.ControlOrigin, ProtocolVersion: wire.ProfileVersion(profile(c)), ExecutionProfile: profile(c)}); e != nil {
		return errors.New("ENROLLMENT_COMMITTED_LOCAL_STATE_FAILED")
	}
	return nil
}
// cycle runs one cycle on a session of its own, for callers outside the run loop.
func cycle(ctx context.Context, c Config, connection *client.Client, identity state.Identity, m componentLifecycle, im *inference.Manager, tm *training.Manager, pm *modelpreparations.Manager, rm taskImageLoader) error {
	var session *client.Session
	if connection != nil {
		session = connection.Session(identity.NodeID)
		defer session.Close()
	}
	return sessionCycle(ctx, c, connection, session, identity, m, im, tm, pm, rm)
}

// sessionCycle recovers interrupted work, reports the node, then claims and runs at most
// one task. Heartbeats, claims and acknowledgements use the session; task traffic uses
// HTTPS.
func sessionCycle(ctx context.Context, c Config, connection *client.Client, session *client.Session, identity state.Identity, m componentLifecycle, im *inference.Manager, tm *training.Manager, pm *modelpreparations.Manager, rm taskImageLoader) error {
	if e := recoverLocalWork(ctx, c, connection, identity, m, tm, pm); e != nil {
		return e
	}
	f, e := discovery.Collect(c.DiskPath)
	if e != nil {
		return e
	}
	return claimAndRun(ctx, c, connection, session, identity, f, m, im, tm, pm, rm)
}

// recoverLocalWork reconciles interrupted work from the local journals. It runs at the
// start of every cycle and for a full resync.
func recoverLocalWork(ctx context.Context, c Config, connection *client.Client, identity state.Identity, m componentLifecycle, tm *training.Manager, pm *modelpreparations.Manager) error {
	// An expired lease or cancelled maintenance can leave a durable transition
	// in this process. Reconcile it before publishing state or claiming more work.
	if e := recoverComponent(ctx, c, m); e != nil {
		return e
	}
	if pm != nil {
		bounded, stop := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		e := pm.Recover(bounded, identity.NodeID, connection)
		stop()
		if e != nil {
			return e
		}
	}
	if tm != nil {
		bounded, stop := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		e := tm.Recover(bounded, identity.NodeID, connection)
		stop()
		if e != nil {
			return e
		}
	}
	return nil
}

func claimAndRun(ctx context.Context, c Config, connection *client.Client, session *client.Session, identity state.Identity, f wire.Facts, m componentLifecycle, im *inference.Manager, tm *training.Manager, pm *modelpreparations.Manager, rm taskImageLoader) error {
	base := "/agent/v1/nodes/" + url.PathEscape(identity.NodeID)
	h := handshake(f, c)
	if m != nil {
		o := m.Observe(ctx)
		h.Component = &o
	}
	if im != nil {
		o := registryObservation(im.Observe(ctx, identity.NodeID), rm)
		h.Inference = &o
	}
	disposition, e := session.Heartbeat(ctx, h)
	if e != nil {
		return e
	}
	if disposition.Status == "disabled" {
		return nil
	}
	if disposition.Status != "active" {
		return errors.New("AGENT_DISPOSITION_INVALID")
	}
	cursor, e := state.LoadCursor(c.StateDirectory)
	if e != nil {
		return e
	}
	// Control holds the claim for most of the interval and answers as soon as work arrives.
	claim, e := session.Claim(ctx, session.Pacing().ClaimWaitSeconds(), cursor)
	if e != nil {
		return e
	}
	if claim.Resync != nil {
		// Control's record and this cursor disagree (ADR 0011). Recover all local work
		// from the journals, then adopt control's cursor; the next cycle reports a full
		// heartbeat and claims from there.
		if e := recoverLocalWork(ctx, c, connection, identity, m, tm, pm); e != nil {
			return e
		}
		slog.Info("agent_task_resync", "acknowledged_sequence", claim.Resync.AcknowledgedSequence)
		return state.SaveCursor(c.StateDirectory, claim.Resync.AcknowledgedSequence)
	}
	if claim.Lease == nil {
		return nil
	}
	lease := claim.Lease
	if e := validateExecutionLease(profile(c), *lease); e != nil {
		return e
	}
	// Acknowledge before executing anything, and execute only once control confirms. The
	// cursor is saved first, so it is never behind what control recorded. A lease that is
	// not acknowledged is delivered again on the next claim without spending an attempt.
	if e := wire.AcceptLease(cursor, *lease); e != nil {
		return e
	}
	if lease.Sequence > cursor {
		if e := state.SaveCursor(c.StateDirectory, lease.Sequence); e != nil {
			return e
		}
	}
	if _, e := session.Acknowledge(ctx, *lease); e != nil {
		return e
	}
	if lease.Operation == wire.ModelPreparationRunOperation {
		if pm == nil {
			return errors.New("AGENT_TASK_UNSUPPORTED")
		}
		return executePreparation(ctx, connection, base, identity.NodeID, *lease, pm, c.Preparation.OutputTimeoutSeconds())
	}
	if lease.Operation == wire.TrainingRunOperation {
		if tm == nil || (profile(c) != wire.DockerAIProfile && profile(c) != wire.DockerModelLifecycleProfile) || lease.Training == nil || lease.Component != nil || lease.Inference != nil || lease.Training.Validate() != nil {
			return errors.New("AGENT_TASK_UNSUPPORTED")
		}
		return executeTraining(ctx, connection, base, identity.NodeID, *lease, tm)
	}
	if lease.Training != nil {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	if wire.IsInferenceOperation(lease.Operation) {
		if im == nil || !wire.IsInferenceProfile(profile(c)) || lease.Component != nil || lease.Inference == nil || lease.Inference.Validate(lease.Operation) != nil {
			return errors.New("AGENT_TASK_UNSUPPORTED")
		}
		return executeInference(ctx, connection, base, identity.NodeID, *lease, im, rm)
	}
	if lease.Inference != nil {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	if wire.IsComponentOperation(lease.Operation) {
		if m == nil || lease.Component == nil || lease.Component.ValidateForProfile(profile(c), lease.Operation) != nil {
			return errors.New("AGENT_TASK_UNSUPPORTED")
		}
		return executeComponent(ctx, connection, base, *lease, m, profile(c))
	}
	if lease.Component != nil {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success"}
	refreshed, e := discovery.Collect(c.DiskPath)
	if e != nil {
		result.Outcome = "failure"
		result.ErrorCode = "DISCOVERY_FAILED"
	} else {
		result.Facts = &refreshed
	}
	var completed struct {
		State string `json:"state"`
	}
	return connection.Post(ctx, base+"/tasks/"+lease.TaskID+"/results", result, &completed)
}
func Run(ctx context.Context, c Config) error {
	if _, e := discovery.Collect(c.DiskPath); e != nil {
		return e
	}
	lock, e := state.Lock(c.StateDirectory)
	if e != nil {
		return e
	}
	defer lock.Close()
	identity, e := state.Load(c.StateDirectory)
	if e != nil {
		return e
	}
	if identity.ControlOrigin != c.ControlOrigin || identity.ProtocolVersion != wire.ProfileVersion(profile(c)) || (identity.ExecutionProfile != "" && identity.ExecutionProfile != profile(c)) || !idPattern.MatchString(identity.NodeID) || !token(identity.Credential) {
		return errors.New("IDENTITY_INVALID")
	}
	connection, e := client.New(c.ControlOrigin, c.CAFile, c.Policy())
	if e != nil {
		return e
	}
	defer connection.Close()
	connection.Credential = identity.Credential
	connection.Scope = identity.Scope()
	m, e := manager(c)
	if e != nil {
		return e
	}
	if e = recoverComponent(ctx, c, m); e != nil {
		return e
	}
	var im *inference.Manager
	if wire.IsInferenceProfile(profile(c)) {
		im, e = inference.New(c.Inference, c.StateDirectory, c.Policy())
		if e != nil {
			return e
		}
		defer im.Close()
	}
	rm, closeRegistry, e := registryLoader(c, connection)
	if e != nil {
		return e
	}
	defer closeRegistry()
	var tm *training.Manager
	if profile(c) == wire.DockerAIProfile || profile(c) == wire.DockerModelLifecycleProfile {
		tm, e = training.New(c.Training, c.StateDirectory, c.Policy())
		if e != nil {
			return e
		}
		defer tm.Close()
		bounded, stop := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		e = tm.Recover(bounded, identity.NodeID, connection)
		if e == nil {
			e = tm.CheckInstallation(bounded)
		}
		stop()
		if e != nil {
			return e
		}
	}
	var pm *modelpreparations.Manager
	if profile(c) == wire.DockerModelLifecycleProfile {
		pm, e = modelpreparations.New(c.Preparation, c.StateDirectory, c.Policy())
		if e != nil {
			return e
		}
		defer pm.Close()
		bounded, stop := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		e = pm.Recover(bounded, identity.NodeID, connection)
		if e == nil {
			e = pm.CheckInstallation(bounded)
		}
		stop()
		if e != nil {
			return e
		}
	}
	session := connection.Session(identity.NodeID)
	defer session.Close()
	pace := newPacer()
	transport := ""
	for {
		started := time.Now()
		e = sessionCycle(ctx, c, connection, session, identity, m, im, tm, pm, rm)
		if ctx.Err() != nil {
			return nil
		}
		if current := session.Transport(); current != transport && current != "" {
			slog.Info("agent_session_transport", "transport", current)
			transport = current
		}
		var delay time.Duration
		if e != nil {
			var p *client.Problem
			if errors.As(e, &p) && p.Status == 401 {
				return errors.New("AGENT_CREDENTIAL_REVOKED")
			}
			slog.Warn("agent_cycle_failed", "code", e.Error())
			delay = pace.afterFailure(session.Pacing(), e)
		} else {
			// The claim wait is part of the interval, so a cycle starts once per interval
			// when idle and right away after a task.
			delay = pace.afterSuccess(session.Pacing(), time.Since(started))
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func executeComponent(ctx context.Context, connection *client.Client, base string, lease wire.Lease, m componentLifecycle, executionProfile string) error {
	return fencedExecution(ctx, connection, base, lease, settings.Seconds(connection.Policy().ComponentTaskSeconds), func(bounded context.Context, taskBase string) (wire.Result, error) {
		download := func(downloadContext context.Context, destination string) error {
			return connection.ArtifactVersion(downloadContext, taskBase+"/artifact", lease.LeaseToken, destination, lease.Component.Version)
		}
		var observation wire.ComponentObservation
		var logs *wire.ComponentLogs
		var e error
		if executionProfile == wire.SystemdMaintenanceProfile {
			observation, logs, e = m.ExecuteMaintenance(bounded, lease.TaskID, *lease.Component, download)
		} else {
			observation, e = m.Execute(bounded, *lease.Component, download)
		}
		result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Component: &observation, Logs: logs}
		if e != nil {
			result.Outcome = "failure"
			result.ErrorCode = observation.ErrorCode
		}
		return result, nil
	})
}
func executeInference(ctx context.Context, connection *client.Client, base, node string, lease wire.Lease, m *inference.Manager, rm taskImageLoader) error {
	limit := settings.Seconds(connection.Policy().RemoveSeconds)
	if lease.Inference.Action == "deploy" {
		limit = settings.Seconds(connection.Policy().InferenceTaskSeconds)
	}
	return fencedExecution(ctx, connection, base, lease, limit, func(bounded context.Context, taskBase string) (wire.Result, error) {
		if e := ensureRegistryTaskImage(bounded, node, lease, rm); e != nil {
			return wire.Result{}, e
		}
		download := func(downloadContext context.Context, file wire.ModelFile, destination string) error {
			if lease.Inference.Derived != nil {
				return connection.DerivedModelFile(downloadContext, taskBase+"/model-files", lease.LeaseToken, *lease.Inference, file, destination)
			}
			return connection.ModelFile(downloadContext, taskBase+"/model-files", lease.LeaseToken, file, destination)
		}
		observation, e := m.Execute(bounded, node, lease.TaskID, *lease.Inference, download)
		observation = registryObservation(observation, rm)
		result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success", Inference: &observation}
		if e != nil {
			result.Outcome = "failure"
			result.ErrorCode = observation.ErrorCode
		}
		if result.ValidateInference(*lease.Inference) != nil {
			return result, errors.New("AGENT_RESULT_INVALID")
		}
		return result, nil
	})
}

func fencedExecution(ctx context.Context, connection *client.Client, base string, lease wire.Lease, timeout time.Duration, execute func(context.Context, string) (wire.Result, error)) error {
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	taskBase := base + "/tasks/" + lease.TaskID
	renew := leaseRenewal(connection, taskBase, lease)
	expiry, e := renew(bounded)
	if e != nil {
		return e
	}
	stopRenewal := make(chan struct{})
	done, renewed := renewWhileRunning(bounded, cancel, renew, lease.TaskID, expiry, stopRenewal)
	result, executionError := execute(bounded, taskBase)
	if executionError != nil {
		close(stopRenewal)
		<-done
		return executionError
	}
	// Keep renewal active until effects finish, then join it before submitting the fenced result.
	close(stopRenewal)
	<-done
	cancel()
	select {
	case renewalError := <-renewed:
		return renewalError
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var completed struct {
		State string `json:"state"`
	}
	return connection.Post(ctx, taskBase+"/results", result, &completed)
}

// Use a monotonic request-start duration, never the agent's wall clock, to bound effects.
func renewalDeadline(started time.Time, lease wire.RenewedLease) (time.Time, error) {
	if lease.LeaseExpiresAt.IsZero() || lease.ValidForMilliseconds < 1 || lease.ValidForMilliseconds > 30000 {
		return time.Time{}, errors.New("AGENT_LEASE_STALE")
	}
	deadline := started.Add(time.Duration(lease.ValidForMilliseconds) * time.Millisecond)
	if !deadline.After(time.Now()) {
		return time.Time{}, errors.New("AGENT_LEASE_STALE")
	}
	return deadline, nil
}

func (c Config) Policy() settings.Policy { return settings.Resolve(c.Runtime) }
