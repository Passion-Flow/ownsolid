package install

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// PreflightPolicy sets the site requirements checked before an installation changes state.
type PreflightPolicy struct {
	MinimumKernel          string `yaml:"minimum_kernel"`
	MinimumDataFreeBytes   uint64 `yaml:"minimum_data_free_bytes"`
	ContainerdRoot         string `yaml:"containerd_root"`
	MinimumDockerFreeBytes uint64 `yaml:"minimum_docker_free_bytes"`
	MinimumMemoryBytes     uint64 `yaml:"minimum_memory_bytes"`
	MinimumUTC             string `yaml:"minimum_utc"`
	RequireClockSync       bool   `yaml:"require_clock_sync"`
	DNSServer              string `yaml:"dns_server"`
	RequireNVIDIA          bool   `yaml:"require_nvidia"`
}

var kernelReleasePattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$`)

func kernelRelease(value string) ([3]uint64, bool) {
	var result [3]uint64
	match := kernelReleasePattern.FindStringSubmatch(value)
	if match == nil {
		return result, false
	}
	for i, field := range match[1:] {
		number, err := strconv.ParseUint(field, 10, 32)
		if err != nil {
			return result, false
		}
		result[i] = number
	}
	return result, true
}

func (p PreflightPolicy) validate() error {
	if _, ok := kernelRelease(p.MinimumKernel); !ok || strings.ContainsAny(p.MinimumKernel, "-+") {
		return invalid("preflight.minimum_kernel", "must be a numeric Linux kernel release such as 5.15.0")
	}
	if p.MinimumDataFreeBytes == 0 || p.MinimumDockerFreeBytes == 0 || p.MinimumMemoryBytes == 0 {
		return invalid("preflight", "requires positive data free, Docker free and memory budgets in bytes")
	}
	if p.ContainerdRoot != "" && (!canonicalPath(p.ContainerdRoot) || p.ContainerdRoot == "/") {
		return invalid("preflight.containerd_root", "must be an absolute clean directory below /")
	}
	if _, err := time.Parse(time.RFC3339, p.MinimumUTC); err != nil || !strings.HasSuffix(p.MinimumUTC, "Z") {
		return invalid("preflight.minimum_utc", "must be an RFC 3339 UTC timestamp ending in Z")
	}
	if p.DNSServer != "" {
		server, err := netip.ParseAddrPort(p.DNSServer)
		if err != nil || server.Port() == 0 || server.Addr().Zone() != "" ||
			!(server.Addr().Unmap().IsPrivate() || server.Addr().Unmap().IsLoopback()) {
			return invalid("preflight.dns_server", "must be an explicit private or loopback IP address and port")
		}
	}
	return nil
}

type dockerHostInfo struct {
	OSType, Architecture, KernelVersion, DockerRootDir string
	MemTotal                                           uint64
	Runtimes                                           map[string]json.RawMessage
	DriverStatus                                       [][2]string
}

// Preflight checks the Linux host and Docker daemon without preparing or starting an installation.
func Preflight(ctx context.Context, c Config, runner Runner, out io.Writer) error {
	if c.Preflight == nil {
		return invalid("preflight", "is required for host preflight")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
	defer cancel()
	check := func(name string, run func() error) error {
		if err := run(); err != nil {
			return retryWith(wrapStep("preflight/"+name, err), "preflight")
		}
		_, err := fmt.Fprintf(out, "preflight %s: ok\n", name)
		return err
	}
	p := *c.Preflight
	var host dockerHostInfo
	if err := check("platform", func() error {
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
			return errors.New("INSTALL_PREFLIGHT_PLATFORM: run the installer on the Linux amd64 Docker host")
		}
		if err := checkLocalDocker(ctx, runner); err != nil {
			return err
		}
		const format = `{"OSType":{{json .OSType}},"Architecture":{{json .Architecture}},"KernelVersion":{{json .KernelVersion}},"DockerRootDir":{{json .DockerRootDir}},"MemTotal":{{json .MemTotal}},"DriverStatus":{{json .DriverStatus}},"Runtimes":{{json .Runtimes}}}`
		raw, err := runner.Run(ctx, "docker", "info", "--format", format)
		if err != nil {
			return errors.New("INSTALL_DOCKER_UNAVAILABLE: start the local Docker daemon and check socket access")
		}
		if json.Unmarshal([]byte(raw), &host) != nil || host.OSType != "linux" || host.Architecture != "x86_64" || !canonicalPath(host.DockerRootDir) {
			return errors.New("INSTALL_PREFLIGHT_DOCKER_PLATFORM: expected local Docker on Linux amd64 with a readable storage path")
		}
		_, err = runner.Run(ctx, "docker", "compose", "version", "--short")
		if err != nil {
			return errors.New("INSTALL_COMPOSE_UNAVAILABLE: install the Compose plugin from local packages")
		}
		return nil
	}); err != nil {
		return err
	}
	checks := []struct {
		name string
		run  func() error
	}{
		{"kernel", func() error { return checkKernel(host.KernelVersion, p.MinimumKernel) }},
		{"ports", func() error { return checkPublishedPort(ctx, c, runner) }},
		{"disk", func() error { return checkDisk(c, host, p) }},
		{"memory", func() error {
			return checkMemory(host.MemTotal, p.MinimumMemoryBytes)
		}},
		{"clock", func() error {
			earliest, _ := time.Parse(time.RFC3339, p.MinimumUTC)
			return checkClock(time.Now(), earliest, p.RequireClockSync)
		}},
		{"dns", func() error { return checkOriginResolution(ctx, c, p.DNSServer) }},
	}
	if p.RequireNVIDIA {
		checks = append(checks, struct {
			name string
			run  func() error
		}{"nvidia", func() error { return checkNVIDIA(ctx, runner, host) }})
	}
	for _, item := range checks {
		if err := check(item.name, item.run); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(out, "INSTALL_PREFLIGHT_PASSED")
	return err
}

func checkLocalDocker(ctx context.Context, runner Runner) error {
	endpoint := os.Getenv("DOCKER_HOST")
	if endpoint == "" {
		raw, err := runner.Run(ctx, "docker", "context", "inspect", "--format", `{{(index .Endpoints "docker").Host}}`)
		if err != nil {
			return errors.New("INSTALL_PREFLIGHT_DOCKER_ENDPOINT: select the local Docker context")
		}
		endpoint = strings.TrimSpace(raw)
	}
	socket, ok := strings.CutPrefix(endpoint, "unix://")
	if !ok || !canonicalPath(socket) {
		return errors.New("INSTALL_PREFLIGHT_DOCKER_ENDPOINT: select a local Unix Docker socket")
	}
	return nil
}

func checkKernel(actual, minimum string) error {
	have, ok := kernelRelease(actual)
	want, _ := kernelRelease(minimum)
	if ok {
		for i, part := range have {
			if part > want[i] {
				return nil
			}
			if part < want[i] {
				break
			}
			if i == len(have)-1 {
				return nil
			}
		}
	}
	return fmt.Errorf("INSTALL_PREFLIGHT_KERNEL: Docker reports kernel %q, need Linux %s or newer", actual, minimum)
}

type portPublication struct {
	HostIP   string
	HostPort string
}

type portOwner struct {
	Project, Service, ConfigFile string
	Ports                        map[string][]portPublication
}

func checkPublishedPort(ctx context.Context, c Config, runner Runner) error {
	raw, err := runner.Run(ctx, "docker", "ps", "--quiet", "--no-trunc")
	if err != nil {
		return errors.New("INSTALL_PREFLIGHT_PORT_INSPECT: cannot read running Docker containers")
	}
	address := netip.MustParseAddr(c.Network.BindAddress).Unmap()
	port := strconv.Itoa(c.Network.HTTPSPort)
	owned := false
	if ids := strings.Fields(raw); len(ids) > 0 {
		const format = `{"Project":{{json (index .Config.Labels "com.docker.compose.project")}},"Service":{{json (index .Config.Labels "com.docker.compose.service")}},"ConfigFile":{{json (index .Config.Labels "com.docker.compose.project.config_files")}},"Ports":{{json .NetworkSettings.Ports}}}`
		result, err := runner.Run(ctx, "docker", append([]string{"inspect", "--type", "container", "--format", format, "--"}, ids...)...)
		if err != nil {
			return errors.New("INSTALL_PREFLIGHT_PORT_INSPECT: Docker port assignments changed or could not be read; retry")
		}
		decoder := json.NewDecoder(strings.NewReader(result))
		for range ids {
			var owner portOwner
			if err = decoder.Decode(&owner); err != nil {
				return errors.New("INSTALL_PREFLIGHT_PORT_INSPECT: Docker returned incomplete port assignments; retry")
			}
			for protocol, bindings := range owner.Ports {
				if !strings.HasSuffix(protocol, "/tcp") {
					continue
				}
				for _, binding := range bindings {
					host, err := netip.ParseAddr(binding.HostIP)
					if err != nil {
						return errors.New("INSTALL_PREFLIGHT_PORT_INSPECT: Docker returned an invalid bind address")
					}
					host = host.Unmap()
					if binding.HostPort != port || !addressesOverlap(host, address) {
						continue
					}
					if owner.Project != c.Project || owner.Service != "control" || owner.ConfigFile != c.Layout().ComposeFile || host != address {
						return fmt.Errorf("INSTALL_PREFLIGHT_PORT: another Docker service publishes %s; choose an available network.https_port", netip.AddrPortFrom(address, uint16(c.Network.HTTPSPort)))
					}
					owned = true
				}
			}
		}
	}
	target := net.JoinHostPort(address.String(), port)
	network := "tcp4"
	if address.Is6() {
		network = "tcp6"
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, network, target)
	if err != nil {
		if owned && errors.Is(err, syscall.EADDRINUSE) {
			return nil
		}
		return fmt.Errorf("INSTALL_PREFLIGHT_PORT: cannot bind %s; verify the address and free the port: %w", target, err)
	}
	return listener.Close()
}

func addressesOverlap(a, b netip.Addr) bool {
	return a.Is4() == b.Is4() && (a == b || a.IsUnspecified() || b.IsUnspecified())
}

func checkDisk(c Config, host dockerHostInfo, p PreflightPolicy) error {
	layout := c.Layout()
	// Reserve space on every filesystem that receives data. If Docker and the
	// install share a filesystem, both reserves must fit at the same time.
	type budget struct {
		path         string
		data, docker uint64
		free         uint64
	}
	budgets := map[uint64]budget{}
	var order []uint64
	reserve := func(path string, docker bool) error {
		probe, info, err := existingDirectory(path)
		if err != nil || (docker && probe != path) {
			return fmt.Errorf("INSTALL_PREFLIGHT_DISK: cannot find a storage directory at %s", path)
		}
		device := uint64(info.Sys().(*syscall.Stat_t).Dev)
		current, exists := budgets[device]
		if !exists {
			var fs syscall.Statfs_t
			if err = syscall.Statfs(probe, &fs); err != nil || fs.Bsize <= 0 {
				return fmt.Errorf("INSTALL_PREFLIGHT_DISK: cannot read free space at %s", probe)
			}
			current = budget{path: probe, free: uint64(fs.Bavail) * uint64(fs.Bsize)}
			order = append(order, device)
		}
		if docker {
			current.docker = p.MinimumDockerFreeBytes
		} else {
			current.data = p.MinimumDataFreeBytes
		}
		budgets[device] = current
		return nil
	}
	for _, path := range []string{layout.Root, layout.Postgres, layout.Metrics, layout.Logs, layout.Models} {
		if err := reserve(path, false); err != nil {
			return err
		}
	}
	if err := reserve(host.DockerRootDir, true); err != nil {
		return err
	}
	for _, status := range host.DriverStatus {
		if status != [2]string{"driver-type", "io.containerd.snapshotter.v1"} {
			continue
		}
		if p.ContainerdRoot == "" {
			return errors.New("INSTALL_PREFLIGHT_DISK: set preflight.containerd_root to the daemon's containerd image store directory")
		}
		if err := reserve(p.ContainerdRoot, true); err != nil {
			return err
		}
	}
	for _, device := range order {
		item := budgets[device]
		if item.data > item.free || item.docker > item.free-item.data {
			return fmt.Errorf("INSTALL_PREFLIGHT_DISK: %s has %d bytes free, needs data reserve %d and Docker reserve %d; free space or use another volume", item.path, item.free, item.data, item.docker)
		}
	}
	return nil
}

func checkMemory(actual, minimum uint64) error {
	if actual < minimum {
		return fmt.Errorf("INSTALL_PREFLIGHT_MEMORY: Docker reports %d bytes of memory, need %d", actual, minimum)
	}
	return nil
}

func existingDirectory(path string) (string, os.FileInfo, error) {
	for {
		info, err := os.Stat(path)
		if err == nil {
			if !info.IsDir() {
				return "", nil, errors.New("expected a directory")
			}
			return path, info, nil
		}
		if !errors.Is(err, os.ErrNotExist) || path == "/" {
			return "", nil, err
		}
		path = filepath.Dir(path)
	}
}

func checkClock(now, earliest time.Time, requireSync bool) error {
	if now.Before(earliest) {
		return fmt.Errorf("INSTALL_PREFLIGHT_CLOCK: host UTC time %s precedes the configured reference %s; correct the host clock", now.UTC().Format(time.RFC3339), earliest.Format(time.RFC3339))
	}
	if requireSync && !clockSynchronized() {
		return errors.New("INSTALL_PREFLIGHT_CLOCK: Linux does not report a synchronized clock; check the site's time source")
	}
	return nil
}

func checkOriginResolution(ctx context.Context, c Config, server string) error {
	hosts, err := c.originHosts()
	if err != nil {
		return err
	}
	file, err := os.Open("/etc/hosts")
	if err != nil {
		return errors.New("INSTALL_PREFLIGHT_DNS: cannot read /etc/hosts")
	}
	defer file.Close()
	local, err := hostMappings(file)
	if err != nil {
		return errors.New("INSTALL_PREFLIGHT_DNS: cannot read /etc/hosts")
	}
	resolver := &net.Resolver{PreferGo: true, StrictErrors: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server)
	}}
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if _, err := netip.ParseAddr(host); err == nil || local[host] {
			continue
		}
		if server != "" {
			addrs, err := resolver.LookupNetIP(ctx, "ip", host+".")
			if err == nil && len(addrs) > 0 {
				continue
			}
		}
		return fmt.Errorf("INSTALL_PREFLIGHT_DNS: %s has no address in /etc/hosts or the configured private DNS resolver", host)
	}
	return nil
}

func hostMappings(reader io.Reader) (map[string]bool, error) {
	mappings := map[string]bool{}
	lines := bufio.NewScanner(reader)
	for lines.Scan() {
		line, _, _ := strings.Cut(lines.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if address, err := netip.ParseAddr(fields[0]); err != nil || address.IsUnspecified() || address.IsMulticast() {
			continue
		}
		for _, name := range fields[1:] {
			mappings[strings.ToLower(strings.TrimSuffix(name, "."))] = true
		}
	}
	return mappings, lines.Err()
}

func checkNVIDIA(ctx context.Context, runner Runner, host dockerHostInfo) error {
	if _, ok := host.Runtimes["nvidia"]; !ok {
		return errors.New("INSTALL_PREFLIGHT_NVIDIA: configure the NVIDIA runtime in local Docker")
	}
	raw, err := runner.Run(ctx, "nvidia-smi", "--query-gpu=driver_version", "--format=csv,noheader")
	if err != nil || strings.TrimSpace(raw) == "" {
		return errors.New("INSTALL_PREFLIGHT_NVIDIA: nvidia-smi could not find a GPU; check the host driver and device access")
	}
	return nil
}
