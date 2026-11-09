package apps

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"regexp"
	"slices"
)

const (
	maxComposeModelBytes = 1 << 20
	maxComposeServices   = 256
)

// ErrComposeImagePlan identifies a rendered Compose model outside the signed local image set.
var ErrComposeImagePlan = errors.New("CATALOG_COMPOSE_IMAGE_PLAN_INVALID")

var composeServiceName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RenderedCompose identifies the verified source artifact used by the installer's
// restricted renderer and its exact normalized Compose JSON output.
type RenderedCompose struct {
	ArtifactSHA256 string
	JSON           []byte
}

// ComposeServiceImage binds a rendered service to its signed manifest digest.
type ComposeServiceImage struct {
	Service        string
	ManifestDigest string
}

// ComposeImagePlan identifies the checked rendered bytes and their image bindings.
// The installer admits the workload and checks the same bytes before execution.
type ComposeImagePlan struct {
	Pin            ReleasePin
	ArtifactSHA256 string
	ModelSHA256    string
	Services       []ComposeServiceImage
}

// VerifyRendered checks the source identity and exact bytes against a trusted
// admitted plan. The installer passes those same bytes to the launcher.
func (p ComposeImagePlan) VerifyRendered(rendered RenderedCompose) error {
	if !digest.MatchString(p.ArtifactSHA256) || rendered.ArtifactSHA256 != p.ArtifactSHA256 ||
		len(rendered.JSON) == 0 || len(rendered.JSON) > maxComposeModelBytes || digestBytes(rendered.JSON) != p.ModelSHA256 {
		return fmt.Errorf("%w: use the exact admitted Compose model and source digest", ErrComposeImagePlan)
	}
	return nil
}

type composeImageModel struct {
	Services map[string]composeImageService `json:"services"`
	Include  jsontext.Value                 `json:"include"`
	Models   jsontext.Value                 `json:"models"`
}

type composeImageService struct {
	Image      string         `json:"image"`
	PullPolicy string         `json:"pull_policy"`
	Platform   string         `json:"platform"`
	Build      jsontext.Value `json:"build"`
	Develop    jsontext.Value `json:"develop"`
	Extends    jsontext.Value `json:"extends"`
	Provider   jsontext.Value `json:"provider"`
	PreStart   jsontext.Value `json:"pre_start"`
	Models     jsontext.Value `json:"models"`
	Volumes    []struct {
		Type string `json:"type"`
	} `json:"volumes"`
}

// VerifyComposeImagePlan checks the installer's normalized Compose model against
// an InstallBundle returned by the signed verifier. The installer provides a
// trusted source digest from its disconnected renderer. This check covers
// image acquisition. Authorization and workload admission belong to the installer.
func (b InstallBundle) VerifyComposeImagePlan(rendered RenderedCompose) (ComposeImagePlan, error) {
	if !b.Pin.valid() || b.Pin.Version != b.Manifest.Version || len(b.Archive.Images) == 0 ||
		b.Manifest.Artifact.Engine != "compose" || rendered.ArtifactSHA256 != b.Manifest.Artifact.SHA256 {
		return ComposeImagePlan{}, fmt.Errorf("%w: render the verified Compose artifact from the selected bundle", ErrComposeImagePlan)
	}
	var model composeImageModel
	if len(rendered.JSON) > maxComposeModelBytes || json.Unmarshal(rendered.JSON, &model) != nil ||
		len(model.Services) == 0 || len(model.Services) > maxComposeServices ||
		len(model.Include) != 0 || len(model.Models) != 0 {
		return ComposeImagePlan{}, fmt.Errorf("%w: supply bounded normalized Compose JSON with services and no unresolved includes or models", ErrComposeImagePlan)
	}

	approved := make(map[string]bool, len(b.Archive.Images))
	for _, image := range b.Archive.Images {
		approved[image.ManifestDigest] = true
	}
	names := make([]string, 0, len(model.Services))
	for name := range model.Services {
		if !composeServiceName.MatchString(name) {
			return ComposeImagePlan{}, fmt.Errorf("%w: use bounded Compose service names", ErrComposeImagePlan)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	plan := ComposeImagePlan{
		Pin: b.Pin, ArtifactSHA256: b.Manifest.Artifact.SHA256, ModelSHA256: digestBytes(rendered.JSON),
		Services: make([]ComposeServiceImage, 0, len(names)),
	}
	for _, name := range names {
		service := model.Services[name]
		if !digest.MatchString(service.Image) || !approved[service.Image] || service.PullPolicy != "never" ||
			(service.Platform != "" && service.Platform != "linux/amd64" && service.Platform != "linux/amd64/v1") {
			return ComposeImagePlan{}, fmt.Errorf("%w: service %s needs a signed Linux amd64 image digest and pull_policy never", ErrComposeImagePlan, name)
		}
		if len(service.Build) != 0 || len(service.Develop) != 0 || len(service.Extends) != 0 ||
			len(service.Provider) != 0 || len(service.PreStart) != 0 || len(service.Models) != 0 {
			return ComposeImagePlan{}, fmt.Errorf("%w: service %s uses an unsupported image acquisition path", ErrComposeImagePlan, name)
		}
		for _, volume := range service.Volumes {
			if volume.Type != "volume" && volume.Type != "bind" && volume.Type != "tmpfs" {
				return ComposeImagePlan{}, fmt.Errorf("%w: service %s needs a supported normalized volume type", ErrComposeImagePlan, name)
			}
		}
		plan.Services = append(plan.Services, ComposeServiceImage{Service: name, ManifestDigest: service.Image})
	}
	return plan, nil
}
