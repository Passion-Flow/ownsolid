// Package imageregistry loads actual verified tagless OCI archives into the
// existing protected Docker owner. Image availability never proves GPU execution.
package imageregistry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/client"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/state"
	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

type Config struct {
	Root                       string              `json:"root"`
	DockerDataRoot             string              `json:"docker_data_root"`
	Verification               bundle.Verification `json:"verification"`
	MaximumArchiveBytes        int64               `json:"maximum_archive_bytes"`
	MaximumExpandedBytes       int64               `json:"maximum_expanded_bytes"`
	MaximumDockerResponseBytes int64               `json:"maximum_docker_response_bytes"`
	OperationSeconds           int                 `json:"operation_seconds"`
	MinimumHeadroomPercent     int                 `json:"minimum_headroom_percent"`
}

func Defaults() Config {
	return Config{Verification: bundle.Defaults(), MaximumArchiveBytes: 64 << 30, MaximumExpandedBytes: 64 << 30, MaximumDockerResponseBytes: 1 << 20, OperationSeconds: 3600, MinimumHeadroomPercent: 30}
}
func (c Config) Validate() error {
	for _, name := range []string{c.Root, c.DockerDataRoot, c.Verification.MountRoot, c.Verification.TrustFile} {
		if !filepath.IsAbs(name) || filepath.Clean(name) != name || strings.ContainsAny(name, "\x00\r\n") {
			return errors.New("REGISTRY_CONFIGURATION_INVALID")
		}
	}

	if !filepath.IsAbs(c.DockerDataRoot) || filepath.Clean(c.DockerDataRoot) != c.DockerDataRoot || c.MaximumArchiveBytes > 1<<50 || c.MaximumExpandedBytes > 1<<50 || !filepath.IsAbs(c.Root) || filepath.Clean(c.Root) != c.Root || c.Verification.Validate() != nil || c.Verification.AllowTestFixture || c.MaximumArchiveBytes < 1 || c.MaximumExpandedBytes < 1 || c.MaximumDockerResponseBytes < 1 || c.MaximumDockerResponseBytes > c.Verification.MaximumMetadataBytes || c.OperationSeconds < 1 || c.OperationSeconds > 86400 || c.MinimumHeadroomPercent < 30 || c.MinimumHeadroomPercent > 90 {
		return errors.New("REGISTRY_CONFIGURATION_INVALID")
	}
	rel, e := filepath.Rel(c.Verification.MountRoot, c.Root)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("REGISTRY_CONFIGURATION_INVALID")
	}
	return nil
}
func Load(filename string) (Config, error) {
	raw, e := bundle.ReadProtectedConfiguration(filename)
	if e != nil {
		return Config{}, e
	}
	c := Defaults()
	if release.StrictJSON(raw, &c) != nil || c.Validate() != nil {
		return Config{}, errors.New("REGISTRY_CONFIGURATION_INVALID")
	}
	return c, nil
}

type Loader struct {
	config  Config
	control *client.Client
	docker  *inference.Docker
}

func New(c Config, control *client.Client, docker *inference.Docker) (*Loader, error) {
	if c.Validate() != nil || docker == nil {
		return nil, errors.New("REGISTRY_CONFIGURATION_INVALID")
	}
	if state.Directory(c.Root) != nil {
		return nil, errors.New("REGISTRY_STORE_INVALID")
	}
	return &Loader{c, control, docker}, nil
}
func privateWrite(root *os.Root, name string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil {
		return e
	}
	f, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	dir, e := root.Open(filepath.Dir(name))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func (l *Loader) admit(m wire.RegistryImageIdentity) error {
	if m.Validate() != nil || m.ArchiveBytes > l.config.MaximumArchiveBytes || m.ExpandedBytes > l.config.MaximumExpandedBytes {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	var fs syscall.Statfs_t
	if syscall.Statfs(l.config.Root, &fs) != nil || fs.Blocks == 0 {
		return errors.New("REGISTRY_STORE_INVALID")
	}
	available := uint64(fs.Bavail) * uint64(fs.Bsize)
	capacity := uint64(fs.Blocks) * uint64(fs.Bsize)
	need := uint64(m.ArchiveBytes)*2 + uint64(m.ExpandedBytes)
	if available < need || available-need < capacity/100*uint64(l.config.MinimumHeadroomPercent) {
		return errors.New("REGISTRY_STORAGE_CAPACITY_REQUIRED")
	}
	return nil
}
func (l *Loader) EnsureTask(ctx context.Context, node, task, lease string, command wire.InferenceCommand) (wire.RegistryManifest, error) {
	if l.control == nil {
		return wire.RegistryManifest{}, errors.New("REGISTRY_CONTROL_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(l.config.OperationSeconds)*time.Second)
	defer cancel()
	path := "/agent/v1/nodes/" + node + "/tasks/" + task + "/image-registry"
	m, e := l.control.RegistryManifest(ctx, path, lease, command)
	if e != nil {
		return wire.RegistryManifest{}, e
	}
	if e = l.verifyProof(ctx, m.ImageIdentity()); e != nil {
		return wire.RegistryManifest{}, e
	}
	root, e := os.OpenRoot(l.config.Root)
	if e != nil {
		return wire.RegistryManifest{}, e
	}
	defer root.Close()
	job := "task-" + task
	if _, e = root.Lstat(job); e == nil {
		original, readErr := l.taskIntent(root, job)
		if readErr != nil || !sameTaskImageIntent(original, m) {
			return wire.RegistryManifest{}, errors.New("REGISTRY_INTENT_STALE")
		}
		if e = l.reconcileImage(ctx, root, job, m.ImageIdentity(), false); e != nil {
			return wire.RegistryManifest{}, e
		}
		return l.finalTaskAuthority(ctx, path, lease, command, m)
	}
	if !os.IsNotExist(e) {
		return wire.RegistryManifest{}, e
	}
	if e = l.admit(m.ImageIdentity()); e != nil {
		return wire.RegistryManifest{}, e
	}
	if e = createPrivateJob(root, job); e != nil {
		return wire.RegistryManifest{}, e
	}
	if e = privateWrite(root, job+"/intent.json", m); e != nil {
		return wire.RegistryManifest{}, e
	}
	f, e := root.OpenFile(job+"/archive.tar", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return wire.RegistryManifest{}, e
	}
	e = l.control.RegistryArchive(ctx, path, lease, m, f)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return wire.RegistryManifest{}, e
	}
	if closeErr != nil {
		return wire.RegistryManifest{}, closeErr
	}
	current, e := l.finalTaskAuthority(ctx, path, lease, command, m)
	if e != nil {
		return wire.RegistryManifest{}, errors.New("REGISTRY_GRANT_STALE")
	}
	m = current
	if e = l.load(ctx, root, job, m.ImageIdentity()); e != nil {
		return wire.RegistryManifest{}, e
	}
	return l.finalTaskAuthority(ctx, path, lease, command, m)
}
func (l *Loader) load(ctx context.Context, root *os.Root, job string, m wire.RegistryImageIdentity) error {
	if e := l.verifyProof(ctx, m); e != nil {
		return e
	}
	name := filepath.Join(l.config.Root, job, "archive.tar")
	asset := release.Asset{ID: "registry-archive", Kind: "image_archive", Path: "archive.tar", Size: m.ArchiveBytes, SHA256: m.ArchiveSHA256, ImageID: m.ImageManifestDigest, ImageConfigDigest: m.ImageConfigDigest, Architecture: m.Architecture}
	source, e := bundle.OpenImageSource(ctx, l.config.Verification, name, asset, l.config.MaximumExpandedBytes)
	if e != nil {
		return e
	}
	defer source.Close()
	inventory := source.Inventory()
	if inventory.ManifestDigest != m.ImageManifestDigest || inventory.ConfigDigest != m.ImageConfigDigest || inventory.OS != m.OS || inventory.Architecture != m.Architecture || inventory.ExpandedBytes != m.ExpandedBytes {
		return errors.New("REGISTRY_IMAGE_INVALID")
	}
	transport, e := root.OpenFile(job+"/load.tar", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	receipt, e := source.WriteTagless(ctx, transport)
	if e == nil {
		e = transport.Sync()
	}
	ce := transport.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if e = privateWrite(root, job+"/load.started.json", receipt); e != nil {
		return e
	}
	f, e := root.Open(job + "/load.tar")
	if e != nil {
		return e
	}
	defer f.Close()
	image, e := l.docker.LoadRegistryArchive(ctx, f, receipt.TransportBytes, inference.RegistryLoadPolicy{MaximumResponseBytes: l.config.MaximumDockerResponseBytes, ExpectedDockerDataRoot: l.config.DockerDataRoot, RequiredBytes: receipt.TransportBytes + m.ExpandedBytes, MinimumHeadroomPercent: l.config.MinimumHeadroomPercent})
	if e != nil {
		return e
	}
	return privateWrite(root, job+"/loaded.json", image)
}
func (l *Loader) ReconcileTask(ctx context.Context, node, task, lease string, command wire.InferenceCommand) (wire.RegistryManifest, error) {
	if l.control == nil {
		return wire.RegistryManifest{}, errors.New("REGISTRY_CONTROL_REQUIRED")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(l.config.OperationSeconds)*time.Second)
	defer cancel()
	path := "/agent/v1/nodes/" + node + "/tasks/" + task + "/image-registry"
	m, e := l.control.RegistryManifest(ctx, path, lease, command)
	if e != nil {
		return wire.RegistryManifest{}, e
	}
	root, e := os.OpenRoot(l.config.Root)
	if e != nil {
		return wire.RegistryManifest{}, e
	}
	defer root.Close()
	job := "task-" + task
	original, e := l.taskIntent(root, job)
	if e != nil || !sameTaskImageIntent(original, m) {
		return wire.RegistryManifest{}, errors.New("REGISTRY_INTENT_STALE")
	}
	if e = l.reconcileImage(ctx, root, job, m.ImageIdentity(), true); e != nil {
		return wire.RegistryManifest{}, e
	}
	return l.finalTaskAuthority(ctx, path, lease, command, m)
}

// ImportOffline uses a current Admin-exported retained hold and protected local
// archive. It has no backend URL/credential and never grants deployment execution.
func (l *Loader) ImportOffline(ctx context.Context, m wire.RegistryOfflineManifest, filename string) error {
	if m.Validate() != nil || !filepath.IsAbs(filename) || filepath.Clean(filename) != filename {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(l.config.OperationSeconds)*time.Second)
	defer cancel()
	if e := l.verifyProof(ctx, m.Image); e != nil {
		return e
	}
	if e := l.admit(m.Image); e != nil {
		return e
	}
	asset := release.Asset{ID: "registry-archive", Kind: "image_archive", Path: "archive.tar", Size: m.Image.ArchiveBytes, SHA256: m.Image.ArchiveSHA256, ImageID: m.Image.ImageManifestDigest, ImageConfigDigest: m.Image.ImageConfigDigest, Architecture: m.Image.Architecture}
	source, e := bundle.OpenImageSource(ctx, l.config.Verification, filename, asset, l.config.MaximumExpandedBytes)
	if e != nil {
		return e
	}
	defer source.Close()
	root, e := os.OpenRoot(l.config.Root)
	if e != nil {
		return e
	}
	defer root.Close()
	job := "offline-" + m.ReferenceID
	if e = createPrivateJob(root, job); e != nil {
		return errors.New("REGISTRY_RECOVERY_REQUIRED")
	}
	if e = privateWrite(root, job+"/intent.json", m); e != nil {
		return e
	}
	f, e := root.OpenFile(job+"/archive.tar", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	receipt, e := source.WriteTagless(ctx, f)
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	if receipt.TransportSHA256 != m.Image.ArchiveSHA256 || receipt.TransportBytes != m.Image.ArchiveBytes {
		return errors.New("REGISTRY_DIGEST_MISMATCH")
	}
	return l.load(ctx, root, job, m.Image)
}
func (l *Loader) ReconcileOffline(ctx context.Context, m wire.RegistryOfflineManifest) error {
	if m.Validate() != nil {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(l.config.OperationSeconds)*time.Second)
	defer cancel()
	root, e := os.OpenRoot(l.config.Root)
	if e != nil {
		return e
	}
	defer root.Close()
	job := "offline-" + m.ReferenceID
	var original wire.RegistryOfflineManifest
	if privateJob(root, job) != nil || readPrivateJSON(root, job+"/intent.json", l.config.Verification.MaximumMetadataBytes, &original) != nil || !reflect.DeepEqual(original, m) {
		return errors.New("REGISTRY_INTENT_STALE")
	}
	return l.reconcileImage(ctx, root, job, m.Image, true)
}
func (l *Loader) verifyStarted(ctx context.Context, root *os.Root, job string, m wire.RegistryImageIdentity) error {
	if e := l.verifyProof(ctx, m); e != nil {
		return e
	}
	var receipt bundle.ImportReceipt
	if readPrivateJSON(root, job+"/load.started.json", l.config.Verification.MaximumMetadataBytes, &receipt) != nil || receipt.SourceSHA256 != m.ArchiveSHA256 || receipt.ExpandedBytes != m.ExpandedBytes || receipt.TransportBytes < 1 || receipt.TransportBytes > l.config.MaximumArchiveBytes {
		return errors.New("REGISTRY_CONTENT_INVALID")
	}
	asset := release.Asset{ID: "registry-load", Kind: "image_archive", Path: "load.tar", Size: receipt.TransportBytes, SHA256: receipt.TransportSHA256, ImageID: m.ImageManifestDigest, ImageConfigDigest: m.ImageConfigDigest, Architecture: m.Architecture}
	source, e := bundle.OpenImageSource(ctx, l.config.Verification, filepath.Join(l.config.Root, job, "load.tar"), asset, l.config.MaximumExpandedBytes)
	if e != nil {
		return e
	}
	defer source.Close()
	if source.Inventory().ExpandedBytes != m.ExpandedBytes {
		return errors.New("REGISTRY_IMAGE_INVALID")
	}
	return nil
}

func LoadEnvironment(values map[string]string) (Config, error) {
	if len(values) != 1 || values["PROJECT_ONE_AGENT_REGISTRY_CONFIG_FILE"] == "" {
		return Config{}, errors.New("REGISTRY_CONFIGURATION_INVALID")
	}
	return Load(values["PROJECT_ONE_AGENT_REGISTRY_CONFIG_FILE"])
}

func (l *Loader) verifyProof(ctx context.Context, m wire.RegistryImageIdentity) error {
	var proof bundle.ImageProof
	if release.StrictJSON(m.ReleaseProof, &proof) != nil {
		return errors.New("REGISTRY_RELEASE_PROOF_INVALID")
	}
	result, e := bundle.VerifyImageProof(ctx, l.config.Verification, proof)
	if e != nil || result.ManifestSHA256 != m.SourceManifestSHA256 || result.Asset.Kind != "image_archive" || result.Asset.ImageID != m.ImageManifestDigest || result.Asset.ImageConfigDigest != m.ImageConfigDigest || result.Asset.Architecture != m.Architecture {
		return errors.New("REGISTRY_RELEASE_PROOF_INVALID")
	}
	return nil
}
