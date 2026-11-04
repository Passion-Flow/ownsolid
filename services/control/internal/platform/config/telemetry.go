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
	telemetryMaximumPushSeconds    = 3600
)

// TelemetryConfiguration points the control plane at the stores of ADR 0009. Without it,
// component logs are unavailable, process metrics are not pushed and the rest of the
// platform runs normally.
type TelemetryConfiguration struct {
	LogsAddress                string `yaml:"logs_address"`
	MetricsAddress             string `yaml:"metrics_address"`
	RequestTimeoutSeconds      int    `yaml:"request_timeout_seconds"`
	QueueCapacity              int    `yaml:"queue_capacity"`
	MetricsPushIntervalSeconds int    `yaml:"metrics_push_interval_seconds"`
}

// Settings converts the configuration for telemetrystore.Open.
func (c TelemetryConfiguration) Settings() telemetrystore.Settings {
	return telemetrystore.Settings{
		LogsAddress: c.LogsAddress, MetricsAddress: c.MetricsAddress,
		RequestTimeout: time.Duration(c.RequestTimeoutSeconds) * time.Second, QueueCapacity: c.QueueCapacity,
		MetricsPushInterval: time.Duration(c.MetricsPushIntervalSeconds) * time.Second,
	}
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
	if c.MetricsPushIntervalSeconds < 1 || c.MetricsPushIntervalSeconds > telemetryMaximumPushSeconds {
		return errors.New("telemetry.metrics_push_interval_seconds must be 1 to 3600")
	}
	if _, e := telemetrystore.NewClient(c.LogsAddress, time.Second); e != nil {
		return errors.New("telemetry.logs_address must be an http URL without a path")
	}
	if _, e := telemetrystore.NewClient(c.MetricsAddress, time.Second); e != nil {
		return errors.New("telemetry.metrics_address must be an http URL without a path")
	}
	return nil
}
