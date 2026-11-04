package config

import (
	"errors"
	"time"

	"github.com/Passion-Flow/ownsolid/services/control/internal/platform/telemetrystore"
)

// Accepted ranges for the telemetry settings.
const (
	telemetryMaximumTimeoutSeconds = 60
	telemetryMaximumQueueCapacity  = 65536
)

// TelemetryConfiguration points the control plane at the log store of ADR 0009. Without
// it, component logs are unavailable and the rest of the platform runs normally.
type TelemetryConfiguration struct {
	LogsAddress           string `yaml:"logs_address"`
	RequestTimeoutSeconds int    `yaml:"request_timeout_seconds"`
	QueueCapacity         int    `yaml:"queue_capacity"`
}

// Settings converts the configuration for telemetrystore.Open.
func (c TelemetryConfiguration) Settings() telemetrystore.Settings {
	return telemetrystore.Settings{LogsAddress: c.LogsAddress, RequestTimeout: time.Duration(c.RequestTimeoutSeconds) * time.Second, QueueCapacity: c.QueueCapacity}
}

// Validate accepts an absent section and refuses an incomplete one.
func (c *TelemetryConfiguration) Validate() error {
	if c == nil {
		return nil
	}
	if c.RequestTimeoutSeconds < 1 || c.RequestTimeoutSeconds > telemetryMaximumTimeoutSeconds {
		return errors.New("telemetry.request_timeout_seconds must be 1 to 60")
	}
	if c.QueueCapacity < 1 || c.QueueCapacity > telemetryMaximumQueueCapacity {
		return errors.New("telemetry.queue_capacity must be 1 to 65536")
	}
	if _, e := telemetrystore.NewClient(c.LogsAddress, time.Second); e != nil {
		return errors.New("telemetry.logs_address must be an http URL without a path")
	}
	return nil
}
