package composeprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const (
	machineValuesInvalid      = "COMPOSE_MACHINE_VALUES_INVALID"
	machineEnvironmentInvalid = "COMPOSE_MACHINE_ENVIRONMENT_INVALID"
	machineBindEnvironment    = "PROJECT_ONE_COMPOSE_MACHINE_BIND_ADDRESS"
	machinePublishEnvironment = "PROJECT_ONE_COMPOSE_MACHINE_PUBLISHED_PORT"
	machineTargetEnvironment  = "PROJECT_ONE_COMPOSE_MACHINE_TARGET_PORT"
	policyBasenameEnvironment = "PROJECT_ONE_POLICY_ENVIRONMENT_BASENAME"
	machineCAEnvironment      = "PROJECT_ONE_COMPOSE_MACHINE_CA_BASENAME"
	machineDialEnvironment    = "PROJECT_ONE_COMPOSE_MACHINE_DIAL_ADDRESS"
	machineTimeoutEnvironment = "PROJECT_ONE_COMPOSE_MACHINE_PROBE_TIMEOUT_SECONDS"
	healthIntervalEnvironment = "PROJECT_ONE_COMPOSE_HEALTH_INTERVAL_SECONDS"
	healthTimeoutEnvironment  = "PROJECT_ONE_COMPOSE_HEALTH_TIMEOUT_SECONDS"
	healthStartEnvironment    = "PROJECT_ONE_COMPOSE_HEALTH_START_PERIOD_SECONDS"
	healthRetriesEnvironment  = "PROJECT_ONE_COMPOSE_HEALTH_RETRIES"
	controlConfigurationPath  = "/run/projectone/control.yaml"
	controlConfigurationRoot  = "/run/projectone/"
	controlProbeExecutable    = "/usr/local/bin/control"
)

var machineBasename = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// Binding is an explicit host publication and independent container target.
// It is public render intent, not a runtime listener or advertised origin.
type Binding struct {
	BindAddress   string `json:"bind_address"`
	PublishedPort int    `json:"published_port"`
	TargetPort    int    `json:"target_port"`
}

// MachineProfile selects two ordinary Compose publications and one native
// machine-ready check. It never supplies or reads application policy or TLS data.
type MachineProfile struct {
	FormatVersion             int     `json:"format_version"`
	BrowserBinding            Binding `json:"browser_binding"`
	MachineBinding            Binding `json:"machine_binding"`
	PolicyEnvironmentBasename string  `json:"policy_environment_basename"`
	ProbeCABasename           string  `json:"probe_ca_basename"`
	ProbeDialAddress          string  `json:"probe_dial_address"`
	ProbeTimeoutSeconds       int     `json:"probe_timeout_seconds"`
	HealthIntervalSeconds     int     `json:"health_interval_seconds"`
	HealthTimeoutSeconds      int     `json:"health_timeout_seconds"`
	HealthStartPeriodSeconds  int     `json:"health_start_period_seconds"`
	HealthRetries             int     `json:"health_retries"`
}

func (b Binding) validate() error {
	return (Parameters{FormatVersion: formatVersion, BindAddress: b.BindAddress, PublishedPort: b.PublishedPort, TargetPort: b.TargetPort}).validate()
}

func (p MachineProfile) validate() error {
	if p.FormatVersion != formatVersion || p.BrowserBinding.validate() != nil || p.MachineBinding.validate() != nil || p.BrowserBinding.PublishedPort == p.MachineBinding.PublishedPort || p.BrowserBinding.TargetPort == p.MachineBinding.TargetPort {
		return errors.New(machineValuesInvalid)
	}
	if !machineBasename.MatchString(p.PolicyEnvironmentBasename) || !machineBasename.MatchString(p.ProbeCABasename) || p.PolicyEnvironmentBasename == p.ProbeCABasename || p.PolicyEnvironmentBasename == "control.yaml" || p.ProbeCABasename == "control.yaml" {
		return errors.New(machineValuesInvalid)
	}
	dial, e := netip.ParseAddrPort(p.ProbeDialAddress)
	if e != nil || dial.String() != p.ProbeDialAddress || dial.Addr().Zone() != "" || dial.Addr().Is4In6() || !dial.Addr().IsLoopback() || int(dial.Port()) != p.MachineBinding.TargetPort {
		return errors.New(machineValuesInvalid)
	}
	if p.ProbeTimeoutSeconds < 1 || p.ProbeTimeoutSeconds > 60 || p.HealthTimeoutSeconds <= p.ProbeTimeoutSeconds || p.HealthTimeoutSeconds > 120 || p.HealthIntervalSeconds < p.HealthTimeoutSeconds || p.HealthIntervalSeconds > 3600 || p.HealthStartPeriodSeconds < 0 || p.HealthStartPeriodSeconds > 3600 || p.HealthRetries < 1 || p.HealthRetries > 100 {
		return errors.New(machineValuesInvalid)
	}
	return nil
}

func machineMembers(raw []byte, keys ...string) (map[string]json.RawMessage, bool) {
	var members map[string]json.RawMessage
	if wire.StrictJSON(raw, &members) != nil || len(members) != len(keys) {
		return nil, false
	}
	for _, key := range keys {
		if _, found := members[key]; !found {
			return nil, false
		}
	}
	return members, true
}

// LoadMachine first validates complete independent JSON, then applies only an
// explicitly supplied fourteen-key overlay. No ambient environment is consulted.
func LoadMachine(raw, overlay []byte) (MachineProfile, error) {
	bad := errors.New(machineValuesInvalid)
	if len(raw) == 0 || len(raw) > maximumConfigurationBytes || !utf8.Valid(raw) {
		return MachineProfile{}, bad
	}
	members, valid := machineMembers(raw, "format_version", "browser_binding", "machine_binding", "policy_environment_basename", "probe_ca_basename", "probe_dial_address", "probe_timeout_seconds", "health_interval_seconds", "health_timeout_seconds", "health_start_period_seconds", "health_retries")
	if !valid {
		return MachineProfile{}, bad
	}
	for _, key := range []string{"browser_binding", "machine_binding"} {
		if _, valid := machineMembers(members[key], "bind_address", "published_port", "target_port"); !valid {
			return MachineProfile{}, bad
		}
	}
	var p MachineProfile
	if wire.StrictJSON(raw, &p) != nil || p.validate() != nil {
		return MachineProfile{}, bad
	}
	bad = errors.New(machineEnvironmentInvalid)
	if len(overlay) > maximumEnvironmentBytes || !utf8.Valid(overlay) || bytes.IndexByte(overlay, 0) >= 0 {
		return MachineProfile{}, bad
	}
	seen := make(map[string]bool, 14)
	for _, rawLine := range bytes.Split(overlay, []byte{'\n'}) {
		line := strings.TrimSuffix(string(rawLine), "\r")
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || seen[key] || value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(line, "\r\x00") {
			return MachineProfile{}, bad
		}
		seen[key] = true
		if strings.HasPrefix(value, `"`) {
			var decoded string
			if wire.StrictJSON([]byte(value), &decoded) != nil || decoded == "" {
				return MachineProfile{}, bad
			}
			value = decoded
		}
		var number *int
		switch key {
		case bindAddressEnvironment:
			p.BrowserBinding.BindAddress = value
		case publishedPortEnvironment:
			number = &p.BrowserBinding.PublishedPort
		case targetPortEnvironment:
			number = &p.BrowserBinding.TargetPort
		case machineBindEnvironment:
			p.MachineBinding.BindAddress = value
		case machinePublishEnvironment:
			number = &p.MachineBinding.PublishedPort
		case machineTargetEnvironment:
			number = &p.MachineBinding.TargetPort
		case policyBasenameEnvironment:
			p.PolicyEnvironmentBasename = value
		case machineCAEnvironment:
			p.ProbeCABasename = value
		case machineDialEnvironment:
			p.ProbeDialAddress = value
		case machineTimeoutEnvironment:
			number = &p.ProbeTimeoutSeconds
		case healthIntervalEnvironment:
			number = &p.HealthIntervalSeconds
		case healthTimeoutEnvironment:
			number = &p.HealthTimeoutSeconds
		case healthStartEnvironment:
			number = &p.HealthStartPeriodSeconds
		case healthRetriesEnvironment:
			number = &p.HealthRetries
		default:
			return MachineProfile{}, bad
		}
		if number != nil {
			parsed, e := strconv.Atoi(value)
			if e != nil || parsed < 0 || strconv.Itoa(parsed) != value {
				return MachineProfile{}, bad
			}
			*number = parsed
		}
	}
	if p.validate() != nil {
		return MachineProfile{}, bad
	}
	return p, nil
}

// Values emits only the existing control service's two publications, matched
// serve/probe arguments and bounded native health scheduling. Runtime files are
// deliberately not read or rewritten by this public projection.
func (p MachineProfile) Values() ([]byte, error) {
	if e := p.validate(); e != nil {
		return nil, e
	}
	quote := func(value string) string { raw, _ := json.Marshal(value); return string(raw) }
	policy := controlConfigurationRoot + p.PolicyEnvironmentBasename
	command, _ := json.Marshal([]string{"serve", "--config", controlConfigurationPath, "--env-file", policy})
	probe, _ := json.Marshal([]string{"CMD", controlProbeExecutable, "health-probe", "--config", controlConfigurationPath, "--env-file", policy, "--audience", "machine", "--kind", "ready", "--dial-address", p.ProbeDialAddress, "--timeout-seconds", strconv.Itoa(p.ProbeTimeoutSeconds), "--ca-file", controlConfigurationRoot + p.ProbeCABasename})
	var output bytes.Buffer
	output.WriteString("services:\n  control:\n    ports: !override\n")
	for _, binding := range []Binding{p.BrowserBinding, p.MachineBinding} {
		fmt.Fprintf(&output, "      - target: %d\n        published: %s\n        host_ip: %s\n        protocol: \"tcp\"\n", binding.TargetPort, quote(strconv.Itoa(binding.PublishedPort)), quote(binding.BindAddress))
	}
	fmt.Fprintf(&output, "    command: %s\n    healthcheck:\n      test: %s\n      interval: %s\n      timeout: %s\n      start_period: %s\n      retries: %d\n", command, probe, quote(strconv.Itoa(p.HealthIntervalSeconds)+"s"), quote(strconv.Itoa(p.HealthTimeoutSeconds)+"s"), quote(strconv.Itoa(p.HealthStartPeriodSeconds)+"s"), p.HealthRetries)
	return output.Bytes(), nil
}
