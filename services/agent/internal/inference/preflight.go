package inference

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type DockerInfo struct {
	OSType          string
	Architecture    string
	MemoryLimit     bool
	SwapLimit       bool
	PidsLimit       bool
	CPUCfsQuota     bool `json:"CpuCfsQuota"`
	Runtimes        map[string]any
	SecurityOptions []string
}

func ProtectedDockerSocket(path string) error {
	// The production profile deliberately supports only a root-owned local Unix socket.
	resolved, e := filepath.EvalSymlinks(path)
	if e != nil || resolved != "/run/docker.sock" {
		return fail("INFERENCE_DOCKER_SOCKET_INVALID")
	}
	for current := resolved; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil {
			return fail("INFERENCE_DOCKER_SOCKET_INVALID")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != 0 {
			return fail("INFERENCE_DOCKER_SOCKET_INVALID")
		}
		if current == resolved {
			if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0007 != 0 {
				return fail("INFERENCE_DOCKER_SOCKET_INVALID")
			}
		} else if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
			return fail("INFERENCE_DOCKER_SOCKET_INVALID")
		}
		if current == "/" {
			break
		}
	}
	return nil
}
func (d *Docker) CheckEngine(ctx context.Context) error {
	var info DockerInfo
	if e := d.request(ctx, "GET", "/info", nil, &info, 200); e != nil {
		return e
	}
	if info.OSType != "linux" || info.Architecture != "x86_64" && info.Architecture != "amd64" {
		return fail("INFERENCE_PLATFORM_UNSUPPORTED")
	}
	if !info.MemoryLimit || !info.SwapLimit || !info.PidsLimit || !info.CPUCfsQuota {
		return fail("INFERENCE_RESOURCE_LIMITS_UNAVAILABLE")
	}
	if _, ok := info.Runtimes["nvidia"]; !ok {
		return fail("INFERENCE_TOOLKIT_UNAVAILABLE")
	}
	for _, value := range info.SecurityOptions {
		if strings.Contains(value, "rootless") {
			return fail("INFERENCE_DOCKER_SOCKET_INVALID")
		}
	}
	return nil
}
