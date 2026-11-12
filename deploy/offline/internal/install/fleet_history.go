package install

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"go.yaml.in/yaml/v3"
)

const historyOwnerFile = "fleet-history-owner.json"
const historyRootLabel = "io.ownsolid.fleet-history.root"

type historyOwner struct {
	Project, Subnet, RawListen, LongListen, EvaluatorListen string
	UID, GID                                                int
}

func (s historySite) owner() historyOwner {
	c := s.config
	return historyOwner{c.Project, c.Subnet, c.Raw.Listen, c.Long.Listen, c.EvaluatorListen, c.UID, c.GID}
}

func (s historySite) installerConfig() Config {
	return Config{InstallRoot: s.config.Root, Runtime: RuntimeConfig{UID: s.config.UID, GID: s.config.GID}}
}

func (s historySite) ownRoot() error {
	c := s.installerConfig()
	if st, err := os.Lstat(c.InstallRoot); err == nil && (!st.IsDir() || st.Mode().Perm()&0077 != 0) {
		return historyFailure("ROOT_INVALID", "use a private dedicated deployment directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return historyFailure("ROOT_INVALID", "cannot read the dedicated deployment directory")
	}
	marker := filepath.Join(c.InstallRoot, historyOwnerFile)
	raw, err := bundle.ReadProtectedConfiguration(marker)
	if err == nil {
		st, statErr := os.Lstat(marker)
		var owner historyOwner
		if statErr != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 ||
			wire.StrictJSON(raw, &owner) != nil || owner != s.owner() {
			return historyFailure("OWNER_MISMATCH", "keep the original project, subnet, listeners and service identity for this directory")
		}
		return nil
	}
	if _, statErr := os.Lstat(marker); !errors.Is(statErr, os.ErrNotExist) {
		return historyFailure("ROOT_INVALID", "cannot verify the deployment owner")
	}
	entries, err := os.ReadDir(c.InstallRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return historyFailure("ROOT_INVALID", "cannot inspect the deployment directory")
	}
	for _, entry := range entries {
		if entry.Name() != operationLockName || !entry.Type().IsRegular() {
			return historyFailure("ROOT_INVALID", "the initial deployment directory must be empty")
		}
	}
	return nil
}

func (s historySite) prepare() (string, error) {
	c := s.installerConfig()
	if err := privateDirectory(c.InstallRoot, c, false); err != nil {
		return "", err
	}
	if err := writeFile(filepath.Join(c.InstallRoot, historyOwnerFile), historyJSON(s.owner()), c, false); err != nil {
		return "", err
	}
	if err := syncDirectory(c.InstallRoot); err != nil {
		return "", err
	}
	for _, name := range []string{"inputs", "operator", "data", "data/raw", "data/long"} {
		if err := privateDirectory(filepath.Join(c.InstallRoot, name), c, name != "inputs" && name != "operator"); err != nil {
			return "", err
		}
	}
	generation := filepath.Join(c.InstallRoot, "inputs", s.generation)
	if info, err := os.Lstat(generation); err == nil {
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return "", historyFailure("GENERATION_INVALID", "use the original private input generation")
		}
		root, err := os.OpenRoot(generation)
		if err != nil {
			return "", err
		}
		defer root.Close()
		for name, expected := range s.files {
			st, statErr := root.Lstat(name)
			if statErr != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() != int64(len(expected)) {
				return "", historyFailure("GENERATION_INVALID", "the selected immutable input generation has changed")
			}
			file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return "", historyFailure("GENERATION_INVALID", "cannot read the selected input generation")
			}
			raw, readErr := io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
			file.Close()
			if readErr != nil || !bytes.Equal(raw, expected) {
				return "", historyFailure("GENERATION_INVALID", "the selected immutable input generation has changed")
			}
		}
		return generation, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", historyFailure("GENERATION_INVALID", "cannot inspect the input generation")
	}
	staging, err := os.MkdirTemp(filepath.Join(c.InstallRoot, "inputs"), ".stage-")
	if err != nil {
		return "", historyFailure("WRITE_FAILED", "cannot stage the private configuration")
	}
	defer os.RemoveAll(staging)
	for _, name := range []string{"raw", "long", "evaluator", "control"} {
		if err := privateDirectory(filepath.Join(staging, name), c, name != "control"); err != nil {
			return "", err
		}
	}
	for name, raw := range s.files {
		if err := writeFile(filepath.Join(staging, name), raw, c, !strings.HasPrefix(name, "control/")); err != nil {
			return "", err
		}
	}
	if err := own(staging, c, true); err != nil {
		return "", err
	}
	if err := os.Rename(staging, generation); err != nil {
		return "", historyFailure("WRITE_FAILED", "cannot publish the immutable configuration")
	}
	parent, err := os.Open(filepath.Dir(generation))
	if err != nil {
		return "", err
	}
	defer parent.Close()
	if err := parent.Sync(); err != nil {
		return "", err
	}
	return generation, nil
}

func historyMount(source, target string, readOnly bool) map[string]any {
	return map[string]any{"type": "bind", "source": source, "target": target, "read_only": readOnly,
		"bind": map[string]any{"create_host_path": false}}
}

func (s historySite) compose(generation string) ([]byte, error) {
	c := s.config
	addresses := NetworkConfig{StateSubnet: c.Subnet}.stateAddresses()
	service := func(image string, limits historyResources) map[string]any {
		return map[string]any{
			"image": image, "pull_policy": "never", "user": fmt.Sprintf("%d:%d", c.UID, c.GID),
			"read_only": true, "cap_drop": []string{"ALL"}, "security_opt": []string{"no-new-privileges:true"},
			"restart": "unless-stopped", "mem_limit": limits.Memory, "cpus": limits.CPUs, "pids_limit": limits.PIDs,
			"dns":    []string{"127.0.0.1"},
			"labels": map[string]string{historyRootLabel: wire.Digest([]byte(c.Root))},
		}
	}
	services := map[string]map[string]any{}
	for _, store := range []struct {
		name    string
		config  historyStoreConfig
		address netip.Addr
	}{
		{"raw", c.Raw, addresses.Postgres}, {"long", c.Long, addresses.Metrics},
	} {
		value := service(c.MetricsImage, c.StoreResources)
		listen, _ := netip.ParseAddrPort(store.config.Listen)
		value["ports"] = []any{map[string]any{"target": 8428, "published": strconv.Itoa(int(listen.Port())), "host_ip": listen.Addr().String(), "protocol": "tcp"}}
		value["command"] = []string{"-storageDataPath=/data", "-retentionPeriod=" + store.config.Retention,
			"-httpListenAddr=:8428", "-tls", "-tlsCertFile=/run/history/certificate.pem", "-tlsKeyFile=/run/history/private-key.pem"}
		value["volumes"] = []any{historyMount(filepath.Join(generation, store.name), "/run/history", true),
			historyMount(filepath.Join(c.Root, "data", store.name), "/data", false)}
		value["networks"] = map[string]any{"history": map[string]string{"ipv4_address": store.address.String()}}
		services[store.name] = value
	}
	evaluator := service(c.EvaluatorImage, c.EvaluatorResources)
	listen, _ := netip.ParseAddrPort(c.EvaluatorListen)
	evaluator["ports"] = []any{map[string]any{"target": 8880, "published": strconv.Itoa(int(listen.Port())), "host_ip": listen.Addr().String(), "protocol": "tcp"}}
	evaluator["command"] = []string{
		"-rule=/run/history/rules.yaml", "-httpListenAddr=:8880", "-external.url=http://127.0.0.1:8880",
		"-datasource.url=https://" + netip.AddrPortFrom(addresses.Postgres, 8428).String(), "-datasource.tlsCAFile=/run/history/raw-ca.pem",
		"-remoteWrite.url=https://" + netip.AddrPortFrom(addresses.Metrics, 8428).String(), "-remoteWrite.tlsCAFile=/run/history/long-ca.pem",
	}
	evaluator["volumes"] = []any{historyMount(filepath.Join(generation, "evaluator"), "/run/history", true)}
	evaluator["networks"] = []string{"history"}
	services["evaluator"] = evaluator
	return yaml.Marshal(map[string]any{
		"name": c.Project, "services": services,
		"networks": map[string]any{"history": map[string]any{
			"internal": true, "labels": map[string]string{historyRootLabel: wire.Digest([]byte(c.Root))}, "ipam": map[string]any{"config": []any{map[string]string{"subnet": c.Subnet, "ip_range": addresses.Dynamic.String()}}},
		}},
	})
}

func historyCompose(ctx context.Context, runner Runner, path string, args ...string) error {
	_, err := runner.Run(ctx, "docker", append([]string{"compose", "-f", path}, args...)...)
	if err != nil {
		return historyFailure("COMPOSE_FAILED", "inspect the private deployment with docker compose and retry fleet-history apply")
	}
	return nil
}

// Check runtime ownership before Compose can reuse an existing project or network.
func verifyHistoryProject(ctx context.Context, site historySite, runner Runner) error {
	for _, resource := range []struct {
		kind, format string
		args         []string
	}{
		{"container", "{{json .Config.Labels}}", []string{"container", "ls", "--all", "--quiet", "--filter", "label=com.docker.compose.project=" + site.config.Project}},
		{"network", "{{json .Labels}}", []string{"network", "ls", "--quiet", "--filter", "name=^" + site.config.Project + "_history$"}},
	} {
		output, err := runner.Run(ctx, "docker", resource.args...)
		if err != nil {
			return historyFailure("OWNER_UNAVAILABLE", "cannot verify existing project ownership")
		}
		for _, id := range strings.Fields(output) {
			labels, err := runner.Run(ctx, "docker", "inspect", "--type", resource.kind, "--format", resource.format, id)
			var actual map[string]string
			if err != nil || json.Unmarshal([]byte(labels), &actual) != nil || actual[historyRootLabel] != wire.Digest([]byte(site.config.Root)) {
				return historyFailure("OWNER_MISMATCH", "the Docker project or network belongs to another deployment root")
			}
		}
	}
	return nil
}

// ValidateFleetHistory checks protected site inputs without changing the deployment.
func ValidateFleetHistory(configPath string) error {
	_, err := loadHistorySite(configPath)
	return err
}

// ApplyFleetHistory installs or restarts the private TLS fleet-history stores and evaluator.
func ApplyFleetHistory(ctx context.Context, configPath string, runner Runner, out io.Writer) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return historyFailure("HOST_REQUIRED", "run as root on the Linux amd64 Docker host")
	}
	site, err := loadHistorySite(configPath)
	if err != nil {
		return err
	}
	return applyHistory(ctx, site, runner, out, probeHistory)
}

func applyHistory(ctx context.Context, site historySite, runner Runner, out io.Writer, probe func(context.Context, historySite) error) (result error) {
	if ctx == nil || runner == nil || out == nil || probe == nil {
		return historyFailure("OWNER_REQUIRED", "provide an installer context and command runner")
	}
	if err := site.ownRoot(); err != nil {
		return err
	}
	unlock, err := lockOperation(site.installerConfig())
	if err != nil {
		return err
	}
	defer unlock()
	if err := site.ownRoot(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(site.config.RequestSeconds)*time.Second)
	defer cancel()
	if err := verifyHistoryProject(ctx, site, runner); err != nil {
		return err
	}
	for _, image := range []string{site.config.MetricsImage, site.config.EvaluatorImage} {
		if _, err := runner.Run(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image); err != nil {
			return historyFailure("IMAGE_MISSING", "load both approved digest-pinned images before applying")
		}
	}
	generation, err := site.prepare()
	if err != nil {
		return err
	}
	operation := &operation{installer: &installer{config: site.installerConfig(), out: out}, now: time.Now}
	operation.record("fleet-history-apply", "started", "private TLS store and evaluator deployment")
	defer func() {
		if result != nil {
			operation.record("fleet-history-apply", "failed", "repair the reported deployment error and retry apply")
		} else {
			operation.record("fleet-history-apply", "complete", "stores and evaluator ready under the selected trust")
		}
	}()
	raw, err := site.compose(generation)
	if err != nil {
		return err
	}
	candidate := filepath.Join(site.config.Root, "compose-candidate.yaml")
	active := filepath.Join(site.config.Root, "compose.yaml")
	if err := writeFile(candidate, raw, site.installerConfig(), false); err != nil {
		return err
	}
	if err := historyCompose(ctx, runner, candidate, "config", "--quiet"); err != nil {
		return err
	}
	if err := historyCompose(ctx, runner, candidate, "run", "--rm", "--no-deps", "--no-TTY", "--pull", "never", "evaluator", "-rule=/run/history/rules.yaml", "-dryRun"); err != nil {
		return err
	}
	if exists(active) {
		if err := historyCompose(ctx, runner, active, "stop", "evaluator", "raw", "long"); err != nil {
			return err
		}
	}
	if err := writeFile(active, raw, site.installerConfig(), false); err != nil {
		return err
	}
	if err := syncDirectory(site.config.Root); err != nil {
		return err
	}
	if err := historyCompose(ctx, runner, active, "up", "--detach", "--no-build", "--pull", "never"); err != nil {
		return err
	}
	if err := probe(ctx, site); err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(struct {
		FormatVersion  int    `json:"format_version"`
		Status         string `json:"status"`
		MonitoringFile string `json:"monitoring_file"`
		ControlCAFile  string `json:"control_ca_file"`
	}{1, "FLEET_HISTORY_READY", filepath.Join(generation, "control/monitoring.json"), filepath.Join(generation, "control/ca.pem")})
}

func probeHistory(ctx context.Context, site historySite) error {
	for _, endpoint := range []struct {
		origin string
		roots  *tls.Config
	}{
		{"https://" + site.config.Raw.Listen, &tls.Config{RootCAs: site.rawRoots, MinVersion: tls.VersionTLS12}},
		{"https://" + site.config.Long.Listen, &tls.Config{RootCAs: site.longRoots, MinVersion: tls.VersionTLS12}},
		{"http://" + site.config.EvaluatorListen, nil},
	} {
		transport := &http.Transport{TLSClientConfig: endpoint.roots, DisableKeepAlives: true}
		client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer transport.CloseIdleConnections()
		for {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.origin+"/health", nil)
			if err != nil {
				return historyFailure("HEALTH_FAILED", "invalid health origin")
			}
			response, err := client.Do(req)
			if response != nil {
				response.Body.Close()
			}
			if err == nil && response.StatusCode == http.StatusOK {
				break
			}
			select {
			case <-ctx.Done():
				return historyFailure("HEALTH_FAILED", "a store or evaluator is not ready under the configured trust; repair it and retry apply")
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return nil
}
