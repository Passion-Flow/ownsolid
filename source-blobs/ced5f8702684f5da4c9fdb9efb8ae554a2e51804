package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/components"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/discovery"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/imageregistry"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/kubernetes"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/modelpreparations"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/progress"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/training"
)

type Config struct {
	ImageRegistry *imageregistry.Config `json:"-"`
	Runtime       *settings.Policy      `json:"runtime,omitempty"`
	ControlOrigin string                `json:"control_origin"`
	// SessionAddresses are the agent gateways, host:port, that carry the session stream
	// when the cell runs them (ADR 0048). HTTPS calls stay on ControlOrigin.
	SessionAddresses []string `json:"session_addresses,omitempty"`
	// GatewayRebalanceSeconds is the minimum wait between checks of preferred gateways.
	// Zero uses the default.
	GatewayRebalanceSeconds int                      `json:"gateway_rebalance_seconds,omitempty"`
	CAFile                  string                   `json:"ca_file"`
	StateDirectory          string                   `json:"state_directory"`
	DiskPath                string                   `json:"disk_path"`
	JoinTokenFile           string                   `json:"join_token_file"`
	ExecutionProfile        string                   `json:"execution_profile,omitempty"`
	Components              components.Config        `json:"components,omitempty"`
	Inference               inference.Config         `json:"inference,omitempty"`
	Training                training.Config          `json:"training,omitempty"`
	Preparation             modelpreparations.Config `json:"model_preparation,omitempty"`
	// Kubernetes bounds how a node of the Kubernetes profile installs RKE2 (ADR 0051).
	Kubernetes kubernetes.Config `json:"kubernetes,omitempty"`
	// Gateway is the model gateway this serving node runs beside its model (ADR 0052).
	Gateway *inference.GatewayConfig `json:"gateway,omitempty"`
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
	if c.ExecutionProfile == wire.KubernetesNodeProfile {
		if c.Kubernetes.WithDefaults().Validate() != nil {
			return c, errors.New("AGENT_CONFIG_INVALID")
		}
	} else if c.Kubernetes != (kubernetes.Config{}) {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	if c.Gateway != nil {
		if c.ExecutionProfile != wire.DockerInferenceProfile || c.Gateway.Validate() != nil || c.CAFile == "" || overlap(c.Gateway.RootDirectory, c.StateDirectory) || overlap(c.Gateway.RootDirectory, c.Inference.RootDirectory) {
			return c, errors.New("AGENT_CONFIG_INVALID")
		}
	}
	if c.GatewayRebalanceSeconds != 0 && (c.GatewayRebalanceSeconds < wire.MinGatewayRebalanceSeconds || c.GatewayRebalanceSeconds > wire.MaxGatewayRebalanceSeconds) {
		return c, errors.New("AGENT_CONFIG_INVALID")
	}
	if wire.ValidateSessionAddresses(c.SessionAddresses) != nil {
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
	if profile(c) == wire.KubernetesNodeProfile {
		if _, e := kubernetes.New(c.Kubernetes, c.StateDirectory); e != nil {
			return e
		}
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
	// The key is saved before it is asked about, so a certificate never arrives for a key
	// that a crash has lost.
	key, e := wire.NewCertificateKey()
	if e != nil {
		return e
	}
	if e = state.SaveCertificate(c.StateDirectory, state.Certificate{PrivateKey: key}); e != nil {
		return e
	}
	request, e := wire.NewCertificateRequest(key)
	if e != nil {
		return e
	}
	var response wire.Enrolled
	if e = enrollPaced(ctx, connection, wire.Enrollment{JoinToken: join, CertificateRequest: request, Handshake: handshake(f, c)}, &response); e != nil {
		return e
	}
	scope := wire.Scope{SiteID: response.SiteID, CellID: response.CellID}
	if !idPattern.MatchString(response.NodeID) || !token(response.Credential) || response.ProtocolVersion != wire.ProfileVersion(profile(c)) || response.HeartbeatSeconds < wire.MinHeartbeatSeconds || response.HeartbeatSeconds > wire.MaxHeartbeatSeconds || !scope.Valid() {
		return errors.New("ENROLLMENT_RESPONSE_INVALID")
	}
	if response.Certificate != "" {
		leaf, e := checkEnrollmentCertificate(response.Certificate, key, response.NodeID, scope)
		if e != nil {
			return errors.New("ENROLLMENT_RESPONSE_INVALID")
		}
		if e = state.SaveCertificate(c.StateDirectory, state.Certificate{PrivateKey: key, Certificate: response.Certificate, Confirmed: true, RenewAt: renewAt(leaf)}); e != nil {
			return errors.New("ENROLLMENT_COMMITTED_LOCAL_STATE_FAILED")
		}
	}
	if e = state.SaveNew(c.StateDirectory, state.Identity{NodeID: response.NodeID, SiteID: response.SiteID, CellID: response.CellID, Credential: response.Credential, ControlOrigin: c.ControlOrigin, ProtocolVersion: wire.ProfileVersion(profile(c)), ExecutionProfile: profile(c)}); e != nil {
		return errors.New("ENROLLMENT_COMMITTED_LOCAL_STATE_FAILED")
	}
	return nil
}

// enrollPaced enrolls, waiting when control says the node's rack has to wait its turn (ADR
// 0050). The join token is not spent by a refusal, which comes before any enrollment work.
func enrollPaced(ctx context.Context, connection *client.Client, enrollment wire.Enrollment, response *wire.Enrolled) error {
	for {
		e := connection.Post(ctx, "/agent/v1/enroll", enrollment, response)
		var p *client.Problem
		if !errors.As(e, &p) || p.Status != http.StatusTooManyRequests || p.RetryAfter <= 0 {
			return e
		}
		slog.Info("agent_enrollment_waiting", "seconds", int(p.RetryAfter/time.Second))
		timer := time.NewTimer(client.AfterHint(p.RetryAfter))
		select {
		case <-ctx.Done():
			timer.Stop()
			return e
		case <-timer.C:
		}
	}
}

// cycle runs one cycle on a session of its own, for callers outside the run loop.
func cycle(ctx context.Context, c Config, connection *client.Client, identity state.Identity, x executors) error {
	var session *client.Session
	if connection != nil {
		session = connection.Session(identity.NodeID)
		defer session.Close()
	}
	return sessionCycle(ctx, c, connection, session, identity, x)
}

// sessionCycle recovers interrupted work, reports the node, then claims and runs at most
// one task. Heartbeats, claims and acknowledgements use the session; task traffic uses
// HTTPS.
func sessionCycle(ctx context.Context, c Config, connection *client.Client, session *client.Session, identity state.Identity, x executors) error {
	if e := recoverLocalWork(ctx, c, connection, identity, x.components, x.training, x.preparations); e != nil {
		return e
	}
	f, e := discovery.Collect(c.DiskPath)
	if e != nil {
		return e
	}
	return claimAndRun(ctx, c, connection, session, identity, f, x)
}

// recoverLocalWork reconciles interrupted work from the local journals: an attempt an
// earlier process left running is adopted and followed to its end, and anything else is
// closed. It runs at the start of every cycle and for a full resync, and is the first thing
// a new process does, so startup itself never needs control.
func recoverLocalWork(ctx context.Context, c Config, connection *client.Client, identity state.Identity, m componentLifecycle, tm *training.Manager, pm *modelpreparations.Manager) error {
	// An expired lease or cancelled maintenance can leave a durable transition
	// in this process. Reconcile it before publishing state or claiming more work.
	if e := recoverComponent(ctx, c, m); e != nil {
		return e
	}
	if pm != nil {
		if e := resumePreparation(ctx, connection, identity.NodeID, pm, c.Preparation.OutputTimeoutSeconds()); e != nil {
			return e
		}
		bounded, stop := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		e := pm.Recover(bounded, identity.NodeID, connection)
		stop()
		if e != nil {
			return e
		}
	}
	if tm != nil {
		if e := resumeTraining(ctx, connection, identity.NodeID, tm); e != nil {
			return e
		}
		bounded, stop := context.WithTimeout(ctx, settings.Seconds(c.Policy().RemoveSeconds))
		e := tm.Recover(bounded, identity.NodeID, connection)
		stop()
		if e != nil {
			return e
		}
	}
	return nil
}

func claimAndRun(ctx context.Context, c Config, connection *client.Client, session *client.Session, identity state.Identity, f wire.Facts, x executors) error {
	m, im, tm, pm, rm := x.components, x.inference, x.training, x.preparations, x.registry
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
	if wire.IsKubernetesOperation(lease.Operation) {
		if x.kubernetes == nil || lease.Kubernetes == nil || lease.Kubernetes.Validate(lease.Operation) != nil {
			return errors.New("AGENT_TASK_UNSUPPORTED")
		}
		return executeKubernetes(ctx, connection, base, *lease, x.kubernetes, c.Kubernetes.TaskTimeout())
	}
	if lease.Kubernetes != nil {
		return errors.New("AGENT_TASK_UNSUPPORTED")
	}
	if wire.IsGatewayOperation(lease.Operation) {
		if x.gateway == nil || profile(c) != wire.DockerInferenceProfile || lease.Component != nil || lease.Gateway == nil || lease.Gateway.Validate(lease.Operation) != nil {
			return errors.New("AGENT_TASK_UNSUPPORTED")
		}
		return executeGateway(ctx, connection, base, identity, *lease, x.gateway)
	}
	if lease.Gateway != nil {
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
	taskBase := base + "/tasks/" + lease.TaskID
	reporter := taskReporter(connection, taskBase, *lease)
	reporting := progress.With(ctx, reporter)
	reporter.Start(reporting)
	result := wire.Result{LeaseToken: lease.LeaseToken, Outcome: "success"}
	refreshed, e := discovery.CollectReporting(reporting, c.DiskPath)
	if e != nil {
		progress.Error(reporting, "Reading this node's facts failed: %s", e.Error())
		result.Outcome = "failure"
		result.ErrorCode = "DISCOVERY_FAILED"
	} else {
		result.Facts = &refreshed
	}
	reporter.Finish(ctx, e != nil)
	var completed struct {
		State string `json:"state"`
	}
	return connection.Post(ctx, taskBase+"/results", result, &completed)
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
	identity, e := loadBoundIdentity(c)
	if e != nil {
		return e
	}
	connection, e := client.New(c.ControlOrigin, c.CAFile, c.Policy())
	if e != nil {
		return e
	}
	defer connection.Close()
	connection.Credential = identity.Credential
	connection.Scope = identity.Scope()
	connection.SessionAddresses = c.SessionAddresses
	connection.GatewayRebalanceSeconds = c.GatewayRebalanceSeconds
	certificate, found, e := state.LoadCertificate(c.StateDirectory)
	if e != nil {
		return e
	}
	if found && certificate.Certificate != "" {
		if e = connection.UseCertificate(certificate.Certificate, certificate.PrivateKey); e != nil {
			return e
		}
	}
	rotation, stopRotation := context.WithCancel(ctx)
	defer stopRotation()
	go certificateAgent{directory: c.StateDirectory, node: identity.NodeID, connection: connection, now: time.Now}.run(rotation)
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
	var gm *inference.GatewayManager
	if profile(c) == wire.DockerInferenceProfile {
		gm, e = inference.NewGatewayManager(c.Gateway, c.CAFile, c.Inference.DockerSocket, c.Policy())
		if e != nil {
			return e
		}
		defer gm.Close()
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
		e = tm.CheckInstallation(bounded)
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
		e = pm.CheckInstallation(bounded)
		stop()
		if e != nil {
			return e
		}
	}
	var km *kubernetes.Manager
	if profile(c) == wire.KubernetesNodeProfile {
		if km, e = kubernetes.New(c.Kubernetes, c.StateDirectory); e != nil {
			return e
		}
	}
	session := connection.Session(identity.NodeID)
	defer session.Close()
	pace := newPacer()
	transport := ""
	for {
		started := time.Now()
		e = sessionCycle(ctx, c, connection, session, identity, executors{components: m, inference: im, gateway: gm, training: tm, preparations: pm, registry: rm, kubernetes: km})
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
			if errors.As(e, &p) && p.Status == 401 && !client.CertificateRefused(e) {
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
	limited, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	bounded, revoke := context.WithCancelCause(limited)
	defer revoke(nil)
	taskBase := base + "/tasks/" + lease.TaskID
	renew := leaseRenewal(connection, taskBase, lease)
	if e := renew(bounded); e != nil {
		return e
	}
	stopRenewal := make(chan struct{})
	done, renewed := renewWhileRunning(bounded, revoke, renew, lease.TaskID, stopRenewal)
	reporter := taskReporter(connection, taskBase, lease)
	reporting := progress.With(bounded, reporter)
	reporter.Start(reporting)
	result, executionError := execute(reporting, taskBase)
	// The lease proves the last report, so it goes before renewal stops.
	reporter.Finish(ctx, executionError != nil || result.Outcome != "success")
	// Keep renewal active until effects finish, then join it before submitting the fenced result.
	close(stopRenewal)
	<-done
	if executionError != nil {
		return executionError
	}
	select {
	case renewalError := <-renewed:
		return renewalError
	default:
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return deliverResult(ctx, timeout, func(attempt context.Context) error {
		var completed struct {
			State string `json:"state"`
		}
		return connection.Post(attempt, taskBase+"/results", result, &completed)
	})
}

// validRenewal checks control's answer to a renewal. A lease with no time left has reached
// its task deadline, and the next renewal would be refused.
func validRenewal(lease wire.RenewedLease) error {
	if lease.LeaseExpiresAt.IsZero() || lease.ValidForMilliseconds < 1 || lease.ValidForMilliseconds > 30000 {
		return errors.New("AGENT_LEASE_STALE")
	}
	return nil
}

func (c Config) Policy() settings.Policy { return settings.Resolve(c.Runtime) }
