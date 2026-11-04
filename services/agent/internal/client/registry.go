package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
	release "github.com/Passion-Flow/ownsolid/api/release/v1"
)

var registryTaskPath = regexp.MustCompile(`^/agent/v1/nodes/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}/tasks/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}/image-registry$`)

func (c *Client) RegistryManifest(ctx context.Context, path, lease string, command wire.InferenceCommand) (wire.RegistryManifest, error) {
	if !registryTaskPath.MatchString(path) {
		return wire.RegistryManifest{}, errors.New("AGENT_PATH_INVALID")
	}
	raw, _ := json.Marshal(wire.LeaseRenewal{LeaseToken: lease})
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, c.target(path)+"/manifest", bytes.NewReader(raw))
	if e != nil {
		return wire.RegistryManifest{}, errors.New("CONTROL_REQUEST_INVALID")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.Credential)
	response, e := c.HTTP.Do(request)
	if e != nil {
		return wire.RegistryManifest{}, errors.New("CONTROL_CONNECTION_FAILED")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return wire.RegistryManifest{}, errors.New("REGISTRY_MANIFEST_UNAVAILABLE")
	}
	if media := response.Header.Get("Content-Type"); media != "application/json" && media != "application/json; charset=utf-8" {
		return wire.RegistryManifest{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, wire.RegistryMaximumProofBytes+65537))
	if e != nil || len(data) > wire.RegistryMaximumProofBytes+65536 {
		return wire.RegistryManifest{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	var envelope struct {
		Data wire.RegistryManifest `json:"data"`
	}
	if release.StrictJSON(data, &envelope) != nil {
		return wire.RegistryManifest{}, errors.New("CONTROL_RESPONSE_INVALID")
	}
	out := envelope.Data

	if e := out.Validate(command); e != nil {
		return wire.RegistryManifest{}, e
	}
	return out, nil
}
func (c *Client) RegistryArchive(ctx context.Context, path, lease string, m wire.RegistryManifest, dest io.Writer) error {
	if !registryTaskPath.MatchString(path) || m.ImageIdentity().Validate() != nil || dest == nil {
		return errors.New("REGISTRY_MANIFEST_INVALID")
	}
	h := sha256.New()
	for offset := int64(0); offset < m.ArchiveBytes; {
		length := min(wire.RegistryMaximumRangeBytes, m.ArchiveBytes-offset)
		input := struct {
			Lease string `json:"lease_token"`
			wire.RegistryRange
		}{lease, wire.RegistryRange{GrantID: m.GrantID, Offset: offset, Length: length}}
		raw, _ := json.Marshal(input)
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.target(path)+"/ranges", bytes.NewReader(raw))
		if e != nil {
			return errors.New("CONTROL_REQUEST_INVALID")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.Credential)
		r, e := c.HTTP.Do(req)
		if e != nil {
			return errors.New("CONTROL_CONNECTION_FAILED")
		}
		if r.StatusCode != http.StatusOK || r.ContentLength != length || r.Header.Get("Content-Type") != "application/octet-stream" {
			r.Body.Close()
			return errors.New("REGISTRY_RANGE_INVALID")
		}
		content, e := io.ReadAll(io.LimitReader(r.Body, length+1))
		r.Body.Close()
		actual := sha256.Sum256(content)
		if e != nil || int64(len(content)) != length || r.Header.Get("X-Artifact-SHA256") != hex.EncodeToString(actual[:]) {
			return errors.New("REGISTRY_RANGE_INVALID")
		}
		if _, e = io.Copy(io.MultiWriter(dest, h), bytes.NewReader(content)); e != nil {
			return e
		}
		offset += length
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), m.ArchiveSHA256) {
		return errors.New("REGISTRY_DIGEST_MISMATCH")
	}
	return nil
}
