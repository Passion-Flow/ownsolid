// Package config reads and validates the simulator's configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
)

// Bounds of Config.
const (
	MaximumAgents            = 100000
	MaximumAgentsPerConn     = 1000
	MaximumHTTPSConnections  = 4096
	MaximumEnrollConcurrency = 1024
	MaximumRequestSeconds    = 120
	MaximumDurationSeconds   = 7 * 24 * 3600
	// MaximumLongTaskSeconds keeps a long task inside the shortest task deadline control
	// can be configured with.
	MaximumLongTaskSeconds = 110
	maximumConfigBytes     = 1 << 20
)

// Defaults of the optional fields.
const (
	DefaultAgentsPerConnection = 1
	DefaultHTTPSConnections    = 64
	DefaultEnrollConcurrency   = 16
	DefaultRequestSeconds      = 10
	DefaultStartSeconds        = 10
	DefaultLogSeconds          = 10
	DefaultWindowSeconds       = 60
	DefaultLongTaskSeconds     = 90
)

// The kinds of Fault.
const (
	// FaultDisconnect closes the chosen agents' connections; they reconnect on their backoff.
	FaultDisconnect = "disconnect"
	// FaultRestart closes their connections and starts a new cycle at once, like a process restart.
	FaultRestart = "restart"
	// FaultPartition makes control unreachable for them for the duration.
	FaultPartition = "partition"
	// FaultHTTPSOnly keeps the session stream from opening for the duration.
	FaultHTTPSOnly = "https_only"
	// FaultLoseReplies loses control's first reply to each acknowledgement and result for the duration.
	FaultLoseReplies = "lose_replies"
	// FaultLoseCursor makes them forget their task cursor.
	FaultLoseCursor = "lose_cursor"
	// FaultLongTasks makes the tasks they start for the duration run for LongTaskSeconds,
	// renewing their leases, so that work is in flight when something else happens.
	FaultLongTasks = "long_tasks"
)

var lasting = map[string]bool{FaultPartition: true, FaultHTTPSOnly: true, FaultLoseReplies: true, FaultLongTasks: true}
var instant = map[string]bool{FaultDisconnect: true, FaultRestart: true, FaultLoseCursor: true}

// Fault is one scripted fault: at AtSeconds into the run, Percent of the agents, chosen
// at random, suffer Kind, for DurationSeconds when the kind lasts.
type Fault struct {
	Kind            string `json:"kind"`
	AtSeconds       int    `json:"at_seconds"`
	Percent         int    `json:"percent"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
}

// Lasts reports whether the fault has a duration.
func (f Fault) Lasts() bool { return lasting[f.Kind] }

// Config is the simulator's configuration. Paths are absolute.
type Config struct {
	// ControlOrigin is the origin agents use, exactly as on a real node.
	ControlOrigin string `json:"control_origin"`
	// CAFile verifies control's certificate in addition to the system roots.
	CAFile string `json:"ca_file,omitempty"`
	// DialAddress, when set, is the host:port connections go to instead of the origin's
	// host; TLS still verifies the origin's host name.
	DialAddress string `json:"dial_address,omitempty"`
	// SessionAddresses are agent gateways, host:port, for the session streams, as on a real
	// node (ADR 0048). Each agent tries them in its own order; HTTPS stays on DialAddress.
	SessionAddresses []string `json:"session_addresses,omitempty"`
	// GatewayRebalanceSeconds is the minimum wait between preference checks.
	GatewayRebalanceSeconds int `json:"gateway_rebalance_seconds,omitempty"`
	// IdentitiesFile holds the enrolled agents. enroll writes it; run reads it and writes
	// the task cursors back.
	IdentitiesFile string `json:"identities_file"`
	// JoinTokensFile holds one join token per line. Only enroll reads it.
	JoinTokensFile string `json:"join_tokens_file,omitempty"`
	// Agents is how many agents enroll creates and run starts.
	Agents int `json:"agents"`
	// AgentsPerConnection shares one session connection between this many agents. One,
	// the default, gives every agent its own, as on real nodes.
	AgentsPerConnection int `json:"agents_per_connection,omitempty"`
	// HTTPSConnections bounds the pooled connections of task traffic and enrollment.
	HTTPSConnections int `json:"https_connections,omitempty"`
	// EnrollConcurrency bounds the enrollments in flight.
	EnrollConcurrency int `json:"enroll_concurrency,omitempty"`
	// RequestSeconds bounds one request, plus the claim wait for a claim.
	RequestSeconds int `json:"request_seconds,omitempty"`
	// DurationSeconds is how long run runs.
	DurationSeconds int `json:"duration_seconds,omitempty"`
	// StartSeconds spreads the agents' first cycles evenly over this window. Zero starts
	// every agent at once.
	StartSeconds *int `json:"start_seconds,omitempty"`
	// LogSeconds is how often run logs a summary.
	LogSeconds int `json:"log_seconds,omitempty"`
	// WindowSeconds is the length of the windows the report splits a run into, so a steady
	// stretch can be judged apart from the storms around it.
	WindowSeconds int `json:"window_seconds,omitempty"`
	// LongTaskSeconds is how long a task runs under a long_tasks fault.
	LongTaskSeconds int `json:"long_task_seconds,omitempty"`
	// ReportFile receives run's JSON report.
	ReportFile string `json:"report_file,omitempty"`
	// Faults are scripted for run.
	Faults []Fault `json:"faults,omitempty"`
}

// Load reads, defaults and validates the configuration at path.
func Load(path string) (Config, error) {
	var c Config
	f, e := os.Open(path)
	if e != nil {
		return c, errors.New("SIMULATOR_CONFIG_UNREADABLE")
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, maximumConfigBytes+1))
	if e != nil || len(raw) > maximumConfigBytes {
		return c, errors.New("SIMULATOR_CONFIG_UNREADABLE")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || decoder.Decode(new(any)) != io.EOF {
		return c, errors.New("SIMULATOR_CONFIG_INVALID")
	}
	c.defaults()
	return c, c.Validate()
}

// GatewayRebalanceIntervalSeconds returns the explicit interval or the agent default.
func (c Config) GatewayRebalanceIntervalSeconds() int {
	if c.GatewayRebalanceSeconds == 0 {
		return wire.DefaultGatewayRebalanceSeconds
	}
	return c.GatewayRebalanceSeconds
}

func (c *Config) defaults() {
	if c.AgentsPerConnection == 0 {
		c.AgentsPerConnection = DefaultAgentsPerConnection
	}
	if c.HTTPSConnections == 0 {
		c.HTTPSConnections = DefaultHTTPSConnections
	}
	if c.EnrollConcurrency == 0 {
		c.EnrollConcurrency = DefaultEnrollConcurrency
	}
	if c.RequestSeconds == 0 {
		c.RequestSeconds = DefaultRequestSeconds
	}
	if c.StartSeconds == nil {
		start := DefaultStartSeconds
		c.StartSeconds = &start
	}
	if c.LogSeconds == 0 {
		c.LogSeconds = DefaultLogSeconds
	}
	if c.WindowSeconds == 0 {
		c.WindowSeconds = DefaultWindowSeconds
	}
	if c.LongTaskSeconds == 0 {
		c.LongTaskSeconds = DefaultLongTaskSeconds
	}
}

// Validate checks every field that both commands use; ValidateEnroll and ValidateRun add
// what one command needs.
func (c Config) Validate() error {
	invalid := errors.New("SIMULATOR_CONFIG_INVALID")
	origin, e := url.Parse(c.ControlOrigin)
	if e != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return invalid
	}
	if c.DialAddress != "" {
		if _, _, e := net.SplitHostPort(c.DialAddress); e != nil {
			return invalid
		}
	}
	interval := c.GatewayRebalanceIntervalSeconds()
	if wire.ValidateSessionAddresses(c.SessionAddresses) != nil || interval < wire.MinGatewayRebalanceSeconds || interval > wire.MaxGatewayRebalanceSeconds {
		return invalid
	}
	for _, path := range []string{c.CAFile, c.JoinTokensFile, c.ReportFile} {
		if path != "" && !filepath.IsAbs(path) {
			return invalid
		}
	}
	if !filepath.IsAbs(c.IdentitiesFile) || c.Agents < 1 || c.Agents > MaximumAgents ||
		c.AgentsPerConnection < 1 || c.AgentsPerConnection > MaximumAgentsPerConn ||
		c.HTTPSConnections < 1 || c.HTTPSConnections > MaximumHTTPSConnections ||
		c.EnrollConcurrency < 1 || c.EnrollConcurrency > MaximumEnrollConcurrency ||
		c.RequestSeconds < 1 || c.RequestSeconds > MaximumRequestSeconds ||
		c.DurationSeconds < 0 || c.DurationSeconds > MaximumDurationSeconds ||
		c.StartSeconds == nil || *c.StartSeconds < 0 || *c.StartSeconds > MaximumDurationSeconds ||
		c.LogSeconds < 1 || c.LogSeconds > 3600 || c.WindowSeconds < 1 || c.WindowSeconds > 3600 ||
		c.LongTaskSeconds < 1 || c.LongTaskSeconds > MaximumLongTaskSeconds {
		return invalid
	}
	for _, f := range c.Faults {
		if !lasting[f.Kind] && !instant[f.Kind] || f.Percent < 1 || f.Percent > 100 || f.AtSeconds < 0 {
			return invalid
		}
		if f.Lasts() != (f.DurationSeconds > 0) || f.DurationSeconds < 0 {
			return invalid
		}
	}
	return nil
}

// ValidateEnroll checks what enroll needs.
func (c Config) ValidateEnroll() error {
	if c.JoinTokensFile == "" {
		return errors.New("SIMULATOR_CONFIG_INVALID")
	}
	return nil
}

// ValidateRun checks what run needs: a duration, a report and faults that end within it.
func (c Config) ValidateRun() error {
	invalid := errors.New("SIMULATOR_CONFIG_INVALID")
	if c.DurationSeconds < 1 || c.ReportFile == "" {
		return invalid
	}
	for _, f := range c.Faults {
		if f.AtSeconds+f.DurationSeconds >= c.DurationSeconds {
			return invalid
		}
	}
	return nil
}
