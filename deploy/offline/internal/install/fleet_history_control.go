package install

import (
	"encoding/json"
	"net/netip"
	"net/url"
	"path/filepath"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

func (c Config) controlCAPath() string {
	if c.Telemetry.FleetHistoryControlDirectory != "" {
		return containerConfigDir + "/fleet-history/ca.pem"
	}
	return "/etc/ssl/certs/ca-certificates.crt"
}

// The installer owns only file admission and the private transport boundary.
// Control's existing monitoring loader owns complete module-policy validation.
func readHistoryControl(c Config) (map[string][]byte, error) {
	source := c.Telemetry.FleetHistoryControlDirectory
	if source == "" {
		return nil, nil
	}
	if !historyPath(source) {
		return nil, historyFailure("CONTROL_INVALID", "select a canonical protected input directory")
	}
	files := map[string][]byte{}
	for _, name := range []string{"monitoring.json", "ca.pem"} {
		raw, err := bundle.ReadProtectedConfiguration(filepath.Join(source, name))
		if err != nil {
			return nil, historyFailure("CONTROL_INVALID", "select the protected Control output from fleet-history apply")
		}
		files[name] = raw
	}
	if _, err := historyRoots(files["ca.pem"]); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	var format int
	var policy historyPolicy
	if wire.StrictJSON(files["monitoring.json"], &fields) != nil ||
		wire.StrictJSON(fields["format_version"], &format) != nil || format != 1 ||
		wire.StrictJSON(fields["fleet_history"], &policy) != nil {
		return nil, historyFailure("CONTROL_INVALID", "use a valid monitoring policy with fleet_history")
	}
	for _, origin := range []string{policy.RawOrigin, policy.LongOrigin} {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			return nil, historyFailure("CONTROL_INVALID", "fleet stores must use fixed private HTTPS origins")
		}
		address, err := netip.ParseAddr(u.Hostname())
		if err != nil || !address.Is4() || !address.IsPrivate() {
			return nil, historyFailure("CONTROL_INVALID", "containerized Control requires private numeric store addresses reachable from its network")
		}
	}
	if policy.RawOrigin == policy.LongOrigin {
		return nil, historyFailure("CONTROL_INVALID", "raw and long origins must differ")
	}
	return files, nil
}
