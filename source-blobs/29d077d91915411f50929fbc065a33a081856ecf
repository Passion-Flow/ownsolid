package imageregistry

import (
	"context"
	"io"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	release "github.com/Passion-Flow/ownsolid/api/release/v1"
	"github.com/Passion-Flow/ownsolid/tools/release/offline/pkg/bundle"
)

// Match the repository component admitted by the signed registry installer.
var siteAssetID = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)

type siteReleaseSource struct {
	client *distributionClient
}

type siteSourceConfig struct {
	Endpoint       string   `json:"endpoint"`
	AllowedCIDRs   []string `json:"allowed_cidrs"`
	CAFile         string   `json:"ca_file"`
	CredentialFile string   `json:"credential_file"`
}

type siteSourceReceipt struct {
	RegistryIdentity string `json:"registry_identity"`
	Repository       string `json:"repository"`
	ManifestDigest   string `json:"manifest_digest"`
}

func openSiteReleaseSource(filename string, destination Config, remote *distributionClient) (*siteReleaseSource, error) {
	raw, err := bundle.ReadProtectedConfiguration(filename)
	if err != nil {
		return nil, failure("REGISTRY_SITE_SOURCE_CONFIGURATION_INVALID")
	}
	var config siteSourceConfig
	if release.StrictJSON(raw, &config) != nil {
		return nil, failure("REGISTRY_SITE_SOURCE_CONFIGURATION_INVALID")
	}
	source := destination
	source.Endpoint = config.Endpoint
	source.AllowedCIDRs = config.AllowedCIDRs
	source.CAFile = config.CAFile
	source.CredentialFile = config.CredentialFile
	client, err := newDistribution(source)
	if err != nil {
		return nil, err
	}
	if sameRegistryOrigin(remote.origin, client.origin) || remote.credential == client.credential {
		client.Close()
		return nil, failure("REGISTRY_SITE_SOURCE_NOT_DISTINCT")
	}
	return &siteReleaseSource{client: client}, nil
}

func sameRegistryOrigin(a, b *url.URL) bool {
	authority := func(u *url.URL) (string, int) {
		host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
		if ip, err := netip.ParseAddr(host); err == nil {
			host = ip.Unmap().String()
		}
		port := 443
		if u.Port() != "" {
			port, _ = strconv.Atoi(u.Port())
		}
		return host, port
	}
	ah, ap := authority(a)
	bh, bp := authority(b)
	return ah == bh && ap == bp
}

func siteReleaseRepository(asset string) (string, error) {
	if len(asset) > 160 || !siteAssetID.MatchString(asset) || asset == bundle.RegistryImageAsset {
		return "", failure("REGISTRY_PATH_REJECTED")
	}
	return "ownsolid/release/" + asset, nil
}

func (s *siteReleaseSource) get(ctx context.Context, asset string, blob bundle.RuntimeBlob, w io.Writer) error {
	repo, err := siteReleaseRepository(asset)
	if err != nil {
		return err
	}
	if !validDigest(blob.Digest) {
		return failure("REGISTRY_DESCRIPTOR_INVALID")
	}
	kind, descriptorKind := "blobs", "blob"
	if blob.Manifest {
		kind, descriptorKind = "manifests", "manifest"
	}
	target := s.client.origin.String() + "/v2/" + repo + "/" + kind + "/" + blob.Digest
	d := Descriptor{Digest: blob.Digest, Size: blob.Size, MediaType: blob.MediaType, Kind: descriptorKind}
	return s.client.copyVerified(ctx, target, d, w)
}

func (s *siteReleaseSource) stage(ctx context.Context, c Config, root *os.Root, stage string, blobs []bundle.RuntimeBlob, out *cachedImage) error {
	repo, err := siteReleaseRepository(out.Asset.ID)
	if err != nil {
		return err
	}
	if err := root.Mkdir(stage+"/blobs", 0700); err != nil {
		return failure("REGISTRY_STORE_INVALID")
	}
	for _, blob := range blobs {
		if !validDigest(blob.Digest) || blob.Size < 1 || blob.Size > c.MaximumBlobBytes ||
			blob.Manifest && blob.Size > c.Verification.MaximumMetadataBytes {
			return failure("REGISTRY_DESCRIPTOR_INVALID")
		}
		f, err := root.OpenFile(stage+"/blobs/"+blob.Digest[7:], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return failure("REGISTRY_STORE_INVALID")
		}
		err = s.get(ctx, out.Asset.ID, blob, f)
		if err == nil {
			err = f.Sync()
		}
		if err := joinedClose(f, err); err != nil {
			return err
		}
	}
	if err := finishStagedImage(ctx, c, root, stage, len(blobs), out); err != nil {
		return err
	}
	out.SiteSource = &siteSourceReceipt{
		RegistryIdentity: s.client.identity, Repository: repo, ManifestDigest: out.Inventory.ManifestDigest,
	}
	return nil
}

func (r *siteSourceReceipt) matches(asset release.Asset) bool {
	if r == nil {
		return true
	}
	repo, err := siteReleaseRepository(asset.ID)
	return err == nil && validDigest(r.RegistryIdentity) && validDigest(r.ManifestDigest) && r.Repository == repo && r.ManifestDigest == asset.ImageID
}
