package inference

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LoadRegistryArchive uses the already protected local Unix client. The caller
// supplies verified tagless bytes, a bounded deadline and its typed response cap.
// Every uncertain response requires actual inspect reconciliation, never retry.
type RegistryLoadPolicy struct {
	MaximumResponseBytes   int64
	ExpectedDockerDataRoot string
	RequiredBytes          int64
	MinimumHeadroomPercent int
}

func (d *Docker) LoadRegistryArchive(ctx context.Context, archive io.Reader, length int64, p RegistryLoadPolicy) (Image, error) {
	if d == nil || d.client == nil || archive == nil || length < 1 || p.MaximumResponseBytes < 1 || p.RequiredBytes < length || p.RequiredBytes > 1<<51 || p.MinimumHeadroomPercent < 30 || p.MinimumHeadroomPercent > 90 || !filepath.IsAbs(p.ExpectedDockerDataRoot) || filepath.Clean(p.ExpectedDockerDataRoot) != p.ExpectedDockerDataRoot {
		return Image{}, fail("REGISTRY_LOAD_INVALID")
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 {
		return Image{}, fail("REGISTRY_LOAD_INVALID")
	}
	var info struct{ DockerRootDir string }
	if e := d.request(ctx, http.MethodGet, "/info", nil, &info, http.StatusOK); e != nil || info.DockerRootDir != p.ExpectedDockerDataRoot {
		return Image{}, fail("REGISTRY_DOCKER_STORAGE_UNVERIFIED")
	}
	stat, e := os.Lstat(p.ExpectedDockerDataRoot)
	var fs syscall.Statfs_t
	if e != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 || syscall.Statfs(p.ExpectedDockerDataRoot, &fs) != nil || fs.Blocks == 0 {
		return Image{}, fail("REGISTRY_DOCKER_STORAGE_UNVERIFIED")
	}
	available := uint64(fs.Bavail) * uint64(fs.Bsize)
	capacity := uint64(fs.Blocks) * uint64(fs.Bsize)
	if available < uint64(p.RequiredBytes) || available-uint64(p.RequiredBytes) < capacity/100*uint64(p.MinimumHeadroomPercent) {
		return Image{}, fail("REGISTRY_STORAGE_CAPACITY_REQUIRED")
	}
	transport := d.transport.Clone()
	transport.ResponseHeaderTimeout = time.Until(deadline)
	defer transport.CloseIdleConnections()
	client := *d.client
	client.Transport = transport
	client.Timeout = 0
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker"+engineAPI+"/images/load?quiet=1", archive)
	if e != nil {
		return Image{}, fail("REGISTRY_LOAD_INVALID")
	}
	req.ContentLength = length
	req.Header.Set("Content-Type", "application/x-tar")
	response, e := client.Do(req)
	if e != nil {
		return Image{}, fail("REGISTRY_LOAD_OUTCOME_UNKNOWN")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Image{}, fail("REGISTRY_LOAD_OUTCOME_UNKNOWN")
	}
	limited := &io.LimitedReader{R: response.Body, N: p.MaximumResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	events := 0
	for {
		var event map[string]json.RawMessage
		e = decoder.Decode(&event)
		if e == io.EOF {
			break
		}
		if e != nil || event == nil {
			return Image{}, fail("REGISTRY_LOAD_OUTCOME_UNKNOWN")
		}
		if _, ok := event["error"]; ok {
			return Image{}, fail("REGISTRY_LOAD_OUTCOME_UNKNOWN")
		}
		if _, ok := event["errorDetail"]; ok {
			return Image{}, fail("REGISTRY_LOAD_OUTCOME_UNKNOWN")
		}
		events++
	}
	if events == 0 || limited.N <= 0 || ctx.Err() != nil {
		return Image{}, fail("REGISTRY_LOAD_OUTCOME_UNKNOWN")
	}
	image, e := d.InspectApprovedImage(ctx)
	if e != nil {
		return Image{}, fail("REGISTRY_LOAD_OUTCOME_UNKNOWN")
	}
	return image, nil
}
func (d *Docker) ReconcileRegistryImage(ctx context.Context) (Image, error) {
	return d.InspectApprovedImage(ctx)
}
