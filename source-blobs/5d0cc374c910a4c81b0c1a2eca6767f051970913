package modelpreparations

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/model-lifecycle/v1"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/inference"
	"github.com/Passion-Flow/ownsolid/services/agent/internal/settings"
)

type Docker struct {
	client    *http.Client
	transport *http.Transport
}

func NewDocker(socket string, policy settings.Policy) (*Docker, error) {
	if !cleanPath(socket) || policy.Validate() != nil {
		return nil, fail("MODEL_PREPARATION_DOCKER_UNAVAILABLE")
	}
	info, e := os.Stat(socket)
	if e != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, fail("MODEL_PREPARATION_DOCKER_UNAVAILABLE")
	}
	transport := &http.Transport{Proxy: nil, MaxConnsPerHost: policy.DockerConnections, MaxIdleConns: policy.DockerConnections, MaxIdleConnsPerHost: policy.DockerConnections, IdleConnTimeout: settings.Seconds(policy.IdleConnectionSeconds), ResponseHeaderTimeout: settings.Seconds(policy.DockerHeaderSeconds), DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: settings.Seconds(policy.DockerDialSeconds)}).DialContext(ctx, "unix", socket)
	}}
	return &Docker{transport: transport, client: &http.Client{Transport: transport, Timeout: settings.Seconds(policy.DockerSeconds), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (d *Docker) Close() { d.transport.CloseIdleConnections() }
func (d *Docker) request(ctx context.Context, method, path string, input, output any, statuses ...int) error {
	if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\r\n\x00") {
		return fail("MODEL_PREPARATION_DOCKER_REJECTED")
	}
	var body []byte
	var e error
	if input != nil {
		body, e = json.Marshal(input)
		if e != nil || len(body) > MaximumDockerRequestBytes {
			return fail("MODEL_PREPARATION_DOCKER_REJECTED")
		}
	}
	r, e := http.NewRequestWithContext(ctx, method, "http://docker"+EngineAPI+path, bytes.NewReader(body))
	if e != nil {
		return fail("MODEL_PREPARATION_DOCKER_REJECTED")
	}
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	response, e := d.client.Do(r)
	if e != nil {
		return fail("MODEL_PREPARATION_DOCKER_UNAVAILABLE")
	}
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, MaximumDockerResponseBytes+1))
	if e != nil || len(data) > MaximumDockerResponseBytes {
		return fail("MODEL_PREPARATION_DOCKER_REJECTED")
	}
	accepted := false
	for _, status := range statuses {
		accepted = accepted || response.StatusCode == status
	}
	if !accepted {
		if response.StatusCode == 404 {
			return fail("MODEL_PREPARATION_DOCKER_NOT_FOUND")
		}
		if response.StatusCode == 409 {
			return fail("MODEL_PREPARATION_CONTAINER_CONFLICT")
		}
		return fail("MODEL_PREPARATION_DOCKER_REJECTED")
	}
	if output != nil && json.Unmarshal(data, output) != nil {
		return fail("MODEL_PREPARATION_DOCKER_REJECTED")
	}
	return nil
}

type Image struct {
	ID           string `json:"Id"`
	Architecture string
	OS           string `json:"Os"`
	Config       struct {
		User         string
		WorkingDir   string
		Entrypoint   []string
		Cmd          []string
		Env          []string
		Labels       map[string]string
		Volumes      map[string]any
		ExposedPorts map[string]any
		Healthcheck  json.RawMessage
	}
}

func nonrootImageUser(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return false
	}
	for _, part := range parts {
		number, e := strconv.ParseInt(part, 10, 32)
		if e != nil || number < 1 || strconv.FormatInt(number, 10) != part {
			return false
		}
	}
	return true
}

func (d *Docker) InspectImage(ctx context.Context, config Config) (Image, error) {
	var image Image
	if config.Validate() != nil {
		return image, fail("MODEL_PREPARATION_AGENT_CONFIGURATION_INVALID")
	}
	if e := d.request(ctx, "GET", "/images/"+url.PathEscape(config.ImageID)+"/json", nil, &image, 200); e != nil {
		return image, e
	}
	if image.ID != config.ImageID || image.OS != "linux" || image.Architecture != "amd64" || !nonrootImageUser(image.Config.User) || image.Config.WorkingDir != ImageWorkingDirectory || len(image.Config.Entrypoint) != 2 || image.Config.Entrypoint[0] != "python" || image.Config.Entrypoint[1] != WorkerFile || len(image.Config.Cmd) != 0 || image.Config.Labels["projectone.model_lifecycle.worker_source_sha256"] != config.WorkerSourceSHA256 || image.Config.Labels["projectone.model_lifecycle.worker_version"] != wire.WorkerVersion || len(image.Config.Volumes) != 0 || len(image.Config.ExposedPorts) != 0 || len(image.Config.Healthcheck) != 0 && string(image.Config.Healthcheck) != "null" {
		return image, fail("MODEL_PREPARATION_IMAGE_INVALID")
	}
	return image, nil
}
func (d *Docker) CheckEngine(ctx context.Context) error {
	var info struct {
		inference.DockerInfo
		DriverStatus  [][]string
		CgroupVersion string
	}
	if e := d.request(ctx, "GET", "/info", nil, &info, 200); e != nil {
		return e
	}
	if info.OSType != "linux" || info.Architecture != "x86_64" && info.Architecture != "amd64" {
		return fail("MODEL_PREPARATION_PLATFORM_UNSUPPORTED")
	}
	if !info.MemoryLimit || !info.SwapLimit || !info.PidsLimit || !info.CPUCfsQuota {
		return fail("MODEL_PREPARATION_RESOURCE_LIMITS_UNAVAILABLE")
	}
	storeSupported := false
	for _, entry := range info.DriverStatus {
		if len(entry) == 2 && entry[0] == "driver-type" && entry[1] == "io.containerd.snapshotter.v1" {
			storeSupported = true
		}
	}
	if !storeSupported {
		return fail("MODEL_PREPARATION_IMAGE_STORE_UNSUPPORTED")
	}
	if info.CgroupVersion != "2" {
		return fail("MODEL_PREPARATION_RESOURCE_LIMITS_UNAVAILABLE")
	}
	if _, ok := info.Runtimes["nvidia"]; !ok {
		return fail("MODEL_PREPARATION_TOOLKIT_UNAVAILABLE")
	}
	for _, option := range info.SecurityOptions {
		if strings.Contains(option, "rootless") {
			return fail("MODEL_PREPARATION_DOCKER_SOCKET_INVALID")
		}
	}
	return nil
}

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var containerName = regexp.MustCompile(`^projectone-model-preparation-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Ulimit struct {
	Name string
	Soft int64
	Hard int64
}
type HostConfig struct {
	inference.HostConfig
	Ulimits       []Ulimit
	RestartPolicy struct {
		Name              string
		MaximumRetryCount int
	}
	PublishAllPorts bool
	CgroupnsMode    string
	CgroupParent    string
	VolumesFrom     []string
	GroupAdd        []string
	ExtraHosts      []string
	Links           []string
}
type ContainerConfig struct {
	Image        string
	User         string
	WorkingDir   string
	Entrypoint   []string
	Cmd          []string
	Env          []string
	Labels       map[string]string
	ExposedPorts map[string]struct{}
	HostConfig   HostConfig
}
type Container struct {
	ID    string `json:"Id"`
	Name  string
	Image string
	State struct {
		Running   bool
		Status    string
		ExitCode  int
		OOMKilled bool
		Dead      bool
		Error     string
	}
	Config struct {
		Image        string
		User         string
		WorkingDir   string
		Entrypoint   []string
		Cmd          []string
		Env          []string
		Labels       map[string]string
		ExposedPorts map[string]struct{}
	}
	HostConfig      HostConfig
	Mounts          []inference.Mount
	NetworkSettings struct {
		Networks map[string]struct{ NetworkID string }
	}
}

func (d *Docker) InspectContainer(ctx context.Context, reference string) (Container, error) {
	var out Container
	if !containerID.MatchString(reference) && !containerName.MatchString(reference) {
		return out, fail("MODEL_PREPARATION_CONTAINER_CONFLICT")
	}
	e := d.request(ctx, "GET", "/containers/"+reference+"/json", nil, &out, 200)
	if e == nil && !containerID.MatchString(out.ID) {
		return out, fail("MODEL_PREPARATION_DOCKER_REJECTED")
	}
	return out, e
}
func (d *Docker) Create(ctx context.Context, name string, config ContainerConfig) (string, error) {
	if !containerName.MatchString(name) {
		return "", fail("MODEL_PREPARATION_CONTAINER_CONFLICT")
	}
	var out struct {
		ID string `json:"Id"`
	}
	e := d.request(ctx, "POST", "/containers/create?name="+url.QueryEscape(name), config, &out, 201)
	if e == nil && !containerID.MatchString(out.ID) {
		return "", fail("MODEL_PREPARATION_DOCKER_REJECTED")
	}
	return out.ID, e
}
func (d *Docker) Start(ctx context.Context, id string) error {
	if !containerID.MatchString(id) {
		return fail("MODEL_PREPARATION_CONTAINER_CONFLICT")
	}
	return d.request(ctx, "POST", "/containers/"+id+"/start", nil, nil, 204, 304)
}
func (d *Docker) Stop(ctx context.Context, id string, grace time.Duration) error {
	if !containerID.MatchString(id) || grace < time.Second || grace > time.Minute {
		return fail("MODEL_PREPARATION_CONTAINER_CONFLICT")
	}
	return d.request(ctx, "POST", "/containers/"+id+"/stop?t="+strconv.Itoa(int(grace/time.Second)), nil, nil, 204, 304)
}
func (d *Docker) Kill(ctx context.Context, id string) error {
	if !containerID.MatchString(id) {
		return fail("MODEL_PREPARATION_CONTAINER_CONFLICT")
	}
	return d.request(ctx, "POST", "/containers/"+id+"/kill?signal=SIGKILL", nil, nil, 204)
}
func (d *Docker) Remove(ctx context.Context, id string) error {
	if !containerID.MatchString(id) {
		return fail("MODEL_PREPARATION_CONTAINER_CONFLICT")
	}
	return d.request(ctx, "DELETE", "/containers/"+id+"?force=false&v=false", nil, nil, 204)
}
