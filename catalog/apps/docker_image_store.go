package apps

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
)

const (
	dockerAPI         = "http://docker/v1.49"
	dockerLinuxAMD64  = `{"os":"linux","architecture":"amd64"}`
	dockerResponseMax = 2 << 20
)

type dockerImageStore struct {
	client *http.Client
}

func newDockerImageStore(socket string) (dockerImageStore, error) {
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket {
		return dockerImageStore{}, imageImportError("configure an absolute local Docker socket path")
	}
	info, err := os.Stat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return dockerImageStore{}, imageImportError("the configured local Docker socket is unavailable")
	}
	dialer := &net.Dialer{}
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socket)
			},
			DisableCompression:     true,
			MaxResponseHeaderBytes: 32 << 10,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return dockerImageStore{client: client}, nil
}

func (d dockerImageStore) requireContainerd(ctx context.Context) error {
	status, raw, err := d.request(ctx, http.MethodGet, "/info", nil)
	if err != nil {
		return err
	}
	var info struct {
		DriverStatus [][]string `json:"DriverStatus"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &info) != nil ||
		!slices.ContainsFunc(info.DriverStatus, func(pair []string) bool {
			return len(pair) == 2 && pair[0] == "driver-type" && pair[1] == "io.containerd.snapshotter.v1"
		}) {
		return imageImportError("enable the local Docker containerd image store")
	}
	return nil
}

func (d dockerImageStore) inspect(ctx context.Context, expected string) (bool, error) {
	query := url.Values{"platform": {dockerLinuxAMD64}}
	status, raw, err := d.request(ctx, http.MethodGet, "/images/"+url.PathEscape(expected)+"/json?"+query.Encode(), nil)
	if err != nil || status == http.StatusNotFound {
		return false, err
	}
	var image struct {
		ID           string `json:"Id"`
		OS           string `json:"Os"`
		Architecture string `json:"Architecture"`
		Descriptor   struct {
			Digest    string `json:"digest"`
			MediaType string `json:"mediaType"`
		} `json:"Descriptor"`
	}
	if status != http.StatusOK || json.Unmarshal(raw, &image) != nil {
		return false, imageImportError("cannot inspect the signed image in the local store")
	}
	if image.ID != expected || image.Descriptor.Digest != expected || image.Descriptor.MediaType != ociManifestType ||
		image.OS != "linux" || image.Architecture != "amd64" {
		return false, imageImportError("registered image identity differs from the signed Linux amd64 manifest")
	}
	return true, nil
}

func (d dockerImageStore) load(ctx context.Context, write func(io.Writer) error) error {
	reader, writer := io.Pipe()
	written := make(chan error, 1)
	go func() {
		err := write(writer)
		_ = writer.CloseWithError(err)
		written <- err
	}()
	status, raw, err := d.request(ctx, http.MethodPost, "/images/load?quiet=1", reader)
	_ = reader.Close()
	writeErr := <-written
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if writeErr != nil || status != http.StatusOK || !successfulLoad(raw) {
		return imageImportError("Docker did not accept the verified tagless image")
	}
	return nil
}

func successfulLoad(raw []byte) bool {
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	for {
		value, err := decoder.ReadValue()
		if err == io.EOF {
			return true
		}
		var result struct {
			Error       string    `json:"error"`
			ErrorDetail *struct{} `json:"errorDetail"`
		}
		if err != nil || value.Kind() != '{' || json.Unmarshal(value, &result) != nil ||
			result.Error != "" || result.ErrorDetail != nil {
			return false
		}
	}
}

func (d dockerImageStore) request(ctx context.Context, method, endpoint string, input io.Reader) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, dockerAPI+endpoint, input)
	if err != nil {
		return 0, nil, imageImportError("cannot create a local Docker request")
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/x-tar")
	}
	response, err := d.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, nil, ctx.Err()
		}
		return 0, nil, imageImportError("cannot reach the configured local Docker socket")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, dockerResponseMax+1))
	if ctx.Err() != nil {
		return 0, nil, ctx.Err()
	}
	if err != nil || len(raw) > dockerResponseMax {
		return 0, nil, imageImportError("local Docker response is unreadable or too large")
	}
	return response.StatusCode, raw, nil
}
