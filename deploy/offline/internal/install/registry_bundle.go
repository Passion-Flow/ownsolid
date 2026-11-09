package install

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	"go.yaml.in/yaml/v3"
	"golang.org/x/crypto/bcrypt"
)

const registryBindingFile = "registry-release.json"

type registryBinding struct {
	ReleaseSHA256 string `json:"release_sha256"`
	SiteSHA256    string `json:"site_sha256"`
	ImageID       string `json:"image_id"`
}

type registrySeed struct {
	repo   string
	source *bundle.ImageSource
}

// ApplyRegistryBundle provisions a private pull-only Registry from a verified release.
func ApplyRegistryBundle(ctx context.Context, configPath, archive string, policy bundle.Verification, runner StreamRunner, out io.Writer) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("INSTALL_BUNDLE_HOST_REQUIRED: run as root on the Linux amd64 Docker host")
	}
	site, err := loadRegistrySite(configPath)
	if err != nil {
		return err
	}
	prepared, release, cleanup, err := unpackCurrentBundle(ctx, archive, policy)
	if err != nil {
		return err
	}
	defer cleanup()
	if !slices.Contains(release.Manifest.Prerequisites, bundle.RegistryPrerequisite) {
		return errors.New("INSTALL_REGISTRY_BUNDLE_REQUIRED: use a signed release containing the site Registry")
	}
	var image wire.Asset
	var seeds []registrySeed
	defer func() {
		for _, seed := range seeds {
			seed.source.Close()
		}
	}()
	for _, asset := range release.Manifest.Assets {
		if asset.Kind != "image_archive" {
			continue
		}
		if asset.ID == bundle.RegistryImageAsset {
			image = asset
			continue
		}
		if !registryRepoPattern.MatchString(asset.ID) {
			return errors.New("INSTALL_REGISTRY_REPOSITORY_INVALID: the release contains an image ID unsuitable for an OCI repository")
		}
		source, err := bundle.OpenCurrentImageSource(ctx, policy, filepath.Join(prepared, filepath.FromSlash(asset.Path)), asset, policy.MaximumTotalBytes)
		if err != nil {
			return fmt.Errorf("INSTALL_REGISTRY_SEED_INVALID: %s: %w", asset.ID, err)
		}
		seeds = append(seeds, registrySeed{repo: "ownsolid/release/" + asset.ID, source: source})
	}
	if err := checkLocalDocker(ctx, runner); err != nil {
		return err
	}
	binding := registryBinding{ReleaseSHA256: release.ManifestSHA256, SiteSHA256: site.digest, ImageID: image.ImageID}
	unlock, err := site.bind(binding)
	if err != nil {
		return err
	}
	defer unlock()
	err = loadBundleImage(ctx, runner, policy, prepared, image)
	if err == nil {
		docker := registryDocker{site: site, binding: binding, runner: runner}
		err = docker.provision(ctx, seeds)
	}
	c := site.installConfig()
	if auditErr := recordRegistryOperation(c, binding, err == nil); auditErr != nil {
		err = errors.Join(err, auditErr)
	}
	if err != nil {
		return err
	}
	images := make([]string, 0, len(seeds))
	for _, seed := range seeds {
		images = append(images, site.addr.String()+"/"+seed.repo+"@"+seed.source.Inventory().ManifestDigest)
	}
	return json.NewEncoder(out).Encode(struct {
		Registry string   `json:"registry"`
		Release  string   `json:"release"`
		Images   []string `json:"images"`
	}{site.addr.String(), release.ReleaseID, images})
}

func (s registrySite) bind(binding registryBinding) (func(), error) {
	c := s.installConfig()
	invalid := errors.New("INSTALL_REGISTRY_ROOT_INVALID: use a dedicated private Registry root with a real parent directory")
	parent := filepath.Dir(c.InstallRoot)
	if resolved, err := filepath.EvalSymlinks(parent); err != nil || resolved != parent {
		return nil, invalid
	}
	parentInfo, err := os.Stat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode().Perm()&0o022 != 0 || int(parentInfo.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return nil, invalid
	}
	if err := os.Mkdir(c.InstallRoot, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, invalid
	}
	info, err := os.Lstat(c.InstallRoot)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 || int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() {
		return nil, invalid
	}
	if _, err := os.Lstat(filepath.Join(c.InstallRoot, registryBindingFile)); errors.Is(err, os.ErrNotExist) {
		if err := checkEmptyRegistryRoot(c); err != nil {
			return nil, err
		}
	}
	unlock, err := lockRestoreOperation(c)
	if err != nil {
		return nil, err
	}
	if err := bindRegistryRoot(c, binding); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

func bindRegistryRoot(c Config, binding registryBinding) error {
	path := filepath.Join(c.InstallRoot, registryBindingFile)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if err := checkEmptyRegistryRoot(c); err != nil {
			return err
		}
		return writeReleaseRecord(c, registryBindingFile, binding)
	}
	var prior registryBinding
	raw, err := bundle.ReadProtectedConfiguration(path)
	if err != nil || wire.StrictJSON(raw, &prior) != nil || prior != binding {
		return errors.New("INSTALL_REGISTRY_BINDING_CHANGED: use the Registry root with its original signed release and site configuration")
	}
	return nil
}

func checkEmptyRegistryRoot(c Config) error {
	entries, err := os.ReadDir(c.InstallRoot)
	if err != nil || len(entries) > 1 || len(entries) == 1 && entries[0].Name() != operationLockName {
		return errors.New("INSTALL_REGISTRY_ROOT_OCCUPIED: choose an empty directory for the Registry")
	}
	return nil
}

func recordRegistryOperation(c Config, binding registryBinding, ok bool) error {
	status := "failed"
	if ok {
		status = "verified"
	}
	record, err := json.Marshal(map[string]string{
		"time": time.Now().UTC().Format(time.RFC3339), "action": "registry-bundle",
		"outcome": status, "manifest_sha256": binding.ReleaseSHA256,
	})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(c.InstallRoot, operationLogName), os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return errors.New("INSTALL_REGISTRY_AUDIT_FAILED: cannot open the local Registry operations log")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return errors.New("INSTALL_REGISTRY_AUDIT_FAILED: use a regular local Registry operations log")
	}
	_, err = f.Write(append(record, '\n'))
	if errors.Join(err, f.Sync(), f.Close()) != nil {
		return errors.New("INSTALL_REGISTRY_AUDIT_FAILED: cannot record the local Registry operation")
	}
	return nil
}

func (s registrySite) writeRuntime(writer registryCredential) error {
	c := s.installConfig()
	config, data := s.materialPaths()
	for _, path := range []string{config, data} {
		if err := privateDirectory(path, c, true); err != nil {
			return err
		}
	}
	for _, file := range []struct {
		name string
		raw  []byte
	}{
		{"tls.crt", s.cert}, {"tls.key", s.key},
	} {
		if err := writeFile(filepath.Join(config, file.name), file.raw, c, true); err != nil {
			return err
		}
	}
	for _, role := range []string{"seed", "reader"} {
		listen, credential := s.loopback(), writer
		if role == "reader" {
			listen, credential = s.addr.String(), s.auth
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(credential.Password), bcrypt.DefaultCost)
		if err != nil {
			return errors.New("INSTALL_REGISTRY_CREDENTIAL_INVALID: cannot create the Registry credential")
		}
		htpasswd := role + "-htpasswd"
		line := []byte(credential.Username + ":" + string(hash) + "\n")
		if err := writeFile(filepath.Join(config, htpasswd), line, c, true); err != nil {
			return err
		}
		settings := map[string]any{
			"version": "0.1",
			"log":     map[string]string{"level": "error"},
			"storage": map[string]any{
				"filesystem": map[string]string{"rootdirectory": "/var/lib/registry"},
				"maintenance": map[string]any{
					"readonly":      map[string]bool{"enabled": role == "reader"},
					"uploadpurging": map[string]bool{"enabled": false},
				},
			},
			"http": map[string]any{"addr": listen, "relativeurls": true, "tls": map[string]string{
				"certificate": "/etc/ownsolid-registry/tls.crt", "key": "/etc/ownsolid-registry/tls.key", "minimumtls": "tls1.2",
			}},
			"auth": map[string]any{"htpasswd": map[string]string{"realm": "ownsolid site Registry", "path": "/etc/ownsolid-registry/" + htpasswd}},
		}
		raw, err := yaml.Marshal(settings)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(config, role+".yaml"), raw, c, true); err != nil {
			return err
		}
	}
	return syncDirectory(config)
}

func transferRegistrySeeds(ctx context.Context, client registryClient, seeds []registrySeed, upload bool) error {
	defer client.client.CloseIdleConnections()
	if err := client.ready(ctx); err != nil {
		return err
	}
	for _, seed := range seeds {
		for _, b := range seed.source.RuntimeBlobs() {
			if upload {
				input, err := seed.source.OpenRuntimeBlob(b.Digest)
				if err != nil {
					return err
				}
				if err := client.upload(ctx, seed.repo, b, input); err != nil {
					return fmt.Errorf("%s: %w", seed.repo, err)
				}
			}
			if err := client.verify(ctx, seed.repo, b); err != nil {
				return fmt.Errorf("%s: %w", seed.repo, err)
			}
		}
		if err := seed.source.CheckUnchanged(); err != nil {
			return err
		}
	}
	return nil
}

type registryDocker struct {
	site    registrySite
	binding registryBinding
	runner  Runner
}

type registryContainer struct {
	ID     string `json:"Id"`
	Image  string
	State  struct{ Running bool }
	Config struct{ Labels map[string]string }
}

func (d registryDocker) provision(ctx context.Context, seeds []registrySeed) error {
	seed, err := d.find(ctx, "seed")
	if err != nil {
		return err
	}
	if err := d.remove(ctx, seed.ID); err != nil {
		return err
	}
	reader, err := d.find(ctx, "reader")
	if err != nil {
		return err
	}
	if reader.ID != "" {
		if !reader.State.Running {
			if _, err := d.runner.Run(ctx, "docker", "container", "start", reader.ID); err != nil {
				return errors.New("INSTALL_REGISTRY_START_FAILED: cannot start the bound read-only Registry container")
			}
		}
		return transferRegistrySeeds(ctx, d.site.client(d.site.addr.String()), seeds, false)
	}
	random, err := randomBytes(32)
	if err != nil {
		return err
	}
	writer := registryCredential{Username: "seed", Password: base64.RawURLEncoding.EncodeToString(random)}
	if err := d.site.writeRuntime(writer); err != nil {
		return err
	}
	if err := d.seed(ctx, seeds, writer); err != nil {
		return err
	}
	id, err := d.start(ctx, "reader")
	if err != nil {
		return err
	}
	if err := transferRegistrySeeds(ctx, d.site.client(d.site.addr.String()), seeds, false); err != nil {
		return errors.Join(err, d.removeAfterAttempt(ctx, id))
	}
	return nil
}

func (d registryDocker) seed(ctx context.Context, seeds []registrySeed, credential registryCredential) (err error) {
	id, err := d.start(ctx, "seed")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, d.removeAfterAttempt(ctx, id)) }()
	client := d.site.client(d.site.loopback())
	client.auth = credential
	return transferRegistrySeeds(ctx, client, seeds, true)
}

func (d registryDocker) name(role string) string {
	if role == "reader" {
		return d.site.config.Name
	}
	return d.site.config.Name + "-seed"
}

func (d registryDocker) labels(role string) [3]string {
	return [3]string{
		"ownsolid.registry.root=" + d.site.config.Root,
		"ownsolid.registry.release=" + d.binding.ReleaseSHA256,
		"ownsolid.registry.role=" + role,
	}
}

func (d registryDocker) find(ctx context.Context, role string) (registryContainer, error) {
	var c registryContainer
	raw, err := d.runner.Run(ctx, "docker", "container", "ls", "--all", "--no-trunc", "--filter", "name=^/"+d.name(role)+"$", "--format", "{{.ID}}")
	if err != nil {
		return c, errors.New("INSTALL_REGISTRY_DOCKER_UNAVAILABLE: check the local Docker daemon")
	}
	id := strings.TrimSpace(raw)
	if id == "" {
		return c, nil
	}
	if !wire.ValidSHA256(id) {
		return c, errors.New("INSTALL_REGISTRY_CONTAINER_CONFLICT: cannot identify the existing Registry container")
	}
	raw, err = d.runner.Run(ctx, "docker", "container", "inspect", "--format", "{{json .}}", id)
	if err != nil || json.Unmarshal([]byte(raw), &c) != nil || c.ID != id || c.Image != d.binding.ImageID {
		return c, errors.New("INSTALL_REGISTRY_CONTAINER_CONFLICT: choose a name reserved for this signed Registry")
	}
	for _, label := range d.labels(role) {
		key, value, _ := strings.Cut(label, "=")
		if c.Config.Labels[key] != value {
			return c, errors.New("INSTALL_REGISTRY_CONTAINER_CONFLICT: choose a name reserved for this signed Registry")
		}
	}
	return c, nil
}

func (d registryDocker) start(ctx context.Context, role string) (string, error) {
	s := d.site.config
	config, data := d.site.materialPaths()
	restart, mode := "no", ""
	if role == "reader" {
		restart, mode = "unless-stopped", ",readonly"
	}
	args := []string{"container", "run", "--detach", "--pull=never", "--name", d.name(role), "--network=host",
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--restart=" + restart,
		"--user", strconv.Itoa(s.UID) + ":" + strconv.Itoa(s.GID),
		"--mount", "type=bind,src=" + config + ",dst=/etc/ownsolid-registry,readonly",
		"--mount", "type=bind,src=" + data + ",dst=/var/lib/registry" + mode,
		"--env", "OTEL_TRACES_EXPORTER=none"}
	for _, label := range d.labels(role) {
		args = append(args, "--label", label)
	}
	args = append(args, "--entrypoint", "/bin/registry", d.binding.ImageID, "serve", "/etc/ownsolid-registry/"+role+".yaml")
	raw, err := d.runner.Run(ctx, "docker", args...)
	id := strings.TrimSpace(raw)
	if err != nil {
		return "", fmt.Errorf("INSTALL_REGISTRY_START_FAILED: Docker refused the %s container: %s", role, lastLines(raw))
	}
	if !wire.ValidSHA256(id) {
		return "", errors.New("INSTALL_REGISTRY_START_FAILED: Docker did not return a container ID")
	}
	return id, nil
}

func (d registryDocker) removeAfterAttempt(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
	defer cancel()
	return d.remove(ctx, id)
}

func (d registryDocker) remove(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if _, err := d.runner.Run(ctx, "docker", "container", "rm", "--force", "--volumes", id); err != nil {
		return errors.New("INSTALL_REGISTRY_CLEANUP_FAILED: the temporary Registry container could not be removed; retry registry-bundle")
	}
	return nil
}
