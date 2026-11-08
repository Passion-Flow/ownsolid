package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

const bundleBindingName = "release-binding.json"

var bundleImageRoles = []string{"control-image", "postgres-image", "metrics-image", "logs-image"}

type bundleBinding struct {
	FormatVersion  int       `json:"format_version"`
	ReleaseID      string    `json:"release_id"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	ImageIDs       [4]string `json:"image_ids"`
}

func (b bundleBinding) images() ImagesConfig {
	return ImagesConfig{Control: b.ImageIDs[0], Postgres: b.ImageIDs[1], Metrics: b.ImageIDs[2], Logs: b.ImageIDs[3]}
}

// LoadBundleVerification reads only public verification policy from a protected file.
func LoadBundleVerification(path string) (bundle.Verification, error) {
	raw, err := bundle.ReadProtectedConfiguration(path)
	if err != nil {
		return bundle.Verification{}, err
	}
	policy, err := bundle.LoadVerification(raw, nil)
	if err != nil {
		return policy, err
	}
	if !canonicalPath(policy.MountRoot) || policy.MountRoot == "/" || !canonicalPath(policy.TrustFile) || policy.AllowTestFixture {
		return policy, errors.New("INSTALL_BUNDLE_POLICY_INVALID: set an absolute mount root and an independent public trust file")
	}
	return policy, nil
}

// LoadPreflight accepts both a legacy install file and a site-only signed bundle configuration.
func LoadPreflight(path string) (Config, error) {
	c, err := readConfiguration(path)
	if err != nil {
		return Config{}, err
	}
	if err := c.validate(c.ComposeFile == "" && c.Images == (ImagesConfig{})); err != nil {
		return Config{}, err
	}
	return c, nil
}

// LoadOperational resolves omitted delivery inputs from the local signed release binding.
func LoadOperational(path string) (Config, error) {
	c, err := readConfiguration(path)
	if err != nil {
		return Config{}, err
	}
	if c.ComposeFile == "" && c.Images == (ImagesConfig{}) {
		if err := c.validate(true); err != nil {
			return Config{}, err
		}
		binding, err := readBundleBinding(c)
		if err != nil {
			return Config{}, err
		}
		if binding == nil {
			return Config{}, errors.New("INSTALL_BUNDLE_BINDING_MISSING: no signed release is bound to this installation")
		}
		c.ComposeFile = c.Layout().ComposeFile
		c.Images = binding.images()
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func readBundleBinding(c Config) (*bundleBinding, error) {
	path := filepath.Join(c.InstallRoot, bundleBindingName)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	raw, err := bundle.ReadProtectedConfiguration(path)
	var binding bundleBinding
	if err != nil || wire.StrictJSON(raw, &binding) != nil || binding.FormatVersion != 1 ||
		binding.ReleaseID == "" || !wire.ValidSHA256(binding.ManifestSHA256) {
		return nil, errors.New("INSTALL_BUNDLE_BINDING_INVALID: the local release binding is unreadable or invalid")
	}
	for _, id := range binding.ImageIDs {
		digest, ok := strings.CutPrefix(id, "sha256:")
		if !ok || !wire.ValidSHA256(digest) {
			return nil, errors.New("INSTALL_BUNDLE_BINDING_INVALID: the local release binding has an invalid image identity")
		}
	}
	return &binding, nil
}

// ApplyBundle installs a verified single-node bundle or resumes its installation.
// A different release or an existing unsigned installation requires an upgrade path.
func ApplyBundle(ctx context.Context, configPath, archive string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("INSTALL_BUNDLE_HOST_REQUIRED: run as root on the Linux amd64 Docker host")
	}
	c, err := readConfiguration(configPath)
	if err != nil {
		return err
	}
	if err := c.validate(true); err != nil {
		return err
	}
	mount, err := filepath.EvalSymlinks(policy.MountRoot)
	if err != nil || mount != policy.MountRoot || !canonicalPath(mount) || mount == "/" {
		return errors.New("INSTALL_BUNDLE_STAGING_UNAVAILABLE: use a real mounted volume for release preparation")
	}
	stage, err := os.MkdirTemp(mount, ".ownsolid-install-")
	if err != nil {
		return errors.New("INSTALL_BUNDLE_STAGING_UNAVAILABLE: check the mounted volume's permissions and free space")
	}
	defer os.RemoveAll(stage)
	prepared := filepath.Join(stage, "prepared")
	result, err := bundle.UnpackCurrent(ctx, policy, archive, prepared)
	if err != nil {
		return fmt.Errorf("INSTALL_BUNDLE_VERIFICATION_FAILED: %w", err)
	}
	assets := make(map[string]wire.Asset, len(result.Manifest.Assets))
	for _, asset := range result.Manifest.Assets {
		assets[asset.ID] = asset
	}
	binding := bundleBinding{FormatVersion: 1, ReleaseID: result.ReleaseID, ManifestSHA256: result.ManifestSHA256}
	for n, role := range bundleImageRoles {
		binding.ImageIDs[n] = assets[role].ImageID
	}
	c.ComposeFile = filepath.Join(prepared, filepath.FromSlash(assets["compose"].Path))
	c.Images = binding.images()
	if err := checkBundleCompose(c.ComposeFile); err != nil {
		return err
	}
	if c.Preflight != nil {
		if err := Preflight(ctx, c, runner, out); err != nil {
			return retryWith(err, "apply-bundle")
		}
	} else {
		if err := checkLocalDocker(ctx, runner); err != nil {
			return err
		}
		i := installer{config: c, runner: runner}
		if err := i.preflight(ctx); err != nil {
			return retryWith(wrapStep("preflight", err), "apply-bundle")
		}
	}
	backend, err := runner.Run(ctx, "docker", "info", "--format", "{{json .DriverStatus}}")
	var drivers [][2]string
	if err != nil || json.Unmarshal([]byte(backend), &drivers) != nil || !slices.Contains(drivers, [2]string{"driver-type", "io.containerd.snapshotter.v1"}) {
		return errors.New("INSTALL_BUNDLE_DOCKER_STORE_REQUIRED: enable Docker's local containerd image store")
	}
	unlock, err := lockOperation(c)
	if err != nil {
		return err
	}
	defer unlock()
	if err := bindRelease(c, binding); err != nil {
		return err
	}
	fmt.Fprintf(out, "INSTALL_BUNDLE_VERIFIED release=%s manifest_sha256=%s\n", result.ReleaseID, result.ManifestSHA256)
	for _, role := range bundleImageRoles {
		step, cancel := context.WithTimeout(ctx, time.Duration(c.StepSeconds)*time.Second)
		err := loadBundleImage(step, runner, policy, prepared, assets[role])
		cancel()
		if err != nil {
			return retryWith(wrapStep("image/"+role, err), "apply-bundle")
		}
	}
	op := newOperation(c, runner, out)
	op.installer.verifySchema = func(ctx context.Context) error {
		version, err := op.schemaVersion(ctx, databaseName)
		if err != nil || version != result.Manifest.SchemaVersion {
			return errors.New("INSTALL_BUNDLE_SCHEMA_MISMATCH: database schema does not match the signed release")
		}
		return nil
	}
	return retryWith(op.installer.apply(ctx), "apply-bundle")
}

func bindRelease(c Config, binding bundleBinding) error {
	resolved, err := filepath.EvalSymlinks(c.InstallRoot)
	if err != nil || resolved != c.InstallRoot {
		return errors.New("INSTALL_BUNDLE_BINDING_INVALID: use an installation root without symlinks")
	}
	info, err := os.Stat(c.InstallRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return errors.New("INSTALL_BUNDLE_BINDING_INVALID: the installation root must be private")
	}
	prior, err := readBundleBinding(c)
	if err != nil {
		return err
	}
	if prior != nil {
		if *prior != binding {
			return errors.New("INSTALL_BUNDLE_UPGRADE_REQUIRED: this installation is bound to another release")
		}
		return nil
	}
	for _, name := range []string{c.Layout().ComposeFile, c.Layout().Postgres} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			return errors.New("INSTALL_BUNDLE_UPGRADE_REQUIRED: the installation root already contains unbound deployment state")
		}
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(c.InstallRoot, bundleBindingName), append(raw, '\n'), c, false); err != nil {
		return err
	}
	root, err := os.Open(c.InstallRoot)
	if err != nil {
		return errors.New("INSTALL_BUNDLE_BINDING_SYNC_FAILED: the binding may already exist; retry the same bundle")
	}
	if err := errors.Join(root.Sync(), root.Close()); err != nil {
		return errors.New("INSTALL_BUNDLE_BINDING_SYNC_FAILED: the binding may already exist; retry the same bundle")
	}
	return nil
}

func loadBundleImage(ctx context.Context, runner StreamRunner, policy bundle.Verification, prepared string, asset wire.Asset) error {
	const format = "{{.Id}} {{.Os}} {{.Architecture}}"
	id := asset.ImageID
	want := id + " linux amd64"
	inspect := func() bool {
		raw, err := runner.Run(ctx, "docker", "image", "inspect", "--format", format, id)
		return err == nil && strings.TrimSpace(raw) == want
	}
	if inspect() {
		return nil
	}
	filename := filepath.Join(prepared, filepath.FromSlash(asset.Path))
	source, err := bundle.OpenCurrentImageSource(ctx, policy, filename, asset, policy.MaximumTotalBytes)
	if err != nil {
		return fmt.Errorf("INSTALL_BUNDLE_IMAGE_INVALID: %w", err)
	}
	defer source.Close()
	reader, writer := io.Pipe()
	written := make(chan error, 1)
	go func() {
		_, err := source.WriteTagless(ctx, writer)
		writer.CloseWithError(err)
		written <- err
	}()
	diagnostic, loadError := runner.Stream(ctx, reader, io.Discard, "docker", "image", "load")
	reader.Close()
	if err := <-written; err != nil || loadError != nil {
		return fmt.Errorf("INSTALL_BUNDLE_IMAGE_LOAD_FAILED: Docker did not accept the verified OCI image: %s", lastLines(diagnostic))
	}
	if !inspect() {
		return errors.New("INSTALL_BUNDLE_IMAGE_IDENTITY_MISMATCH: loaded image did not match the signed identity")
	}
	return nil
}
