package inference

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
)

// GatewayContainer is a gateway container as Docker lists it.
type GatewayContainer struct {
	ID     string `json:"Id"`
	State  string
	Labels map[string]string
}

var gatewayName = regexp.MustCompile(`^` + gatewayNamePrefix + `[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

// InspectImage reads a local image by its ID. The agent never pulls one.
func (d *Docker) InspectImage(ctx context.Context, id string) (Image, error) {
	var image Image
	if !imageID.MatchString(id) {
		return image, fail("INFERENCE_IMAGE_INVALID")
	}
	e := d.request(ctx, "GET", "/images/"+url.PathEscape(id)+"/json", nil, &image, 200)
	return image, e
}

// GatewayContainers lists every container that carries the gateway label, running or not.
func (d *Docker) GatewayContainers(ctx context.Context) ([]GatewayContainer, error) {
	filters, _ := json.Marshal(map[string][]string{"label": {gatewayLabel}})
	var listed []GatewayContainer
	if e := d.request(ctx, "GET", "/containers/json?all=1&filters="+url.QueryEscape(string(filters)), nil, &listed, 200); e != nil {
		return nil, e
	}
	for _, c := range listed {
		if !containerID.MatchString(c.ID) || !deploymentID.MatchString(c.Labels[gatewayLabel]) {
			return nil, fail("INFERENCE_DOCKER_REJECTED")
		}
	}
	return listed, nil
}

// CreateGatewayContainer creates the named gateway container from a configuration the
// gateway manager built.
func (d *Docker) CreateGatewayContainer(ctx context.Context, name string, configuration ContainerConfig) (string, error) {
	if !gatewayName.MatchString(name) || !imageID.MatchString(configuration.Image) {
		return "", fail("INFERENCE_CONTAINER_CONFLICT")
	}
	var result struct {
		ID string `json:"Id"`
	}
	if e := d.request(ctx, "POST", "/containers/create?name="+url.QueryEscape(name), configuration, &result, 201); e != nil {
		return "", e
	}
	if !containerID.MatchString(result.ID) {
		return "", fail("INFERENCE_DOCKER_REJECTED")
	}
	return result.ID, nil
}
