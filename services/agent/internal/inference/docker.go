// Package inference executes only the fixed approved offline model deployment recipe.
package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

func fail(value string) error { return errors.New(value) }

const engineAPI = "/v1.45"

type Docker struct {
	runtime   *settings.Policy
	client    *http.Client
	transport *http.Transport
	guard     egressGuard
}

func NewDocker(socket string, policies ...settings.Policy) (*Docker, error) {
	policy := settings.Default()
	if len(policies) > 0 {
		policy = policies[0]
	}
	if e := policy.Validate(); e != nil {
		return nil, e
	}
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || strings.ContainsAny(socket, "\x00\r\n") {
		return nil, fail("INFERENCE_DOCKER_UNAVAILABLE")
	}
	info, e := os.Stat(socket)
	if e != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, fail("INFERENCE_DOCKER_UNAVAILABLE")
	}
	transport := &http.Transport{Proxy: nil, MaxConnsPerHost: policy.DockerConnections, MaxIdleConns: policy.DockerConnections, MaxIdleConnsPerHost: policy.DockerConnections, IdleConnTimeout: settings.Seconds(policy.IdleConnectionSeconds), ResponseHeaderTimeout: settings.Seconds(policy.DockerHeaderSeconds), DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: settings.Seconds(policy.DockerDialSeconds)}).DialContext(ctx, "unix", socket)
	}}
	return &Docker{runtime: &policy, client: &http.Client{Transport: transport, Timeout: settings.Seconds(policy.DockerSeconds), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport: transport, guard: newEgressGuard()}, nil
}
func (d *Docker) Close() { d.transport.CloseIdleConnections() }
func (d *Docker) request(ctx context.Context, method, path string, input, output any, want int) error {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n\x00") {
		return fail("INFERENCE_DOCKER_REJECTED")
	}
	var body []byte
	if input != nil {
		var e error
		body, e = json.Marshal(input)
		if e != nil || len(body) > 32768 {
			return fail("INFERENCE_DOCKER_REJECTED")
		}
	}
	request, e := http.NewRequestWithContext(ctx, method, "http://docker"+engineAPI+path, bytes.NewReader(body))
	if e != nil {
		return fail("INFERENCE_DOCKER_REJECTED")
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, e := d.client.Do(request)
	if e != nil {
		return fail("INFERENCE_DOCKER_UNAVAILABLE")
	}
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, 262145))
	if e != nil || len(data) > 262144 {
		return fail("INFERENCE_DOCKER_REJECTED")
	}
	if response.StatusCode != want {
		if response.StatusCode == 404 {
			return fail("INFERENCE_DOCKER_NOT_FOUND")
		}
		if response.StatusCode == 409 {
			return fail("INFERENCE_CONTAINER_CONFLICT")
		}
		return fail("INFERENCE_DOCKER_REJECTED")
	}
	if output != nil && json.Unmarshal(data, output) != nil {
		return fail("INFERENCE_DOCKER_REJECTED")
	}
	return nil
}

type Image struct {
	ID           string `json:"Id"`
	Architecture string
	OS           string `json:"Os"`
	Config       struct{ Env []string }
}

func (d *Docker) InspectApprovedImage(ctx context.Context) (Image, error) {
	var image Image
	recipe := wire.ApprovedInferenceRecipe()
	var e error
	for _, reference := range []string{recipe.ImageRepository + "@" + recipe.ImageManifestDigest, recipe.ImageConfigDigest, recipe.ImageManifestDigest, "projectone-frozen/vllm:" + recipe.Version + "-amd64"} {
		e = d.request(ctx, "GET", "/images/"+url.PathEscape(reference)+"/json", nil, &image, 200)
		if e == nil {
			break
		}
		if e.Error() != "INFERENCE_DOCKER_NOT_FOUND" {
			return image, fail("INFERENCE_IMAGE_UNAVAILABLE")
		}
	}
	if e != nil {
		return image, fail(wire.InferenceImageMissing)
	}
	if (image.ID != recipe.ImageConfigDigest && image.ID != recipe.ImageManifestDigest) || image.Architecture != recipe.Architecture || image.OS != recipe.OS {
		return image, fail("INFERENCE_IMAGE_INVALID")
	}
	actualCUDA := ""
	for _, value := range image.Config.Env {
		if strings.HasPrefix(value, "CUDA_VERSION=") {
			actualCUDA = strings.TrimPrefix(value, "CUDA_VERSION=")
		}
	}
	if actualCUDA != recipe.CUDAVersion {
		return image, fail("INFERENCE_IMAGE_INVALID")
	}
	return image, nil
}

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var deploymentID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Container struct {
	ID    string `json:"Id"`
	Name  string
	Image string
	State struct {
		Running   bool
		Status    string
		ExitCode  int
		OOMKilled bool
		StartedAt string
	}
	Config struct {
		Image      string
		User       string
		Labels     map[string]string
		Cmd        []string
		Entrypoint []string
		Env        []string
		WorkingDir string
	}
	HostConfig      HostConfig
	Mounts          []Mount
	NetworkSettings struct {
		Networks map[string]struct{ NetworkID string }
	}
}
type Mount struct {
	Type        string
	Source      string
	Destination string
	RW          bool
}

func (d *Docker) InspectContainer(ctx context.Context, id string) (Container, error) {
	var value Container
	if !containerID.MatchString(id) && !regexp.MustCompile(`^projectone-vllm-[a-f0-9-]{36}$`).MatchString(id) {
		return value, fail("INFERENCE_CONTAINER_CONFLICT")
	}
	e := d.request(ctx, "GET", "/containers/"+id+"/json", nil, &value, 200)
	if e == nil && !containerID.MatchString(value.ID) {
		return value, fail("INFERENCE_DOCKER_REJECTED")
	}
	return value, e
}
func (d *Docker) CreateContainer(ctx context.Context, deployment, node, gpu, modelPath, cachePath, network string) (string, error) {
	configuration, e := ContainerConfiguration(deployment, node, gpu, modelPath, cachePath, network)
	if e != nil {
		return "", e
	}
	return d.createTypedContainer(ctx, deployment, configuration)
}
func (d *Docker) CreateDerivedContainer(ctx context.Context, c wire.InferenceCommand, node, modelPath, cachePath, network string) (string, error) {
	configuration, e := DerivedContainerConfiguration(c, node, modelPath, cachePath, network)
	if e != nil {
		return "", e
	}
	return d.createTypedContainer(ctx, c.DeploymentID, configuration)
}
func (d *Docker) createTypedContainer(ctx context.Context, deployment string, configuration ContainerConfig) (string, error) {
	image, e := d.InspectApprovedImage(ctx)
	if e != nil {
		return "", e
	}
	configuration.Image = image.ID
	var result struct {
		ID string `json:"Id"`
	}
	if e = d.request(ctx, "POST", "/containers/create?name="+url.QueryEscape("projectone-vllm-"+deployment), configuration, &result, 201); e != nil {
		return "", e
	}
	if !containerID.MatchString(result.ID) {
		return "", fail("INFERENCE_DOCKER_REJECTED")
	}
	return result.ID, nil
}
func (d *Docker) StartContainer(ctx context.Context, id string) error {
	if !containerID.MatchString(id) {
		return fail("INFERENCE_CONTAINER_CONFLICT")
	}
	return d.request(ctx, "POST", "/containers/"+id+"/start", nil, nil, 204)
}
func (d *Docker) StopContainer(ctx context.Context, id string) error {
	if !containerID.MatchString(id) {
		return fail("INFERENCE_CONTAINER_CONFLICT")
	}
	return d.request(ctx, "POST", "/containers/"+id+"/stop?t="+strconv.Itoa(settings.Resolve(d.runtime).DockerStopSeconds), nil, nil, 204)
}
func (d *Docker) RemoveContainer(ctx context.Context, id string) error {
	if !containerID.MatchString(id) {
		return fail("INFERENCE_CONTAINER_CONFLICT")
	}
	return d.request(ctx, "DELETE", "/containers/"+id+"?force=false&v=false", nil, nil, 204)
}
