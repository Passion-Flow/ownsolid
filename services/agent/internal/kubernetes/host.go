package kubernetes

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// host is what the manager needs from the machine. The real host runs commands and reads
// the kernel's tables; tests substitute a fake.
type host interface {
	Privileged() bool
	SystemdRunning() bool
	// DefaultRoute reports whether the kernel has a default route of the address's family.
	// RKE2 refuses to start without one, even on a network with no route out.
	DefaultRoute(address netip.Addr) (bool, error)
	// LocalAddress reports whether an interface of this machine holds the address.
	LocalAddress(address netip.Addr) (bool, error)
	FreeBytes(path string) (int64, error)
	// Run runs a command and returns its standard output, or an error carrying both streams.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type realHost struct{}

func (realHost) Privileged() bool { return os.Geteuid() == 0 }

func (realHost) SystemdRunning() bool {
	info, e := os.Stat("/run/systemd/system")
	return e == nil && info.IsDir()
}

func (realHost) DefaultRoute(address netip.Addr) (bool, error) {
	if address.Is4() {
		return defaultRoute("/proc/net/route", ipv4DefaultRoute)
	}
	return defaultRoute("/proc/net/ipv6_route", ipv6DefaultRoute)
}

func defaultRoute(path string, matches func([]string) bool) (bool, error) {
	content, e := os.ReadFile(path)
	if e != nil {
		return false, e
	}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		if matches(strings.Fields(scanner.Text())) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// ipv4DefaultRoute matches a /proc/net/route line whose destination and mask are zero.
func ipv4DefaultRoute(fields []string) bool {
	return len(fields) >= 8 && fields[1] == "00000000" && fields[7] == "00000000"
}

// ipv6DefaultRoute matches a /proc/net/ipv6_route line for ::/0 that is not the kernel's
// unreachable placeholder on the loopback interface.
func ipv6DefaultRoute(fields []string) bool {
	return len(fields) >= 10 && fields[0] == strings.Repeat("0", 32) && fields[1] == "00" && fields[9] != "lo"
}

func (realHost) LocalAddress(address netip.Addr) (bool, error) {
	addresses, e := net.InterfaceAddrs()
	if e != nil {
		return false, e
	}
	for _, candidate := range addresses {
		if network, ok := candidate.(*net.IPNet); ok {
			if local, ok := netip.AddrFromSlice(network.IP); ok && local.Unmap() == address {
				return true, nil
			}
		}
	}
	return false, nil
}

func (realHost) FreeBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if e := syscall.Statfs(path, &stat); e != nil {
		return 0, e
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func (realHost) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = nil
	command.Stdout, command.Stderr = &stdout, &stderr
	command.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	if e := command.Run(); e != nil {
		return stdout.Bytes(), errors.Join(e, errors.New(strings.TrimSpace(stderr.String())))
	}
	return stdout.Bytes(), nil
}

// resolverFile chooses the resolv.conf the kubelet gives CoreDNS. RKE2 falls back to a
// public resolver when the file it reads names only loopback servers, which a node with
// systemd-resolved does; on a network with no route out that is a lookup that can never
// succeed. So the file must name a real server.
func resolverFile(root string) (string, error) {
	for _, path := range []string{"/run/systemd/resolve/resolv.conf", "/etc/resolv.conf"} {
		content, e := os.ReadFile(root + path)
		if e != nil {
			continue
		}
		if hasUpstreamServer(content) {
			return path, nil
		}
	}
	return "", errors.New("KUBERNETES_RESOLVER_INVALID")
}

func hasUpstreamServer(content []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		address, e := netip.ParseAddr(fields[1])
		if e == nil && !address.IsLoopback() && !address.IsUnspecified() && !address.IsMulticast() && !address.IsLinkLocalUnicast() {
			return true
		}
	}
	return false
}
