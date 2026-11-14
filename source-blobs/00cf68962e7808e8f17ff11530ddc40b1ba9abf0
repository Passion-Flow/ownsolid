package components

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"strings"
)

const unitName = "projectone-node-exporter.service"
const unitPath = "/etc/systemd/system/" + unitName

type serviceManager interface {
	Reload(context.Context) error
	Start(context.Context) error
	Stop(context.Context) error
	Disable(context.Context) error
	State(context.Context) (string, error)
}
type systemd struct{ runtime *settings.Policy }
type boundedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > 4096 {
		b.exceeded = true
		return n, nil
	}
	b.Buffer.Write(p)
	return n, nil
}
func systemctl(ctx context.Context, args ...string) (string, error) {
	return (systemd{}).command(ctx, args...)
}
func (s systemd) command(ctx context.Context, args ...string) (string, error) {
	bounded, cancel := context.WithTimeout(ctx, settings.Seconds(settings.Resolve(s.runtime).SystemdSeconds))
	defer cancel()
	cmd := exec.CommandContext(bounded, "/usr/bin/systemctl", append([]string{"--no-pager", "--no-ask-password"}, args...)...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	var out boundedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	if cmd.Run() != nil || out.exceeded {
		return "", errors.New("COMPONENT_SERVICE_FAILED")
	}
	return strings.TrimSpace(out.String()), nil
}
func (s systemd) Reload(ctx context.Context) error { _, e := s.command(ctx, "daemon-reload"); return e }
func (s systemd) Start(ctx context.Context) error {
	if _, e := s.command(ctx, "enable", unitName); e != nil {
		return e
	}
	_, e := s.command(ctx, "start", unitName)
	return e
}
func (s systemd) Stop(ctx context.Context) error { _, e := s.command(ctx, "stop", unitName); return e }
func (s systemd) Disable(ctx context.Context) error {
	_, e := s.command(ctx, "disable", unitName)
	return e
}
func (s systemd) State(ctx context.Context) (string, error) {
	v, e := s.command(ctx, "show", unitName, "--property=ActiveState", "--value")
	if e != nil {
		return "unknown", e
	}
	switch v {
	case "active":
		return "running", nil
	case "inactive", "failed":
		return "stopped", nil
	}
	return "unknown", errors.New("COMPONENT_SERVICE_FAILED")
}
func systemdAvailable() error {
	i, e := os.Stat("/run/systemd/system")
	if e != nil || !i.IsDir() {
		return errors.New("COMPONENT_SYSTEMD_UNAVAILABLE")
	}
	i, e = os.Lstat("/usr/bin/systemctl")
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0022 != 0 || !rootOwned(i) {
		return errors.New("COMPONENT_SYSTEMD_UNAVAILABLE")
	}
	return nil
}
