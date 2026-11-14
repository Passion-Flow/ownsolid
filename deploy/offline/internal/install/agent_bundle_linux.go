package install

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	agent "github.com/Passion-Flow/ownsolid/api/agent/v1"
	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const (
	agentAccount     = "ownsolid-agent"
	agentService     = agentAccount + ".service"
	agentConfigName  = "agent.json"
	agentCAName      = "ca.pem"
	agentTokenName   = "join-token"
	agentIdentity    = "identity.json"
	agentDiskPath    = "/"
	agentCAFileLimit = 65536 // Agent protected file limit.
)

type agentSite struct {
	FormatVersion int    `json:"format_version"`
	ControlOrigin string `json:"control_origin"`
	CAFile        string `json:"ca_file"`
	JoinTokenFile string `json:"join_token_file,omitempty"`
	DiskPath      string `json:"disk_path,omitempty"`
}

type installedAgentConfig struct {
	ControlOrigin    string `json:"control_origin"`
	CAFile           string `json:"ca_file"`
	StateDirectory   string `json:"state_directory"`
	DiskPath         string `json:"disk_path"`
	ExecutionProfile string `json:"execution_profile"`
	JoinTokenFile    string `json:"join_token_file,omitempty"`
}

type agentHost struct {
	root    string
	runner  Runner
	account func(context.Context) (agentOwner, error)
}

type agentOwner struct{ uid, gid uint32 }

var rootAgentOwner = agentOwner{0, 0}

// InstallAgent verifies signed Agent media, enrolls as the service account and starts the host unit.
func InstallAgent(ctx context.Context, configPath, archive string, policy bundle.Verification, out io.Writer) error {
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("INSTALL_AGENT_HOST_REQUIRED: run as root on a Linux amd64 systemd host")
	}
	runner := ExecRunner{}
	host := agentHost{root: "/", runner: runner, account: func(ctx context.Context) (agentOwner, error) {
		return ensureAgentAccount(ctx, runner)
	}}
	return host.install(ctx, configPath, archive, policy, out)
}

func readAgentSite(configPath string) (agentSite, []byte, error) {
	var site agentSite
	raw, err := bundle.ReadProtectedConfiguration(configPath)
	if err != nil || wire.StrictJSON(raw, &site) != nil || site.FormatVersion != 1 ||
		!canonicalPath(site.CAFile) || (site.JoinTokenFile != "" && !canonicalPath(site.JoinTokenFile)) {
		return site, nil, errors.New("INSTALL_AGENT_CONFIG_INVALID: use protected format version 1 JSON and absolute input paths")
	}
	origin, err := originURL(site.ControlOrigin)
	if err != nil || origin.ForceQuery || origin.String() != site.ControlOrigin || strings.ContainsAny(site.ControlOrigin, "#") {
		return site, nil, errors.New("INSTALL_AGENT_ORIGIN_INVALID: set the private Control HTTPS origin without a path")
	}
	if site.DiskPath == "" {
		site.DiskPath = agentDiskPath
	}
	if disk, err := filepath.EvalSymlinks(site.DiskPath); err != nil || disk != site.DiskPath || !canonicalPath(disk) {
		return site, nil, errors.New("INSTALL_AGENT_DISK_INVALID: set an existing absolute disk measurement path")
	}
	ca, err := bundle.ReadProtectedConfiguration(site.CAFile)
	if err != nil || len(ca) > agentCAFileLimit || !x509.NewCertPool().AppendCertsFromPEM(ca) {
		return site, nil, errors.New("INSTALL_AGENT_CA_INVALID: use a protected PEM trust file for Control")
	}
	return site, ca, nil
}

func readAgentJoinToken(path string) ([]byte, error) {
	raw, err := bundle.ReadProtectedConfiguration(path)
	token := strings.TrimSuffix(string(raw), "\n")
	decoded, decodeErr := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || decodeErr != nil || len(token) != 43 || len(decoded) != 32 {
		return nil, errors.New("INSTALL_AGENT_TOKEN_INVALID: supply a protected file with a valid Control join token")
	}
	return raw, nil
}

func (host agentHost) install(ctx context.Context, configPath, archive string, policy bundle.Verification, out io.Writer) error {
	site, ca, err := readAgentSite(configPath)
	if err != nil {
		return err
	}
	prepared, result, cleanup, err := unpackRelease(ctx, archive, policy, bundle.UnpackAgent)
	if err != nil {
		return err
	}
	defer cleanup()
	var binary, unit []byte
	for _, asset := range result.Manifest.Assets {
		var dest *[]byte
		switch asset.ID {
		case bundle.AgentBinaryAsset:
			dest = &binary
		case bundle.AgentUnitAsset:
			dest = &unit
		default:
			continue
		}
		*dest, err = os.ReadFile(filepath.Join(prepared, filepath.FromSlash(asset.Path)))
		if err != nil || int64(len(*dest)) != asset.Size || wire.Digest(*dest) != asset.SHA256 {
			return errors.New("INSTALL_AGENT_ASSET_UNAVAILABLE: prepare the signed media again")
		}
	}
	binDir := filepath.Join(host.root, "opt", agentAccount)
	configDir := filepath.Join(host.root, "etc", agentAccount)
	stateDir := filepath.Join(host.root, "var", "lib", agentAccount)
	unitDir := filepath.Join(host.root, "etc", "systemd", "system")
	for _, dir := range []string{filepath.Dir(binDir), filepath.Dir(configDir), filepath.Dir(stateDir), unitDir} {
		if err := trustedAgentDirectory(dir); err != nil {
			return err
		}
	}
	if err := ensureAgentDirectory(binDir, 0o755, rootAgentOwner); err != nil {
		return err
	}
	lock, err := lockAgentInstall(binDir)
	if err != nil {
		return err
	}
	defer lock.Close()

	owner, err := host.account(ctx)
	if err != nil {
		return err
	}
	if err := ensureAgentDirectory(configDir, 0o710, agentOwner{0, owner.gid}); err != nil {
		return err
	}
	if err := ensureAgentDirectory(stateDir, 0o700, owner); err != nil {
		return err
	}
	configPath = filepath.Join(configDir, agentConfigName)
	binaryPath := filepath.Join(binDir, "agent")
	tokenPath := filepath.Join(configDir, agentTokenName)
	configuration := installedAgentConfig{
		ControlOrigin: site.ControlOrigin, CAFile: filepath.Join(configDir, agentCAName),
		StateDirectory: stateDir, DiskPath: site.DiskPath, ExecutionProfile: agent.ReadOnlyProfile,
	}
	configurationBytes, err := json.Marshal(configuration)
	if err != nil {
		return err
	}
	for _, file := range []struct {
		path  string
		data  []byte
		mode  os.FileMode
		owner agentOwner
	}{
		{binaryPath, binary, 0o755, rootAgentOwner},
		{filepath.Join(unitDir, agentService), unit, 0o644, rootAgentOwner},
		{configuration.CAFile, ca, 0o600, owner},
		{configPath, configurationBytes, 0o600, owner},
	} {
		if err := publishAgentFile(file.path, file.data, file.mode, file.owner); err != nil {
			return err
		}
	}
	if err := enrollInstalledAgent(ctx, site.JoinTokenFile, binaryPath, configPath, tokenPath, configuration, owner); err != nil {
		return err
	}
	if _, err := host.runner.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return errors.New("INSTALL_AGENT_SYSTEMD_RELOAD_FAILED: check the systemd manager and retry with the same inputs")
	}
	if _, err := host.runner.Run(ctx, "systemctl", "enable", "--now", agentService); err != nil {
		return errors.New("INSTALL_AGENT_SERVICE_START_FAILED: check journalctl -u ownsolid-agent.service and retry")
	}
	_, err = fmt.Fprintf(out, "Agent enrollment verified and service started (release %s).\n", result.ReleaseID)
	return err
}

func enrollInstalledAgent(ctx context.Context, source, binary, configPath, tokenPath string, configuration installedAgentConfig, owner agentOwner) error {
	identityPath := filepath.Join(configuration.StateDirectory, agentIdentity)
	identityPresent, err := agentFilePresent(identityPath)
	if err != nil {
		return err
	}
	tokenPresent, err := agentFilePresent(tokenPath)
	if err != nil {
		return err
	}
	if !identityPresent {
		if tokenPresent {
			return errors.New("INSTALL_AGENT_ENROLLMENT_UNRESOLVED: reconcile the prior attempt with Control before retrying")
		}
		token, err := readAgentJoinToken(source)
		if err != nil {
			return err
		}
		configuration.JoinTokenFile = tokenPath
		raw, err := json.Marshal(configuration)
		if err != nil {
			return err
		}
		file, err := stageAgentFile(filepath.Dir(configPath), raw, 0o600, owner)
		if err != nil {
			return err
		}
		defer os.Remove(file)
		configPath = file
		// Publish the private token before making any request. Without an identity,
		// its presence fences replays after a process crash or an unknown response.
		if err := publishAgentToken(tokenPath, token, owner); err != nil {
			return err
		}
		tokenPresent = true
	}
	if err := runAgentEnrollment(ctx, binary, configPath, owner); err != nil {
		if !identityPresent && errors.Is(err, errAgentTokenUnusable) {
			return errors.Join(err, removeAgentToken(tokenPath))
		}
		if identityPresent {
			return errors.New("INSTALL_AGENT_IDENTITY_INVALID: inspect the existing Agent state before retrying")
		}
		var uncertain agentEnrollmentUncertain
		if errors.As(err, &uncertain) {
			return fmt.Errorf("INSTALL_AGENT_ENROLLMENT_UNRESOLVED: reconcile with Control before retrying (%s)", uncertain)
		}
		return errors.New("INSTALL_AGENT_ENROLLMENT_UNRESOLVED: reconcile the enrollment outcome with Control before retrying")
	}
	info, err := os.Lstat(identityPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !owner.owns(info) || info.Size() == 0 {
		return errors.New("INSTALL_AGENT_IDENTITY_INVALID: enrollment did not leave a private identity")
	}
	if err := syncDirectory(configuration.StateDirectory); err != nil {
		return errors.New("INSTALL_AGENT_IDENTITY_SYNC_FAILED: check Agent state storage and retry")
	}
	if tokenPresent {
		return removeAgentToken(tokenPath)
	}
	return nil
}
