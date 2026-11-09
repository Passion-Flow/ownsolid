//go:build !linux

package install

import (
	"context"
	"errors"
	"io"

	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

// InstallAgent requires the Linux systemd host installer.
func InstallAgent(context.Context, string, string, bundle.Verification, io.Writer) error {
	return errors.New("INSTALL_AGENT_HOST_REQUIRED: run as root on a Linux amd64 systemd host")
}
