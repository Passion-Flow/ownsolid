package install

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"go.yaml.in/yaml/v3"
)

// listenerCompatibility checks new listener intent before Materialize can replace
// the configuration an existing Control image needs to restart.
func (i *installer) listenerCompatibility(ctx context.Context) (err error) {
	i.admittedTLS = nil
	if i.config.Network.PortalListener == nil {
		return nil
	}
	l := i.config.Layout()
	info, err := os.Lstat(l.ComposeEnv)
	if errors.Is(err, os.ErrNotExist) {
		return nil // Fresh installation; the ordinary serving check admits it later.
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("INSTALL_SERVING_CHECK_UNAVAILABLE: the installed Compose environment must be a private regular file")
	}
	for _, path := range []string{l.Root, l.Config, l.Operator} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			return errors.New("INSTALL_SERVING_CHECK_UNAVAILABLE: existing installation directories must be private and not symlinks")
		}
	}
	if err := checkInstalledProject(i.config); err != nil {
		return err
	}
	if err := i.stableListenerOrigins(); err != nil {
		return err
	}
	image, err := i.listenerSourceImage(ctx)
	if err != nil {
		return err
	}
	material, err := i.config.resolveServingTLS(l)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp(l.Operator, "serving-intent-*")
	if err != nil {
		return errors.New("INSTALL_SERVING_CHECK_UNAVAILABLE: cannot reserve private desired material")
	}
	defer removeServingDirectory(directory, &err)
	if err := privateDirectory(directory, i.config, true); err != nil {
		return err
	}
	for name, raw := range map[string][]byte{"certificate.pem": material.certificate, "private-key.pem": material.privateKey} {
		if err := writeFile(filepath.Join(directory, name), raw, i.config, true); err != nil {
			return err
		}
	}
	containerDirectory := containerOperatorDir + "/" + filepath.Base(directory)
	certificateFile, privateKeyFile := containerDirectory+"/certificate.pem", containerDirectory+"/private-key.pem"
	snapshot, err := i.config.renderControlWithTLS(containerOperatorDir+"/operator-dsn", false, certificateFile, privateKeyFile)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(directory, "control.yaml"), snapshot, i.config, true); err != nil {
		return err
	}
	args, err := i.config.servingArgumentsWithTLS(containerDirectory+"/control.yaml", certificateFile, privateKeyFile)
	if err != nil {
		return err
	}
	if err = i.verifyServingIntent(ctx, image, args); err != nil {
		return err
	}
	i.admittedTLS = material
	return nil
}

// Changing a listener must not silently migrate SSO callbacks or session origins.
// This compares public persisted identities; the Control CLI owns TLS validation.
func (i *installer) stableListenerOrigins() error {
	path := filepath.Join(i.config.Layout().Config, "control.yaml")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("INSTALL_SERVING_CHECK_UNAVAILABLE: cannot read the private installed browser identities")
	}
	raw, err := os.ReadFile(path)
	var installed struct {
		Server struct {
			Portal string `yaml:"portal_origin"`
			Admin  string `yaml:"admin_origin"`
		} `yaml:"server"`
	}
	if err != nil || yaml.Unmarshal(raw, &installed) != nil || installed.Server.Portal == "" || installed.Server.Admin == "" {
		return errors.New("INSTALL_SERVING_CHECK_UNAVAILABLE: cannot read the installed browser identities")
	}
	if installed.Server.Portal != i.config.Origins.Portal || installed.Server.Admin != i.config.Origins.Admin {
		return errors.New("INSTALL_LISTENER_ADDRESS_CHANGE_REQUIRED: keep both advertised origins unchanged when selecting a listener; address, session and SSO migration must precede this change")
	}
	return nil
}

func (i *installer) listenerSourceImage(ctx context.Context) (string, error) {
	bad := errors.New("INSTALL_SERVING_CURRENT_IMAGE_UNAVAILABLE: cannot identify the installed Control image; load its local media and retry before changing listeners")
	output, err := i.compose(ctx, "ps", "--all", "--quiet", "control")
	if err != nil {
		return "", bad
	}
	ids := strings.Fields(output)
	if len(ids) > 1 {
		return "", bad
	}
	if len(ids) == 1 {
		if !wire.ValidSHA256(ids[0]) {
			return "", bad
		}
		const projection = `{"Image":{{json .Image}},"Project":{{json (index .Config.Labels "com.docker.compose.project")}},"Service":{{json (index .Config.Labels "com.docker.compose.service")}},"ConfigFile":{{json (index .Config.Labels "com.docker.compose.project.config_files")}}}`
		raw, err := i.runner.Run(ctx, "docker", "inspect", "--type", "container", "--format", projection, "--", ids[0])
		var observed struct{ Image, Project, Service, ConfigFile string }
		l := i.config.Layout()
		if err != nil || wire.StrictJSON(bytes.TrimSpace([]byte(raw)), &observed) != nil || observed.Project != i.config.Project || observed.Service != "control" ||
			(observed.ConfigFile != l.ComposeFile && observed.ConfigFile != l.ComposeFile+","+filepath.Join(l.Root, pitrComposeName)) {
			return "", bad
		}
		digest, ok := strings.CutPrefix(observed.Image, "sha256:")
		if !ok || !wire.ValidSHA256(digest) {
			return "", bad
		}
		return observed.Image, nil
	}
	// Materialize may have finished before an interrupted first install created
	// Control. Resolve the recorded image locally, preserving resumability.
	raw, err := bundle.ReadProtectedConfiguration(i.config.Layout().ComposeEnv)
	if err != nil {
		return "", bad
	}
	image := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if value, found := strings.CutPrefix(line, "OWNSOLID_CONTROL_IMAGE="); found {
			if image != "" || !pinnedImage(value) {
				return "", bad
			}
			image = value
		}
	}
	if image == "" {
		return "", bad
	}
	output, err = i.runner.Run(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image)
	image = strings.TrimSpace(output)
	digest, ok := strings.CutPrefix(image, "sha256:")
	if err != nil || !ok || !wire.ValidSHA256(digest) {
		return "", bad
	}
	return image, nil
}
