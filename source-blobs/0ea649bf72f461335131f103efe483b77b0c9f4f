package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"time"
)

const maxCatalogComposeExecutable = 128 << 20

var (
	errCatalogRuntime = errors.New("INSTALL_CATALOG_COMPOSE_RUNTIME_INVALID: configure a pinned executable, private workspace, project, Unix socket and timeout")
	errCatalogRender  = errors.New("INSTALL_CATALOG_COMPOSE_RENDER_FAILED: check the pinned renderer and the signed source")
	errCatalogLaunch  = errors.New("INSTALL_CATALOG_COMPOSE_LAUNCH_FAILED: keep the install unsettled and inspect the local project")
)

// CatalogComposePolicy comes from the authorized install operation and local host
// configuration. The publisher and install form do not select these values.
type CatalogComposePolicy struct {
	Executable       string
	ExecutableSHA256 string
	Workspace        string
	DockerSocket     string
	Project          string
	Timeout          time.Duration
}

// CatalogComposeBinding carries the source and model digests from the scoped,
// audited admission record. It must be paired with the catalog's verified release
// pin and image admission before startup.
type CatalogComposeBinding struct {
	ArtifactSHA256 string
	ModelSHA256    string
}

// StagedCatalogCompose retains the one source identity, exact rendered model
// and execution context for a scoped install. The caller keeps its source root
// open and serializes Start and Close under the installation operation.
type StagedCatalogCompose struct {
	policy CatalogComposePolicy
	root   *os.Root
	source os.FileInfo
	tool   *os.File
	work   string
	model  []byte
	digest string
	run    func(*exec.Cmd) error
}

// StageCatalogCompose verifies the source against the signed artifact digest
// and renders it in a private context with no engine socket.
func StageCatalogCompose(ctx context.Context, root *os.Root, artifactSHA256 string, policy CatalogComposePolicy) (*StagedCatalogCompose, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil, errCatalogRuntime
	}
	return stageCatalogCompose(ctx, root, artifactSHA256, policy, (*exec.Cmd).Run)
}

func stageCatalogCompose(ctx context.Context, root *os.Root, digest string, policy CatalogComposePolicy, run func(*exec.Cmd) error) (*StagedCatalogCompose, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if root == nil || !catalogDigest(digest) {
		return nil, errCatalogSource
	}
	parent, err := root.Stat(".")
	if err != nil || !catalogPrivateDirectory(parent) {
		return nil, errCatalogSource
	}
	sourceBytes, sourceInfo, err := readCatalogSource(root, digest)
	if err != nil {
		return nil, err
	}
	source, err := readCatalogComposeSource(sourceBytes)
	if err != nil {
		return nil, err
	}
	if !catalogProjectName.MatchString(policy.Project) || policy.Timeout <= 0 ||
		!canonicalPath(policy.Executable) || !catalogDigest(policy.ExecutableSHA256) ||
		!canonicalPath(policy.DockerSocket) || !canonicalPath(policy.Workspace) {
		return nil, errCatalogRuntime
	}
	workspace, err := os.Lstat(policy.Workspace)
	if err != nil || !catalogPrivateDirectory(workspace) {
		return nil, errCatalogRuntime
	}
	if real, err := filepath.EvalSymlinks(policy.Workspace); err != nil || real != policy.Workspace {
		return nil, errCatalogRuntime
	}
	tool, err := os.OpenFile(policy.Executable, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errCatalogRuntime
	}
	work, err := os.MkdirTemp(policy.Workspace, ".catalog-compose-")
	if err != nil {
		tool.Close()
		return nil, errCatalogRuntime
	}
	stage := &StagedCatalogCompose{policy: policy, root: root, source: sourceInfo, tool: tool, work: work, digest: digest, run: run}
	if err := stage.render(ctx, sourceBytes, source); err != nil {
		_ = stage.Close()
		return nil, err
	}
	return stage, nil
}

// Rendered returns a copy of the private model for catalog image admission.
func (s *StagedCatalogCompose) Rendered() []byte {
	return bytes.Clone(s.model)
}

// Start rechecks the staged identities and passes only the admitted JSON to
// Compose. A nil result confirms the launcher returned success to the caller.
func (s *StagedCatalogCompose) Start(ctx context.Context, expected CatalogComposeBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.tool == nil || expected.ArtifactSHA256 != s.digest || expected.ModelSHA256 != catalogSHA256(s.model) {
		return errCatalogModel
	}
	if _, info, err := readCatalogSource(s.root, s.digest); err != nil || !os.SameFile(s.source, info) {
		return errCatalogSource
	}
	socket, err := os.Lstat(s.policy.DockerSocket)
	if err != nil || socket.Mode()&os.ModeSocket == 0 {
		return errCatalogRuntime
	}
	if actual, err := filepath.EvalSymlinks(s.policy.DockerSocket); err != nil || actual != s.policy.DockerSocket {
		return errCatalogRuntime
	}
	err = s.compose(ctx, s.model, s.policy.DockerSocket, io.Discard, "up", "--detach", "--pull", "never", "--no-build")
	return catalogComposeCommandError(err, errCatalogLaunch)
}

// Close releases the pinned executable and removes this operation's workspace.
func (s *StagedCatalogCompose) Close() error {
	if s.tool == nil {
		return nil
	}
	err := errors.Join(s.tool.Close(), os.RemoveAll(s.work))
	s.tool = nil
	s.model = nil
	return err
}

func catalogPrivateDirectory(info os.FileInfo) bool {
	return info.IsDir() && info.Mode().Perm() == 0o700 && catalogOwned(info, false)
}

func catalogOwned(info os.FileInfo, allowRoot bool) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == uint32(os.Geteuid()) || allowRoot && stat.Uid == 0)
}

func readCatalogSource(root *os.Root, expected string) ([]byte, os.FileInfo, error) {
	before, err := root.Lstat(catalogComposeArtifact)
	if err != nil || !before.Mode().IsRegular() || !catalogOwned(before, false) ||
		before.Mode().Perm()&0o022 != 0 || before.Size() < 1 || before.Size() > maxCatalogComposeBytes ||
		before.Sys().(*syscall.Stat_t).Nlink != 1 {
		return nil, nil, errCatalogSource
	}
	file, err := root.OpenFile(catalogComposeArtifact, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, errCatalogSource
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, nil, errCatalogSource
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCatalogComposeBytes+1))
	if err != nil || int64(len(data)) != before.Size() || catalogSHA256(data) != expected {
		return nil, nil, errCatalogSource
	}
	after, err := root.Lstat(catalogComposeArtifact)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() ||
		before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, nil, errCatalogSource
	}
	return data, after, nil
}

func (s *StagedCatalogCompose) render(ctx context.Context, raw []byte, source catalogComposeModel) error {
	var output catalogComposeOutput
	if err := s.compose(ctx, raw, filepath.Join(s.work, "no-engine.sock"), &output,
		"config", "--format", "json", "--no-interpolate", "--no-env-resolution"); err != nil {
		return catalogComposeCommandError(err, errCatalogRender)
	}
	var model catalogComposeModel
	if output.overflow || json.Unmarshal(output.buffer.Bytes(), &model, json.RejectUnknownMembers(true)) != nil || model.Name != s.policy.Project ||
		!validCatalogServices(model.Services) || !reflect.DeepEqual(model.Services, source.Services) {
		return errCatalogModel
	}
	s.model = bytes.Clone(output.buffer.Bytes())
	return nil
}

type catalogComposeOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *catalogComposeOutput) Write(p []byte) (int, error) {
	if len(p) > maxCatalogComposeBytes-b.buffer.Len() {
		b.overflow = true
		return 0, errCatalogRender
	}
	return b.buffer.Write(p)
}

func (s *StagedCatalogCompose) compose(ctx context.Context, input []byte, socket string, output io.Writer, action ...string) error {
	ctx, cancel := context.WithTimeout(ctx, s.policy.Timeout)
	defer cancel()
	if !s.toolMatches() {
		return errCatalogRuntime
	}
	contextDir, err := os.MkdirTemp(s.work, "context-")
	if err != nil {
		return errCatalogRuntime
	}
	defer os.RemoveAll(contextDir)
	args := []string{"--ansi", "never", "--project-name", s.policy.Project, "--project-directory", s.work, "--env-file", os.DevNull, "-f", "-"}
	// The held descriptor is inherited as fd 3. Renaming the configured executable
	// cannot redirect either invocation to a different file.
	command := exec.CommandContext(ctx, "/proc/self/fd/3", append(args, action...)...)
	command.Args[0] = s.policy.Executable
	command.ExtraFiles = []*os.File{s.tool}
	command.Dir = s.work
	command.Env = []string{"HOME=" + contextDir, "DOCKER_CONFIG=" + contextDir, "DOCKER_HOST=unix://" + socket, "COMPOSE_DISABLE_ENV_FILE=1"}
	command.Stdin, command.Stdout = bytes.NewReader(input), output
	if err := ctx.Err(); err != nil {
		return err
	}
	err = s.run(command)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func catalogComposeCommandError(err, failure error) error {
	switch {
	case err == nil || errors.Is(err, errCatalogRuntime):
		return err
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w (%w)", failure, err)
	default:
		return failure
	}
}

func (s *StagedCatalogCompose) toolMatches() bool {
	info, err := s.tool.Stat()
	if err != nil || !info.Mode().IsRegular() || !catalogOwned(info, true) || info.Mode().Perm()&0o022 != 0 ||
		info.Mode().Perm()&0o111 == 0 || info.Size() < 1 || info.Size() > maxCatalogComposeExecutable {
		return false
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.NewSectionReader(s.tool, 0, info.Size()))
	after, statErr := s.tool.Stat()
	return err == nil && statErr == nil && n == info.Size() && after.Size() == info.Size() &&
		info.ModTime().Equal(after.ModTime()) && "sha256:"+hex.EncodeToString(hash.Sum(nil)) == s.policy.ExecutableSHA256
}
