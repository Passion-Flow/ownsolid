// Package kubernetes installs RKE2 on a node of the Kubernetes node profile and joins it
// to its cell's cluster as the server or a worker (ARC-104, ADR 0051).
package kubernetes

import (
	"errors"
	"time"
)

// Defaults for Config fields left at zero.
const (
	// DefaultReadySeconds bounds the wait for a node's own Node to become Ready. The
	// prototype's server was ready in 30 seconds and its worker in 70; RKE2 retries a
	// failed join forever, so the agent must stop waiting on its own.
	DefaultReadySeconds = 600
	// DefaultMinimumFreeBytes is the free space RKE2's data directory needs before an
	// install: the staged and placed archives (about 1.7 GB) and the unpacked images
	// (about 3.5 GB), with room for pods.
	DefaultMinimumFreeBytes int64 = 16 << 30
)

// Config is the agent's Kubernetes section.
type Config struct {
	// ReadySeconds bounds how long a join waits for the node to become Ready.
	ReadySeconds int `json:"ready_seconds,omitempty"`
	// MinimumFreeBytes is the free space the RKE2 data directory needs before an install.
	MinimumFreeBytes int64 `json:"minimum_free_bytes,omitempty"`
}

// WithDefaults fills the zero fields.
func (c Config) WithDefaults() Config {
	if c.ReadySeconds == 0 {
		c.ReadySeconds = DefaultReadySeconds
	}
	if c.MinimumFreeBytes == 0 {
		c.MinimumFreeBytes = DefaultMinimumFreeBytes
	}
	return c
}

// Validate refuses values that would make a join wait too briefly or forever, or install
// onto a disk that cannot hold the images.
func (c Config) Validate() error {
	if c.ReadySeconds < 60 || c.ReadySeconds > 3600 || c.MinimumFreeBytes < 2<<30 {
		return errors.New("AGENT_CONFIG_INVALID")
	}
	return nil
}

func (c Config) readyTimeout() time.Duration { return time.Duration(c.ReadySeconds) * time.Second }
