// Package settings owns restart-required agent runtime options.
package settings

import (
	"errors"
	"time"
)

type Policy struct {
	EnrollSeconds         int `json:"enroll_seconds"`
	RemoveSeconds         int `json:"remove_seconds"`
	ControlRequestSeconds int `json:"control_request_seconds"`
	ControlHeaderSeconds  int `json:"control_header_seconds"`
	TLSHandshakeSeconds   int `json:"tls_handshake_seconds"`
	IdleConnectionSeconds int `json:"idle_connection_seconds"`
	ControlConnections    int `json:"control_connections"`
	BackoffInitialSeconds int `json:"backoff_initial_seconds"`
	BackoffMaxSeconds     int `json:"backoff_max_seconds"`
	ComponentTaskSeconds  int `json:"component_task_seconds"`
	InferenceTaskSeconds  int `json:"inference_task_seconds"`
	SystemdSeconds        int `json:"systemd_seconds"`
	MaintenanceSeconds    int `json:"maintenance_seconds"`
	MetricsSeconds        int `json:"metrics_seconds"`
	MetricsHeaderSeconds  int `json:"metrics_header_seconds"`
	DockerStopSeconds     int `json:"docker_stop_seconds"`
	DockerSeconds         int `json:"docker_seconds"`
	DockerHeaderSeconds   int `json:"docker_header_seconds"`
	DockerDialSeconds     int `json:"docker_dial_seconds"`
	DockerConnections     int `json:"docker_connections"`
	ProbeSeconds          int `json:"probe_seconds"`
	ProbeHeaderSeconds    int `json:"probe_header_seconds"`
	ProbeDialSeconds      int `json:"probe_dial_seconds"`
	GPUProbeSeconds       int `json:"gpu_probe_seconds"`
	ModelLoadSeconds      int `json:"model_load_seconds"`
	ModelPollMilliseconds int `json:"model_poll_milliseconds"`
}

func Default() Policy {
	return Policy{EnrollSeconds: 120, RemoveSeconds: 120, ControlRequestSeconds: 15, ControlHeaderSeconds: 10, TLSHandshakeSeconds: 5, IdleConnectionSeconds: 30, ControlConnections: 2, BackoffInitialSeconds: 1, BackoffMaxSeconds: 30, ComponentTaskSeconds: 120, InferenceTaskSeconds: 1740, SystemdSeconds: 25, MaintenanceSeconds: 10, MetricsSeconds: 3, MetricsHeaderSeconds: 2, DockerStopSeconds: 15, DockerSeconds: 30, DockerHeaderSeconds: 25, DockerDialSeconds: 5, DockerConnections: 2, ProbeSeconds: 30, ProbeHeaderSeconds: 5, ProbeDialSeconds: 2, GPUProbeSeconds: 10, ModelLoadSeconds: 600, ModelPollMilliseconds: 1000}
}
func Resolve(p *Policy) Policy {
	if p == nil {
		return Default()
	}
	return *p
}
func Seconds(n int) time.Duration { return time.Duration(n) * time.Second }
func (p Policy) Validate() error {
	if p.EnrollSeconds < 1 || p.EnrollSeconds > 600 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.RemoveSeconds < 30 || p.RemoveSeconds > 300 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ControlRequestSeconds < 1 || p.ControlRequestSeconds > 25 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ControlHeaderSeconds < 1 || p.ControlHeaderSeconds > 20 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.TLSHandshakeSeconds < 1 || p.TLSHandshakeSeconds > 15 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.IdleConnectionSeconds < 1 || p.IdleConnectionSeconds > 300 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ControlConnections < 1 || p.ControlConnections > 16 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.BackoffInitialSeconds < 1 || p.BackoffInitialSeconds > 10 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.BackoffMaxSeconds < 1 || p.BackoffMaxSeconds > 300 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ComponentTaskSeconds < 30 || p.ComponentTaskSeconds > 240 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.InferenceTaskSeconds < 300 || p.InferenceTaskSeconds > 1740 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.SystemdSeconds < 1 || p.SystemdSeconds > 60 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.MaintenanceSeconds < 1 || p.MaintenanceSeconds > 30 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.MetricsSeconds < 1 || p.MetricsSeconds > 15 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.MetricsHeaderSeconds < 1 || p.MetricsHeaderSeconds > 10 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.DockerStopSeconds < 1 || p.DockerStopSeconds > 60 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.DockerSeconds < 1 || p.DockerSeconds > 120 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.DockerHeaderSeconds < 1 || p.DockerHeaderSeconds > 90 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.DockerDialSeconds < 1 || p.DockerDialSeconds > 30 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.DockerConnections < 1 || p.DockerConnections > 16 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ProbeSeconds < 1 || p.ProbeSeconds > 120 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ProbeHeaderSeconds < 1 || p.ProbeHeaderSeconds > 30 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ProbeDialSeconds < 1 || p.ProbeDialSeconds > 10 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.GPUProbeSeconds < 1 || p.GPUProbeSeconds > 30 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ModelLoadSeconds < 30 || p.ModelLoadSeconds > 1200 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.ModelPollMilliseconds < 100 || p.ModelPollMilliseconds > 10000 {
		return errors.New("AGENT_RUNTIME_BOUNDS_INVALID")
	}
	if p.DockerStopSeconds >= p.DockerHeaderSeconds || p.DockerStopSeconds >= p.DockerSeconds {
		return errors.New("AGENT_RUNTIME_RELATION_INVALID")
	}
	if p.ControlHeaderSeconds > p.ControlRequestSeconds || p.TLSHandshakeSeconds > p.ControlRequestSeconds || p.BackoffInitialSeconds > p.BackoffMaxSeconds || p.MetricsHeaderSeconds > p.MetricsSeconds || p.DockerHeaderSeconds > p.DockerSeconds || p.DockerDialSeconds > p.DockerSeconds || p.ProbeHeaderSeconds > p.ProbeSeconds || p.ProbeDialSeconds > p.ProbeSeconds || p.ModelLoadSeconds >= p.InferenceTaskSeconds {
		return errors.New("AGENT_RUNTIME_RELATION_INVALID")
	}
	return nil
}
