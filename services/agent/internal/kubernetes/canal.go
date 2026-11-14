package kubernetes

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Passion-Flow/ownsolid/services/agent/internal/progress"
)

const (
	canalManifest = dataDirectory + "/server/manifests/rke2-canal.yaml"
	canalHold     = canalManifest + ".skip"
	canalScanName = "zz-ownsolid-canal-bootstrap"
	canalScanFile = dataDirectory + "/server/manifests/" + canalScanName + ".yaml"
	canalScanNote = "# ownsolid Canal bootstrap "
	felixResource = "felixconfigurations.crd.projectcalico.org"
	felixCRDPath  = "rke2-canal/templates/crds/crd.projectcalico.org_felixconfigurations.yaml"
)

const felixHelmOwnership = `{"metadata":{"labels":{"app.kubernetes.io/managed-by":"Helm"},"annotations":{"meta.helm.sh/release-name":"rke2-canal","meta.helm.sh/release-namespace":"kube-system"}}}`
const offlineFelixDefault = `{"apiVersion":"crd.projectcalico.org/v1","kind":"FelixConfiguration","metadata":{"name":"default"},"spec":{"usageReportingEnabled":false}}`

// holdCanal defers the packaged HelmChart until its first Felix configuration is ready.
// The journal target identifies our skip marker on retry and lets Symlink create it
// atomically without replacing a site-owned file.
func (m *Manager) holdCanal(fresh bool) (bool, error) {
	path := m.path(canalHold)
	if fresh {
		if e := os.MkdirAll(filepath.Dir(path), 0o700); e != nil {
			return false, e
		}
		if e := os.Symlink(m.journal, path); e != nil && !errors.Is(e, os.ErrExist) {
			return false, e
		}
	}
	return m.ownsCanalHold()
}

func (m *Manager) ownsCanalHold() (bool, error) {
	path := m.path(canalHold)
	info, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil || info.Mode()&os.ModeSymlink == 0 {
		return false, e
	}
	target, e := os.Readlink(path)
	return target == m.journal, e
}

func (m *Manager) finishCanalBootstrap(ctx context.Context, node string, deadline time.Time) error {
	scratch, e := os.MkdirTemp(filepath.Dir(m.journal), "canal-bootstrap-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(scratch)
	kubectl := func(args ...string) ([]byte, error) {
		return m.host.Run(ctx, m.path(kubectlBinary), append([]string{"--kubeconfig", m.path(adminKubeconfig), "--request-timeout=5s"}, args...)...)
	}
	scanNote := []byte(canalScanNote + filepath.Base(scratch) + "\n")
	sum := sha256.Sum256(scanNote)
	scanChecksum := hex.EncodeToString(sum[:])
	scanned := false
	for {
		var ready bool
		if !scanned {
			scanned, ready, e = m.waitForCanalScan(ctx, kubectl, node, scanNote, scanChecksum)
			if e != nil {
				return e
			}
		}
		if scanned && !ready {
			ready, e = m.configureCanal(kubectl, scratch)
			if e != nil {
				progress.Error(ctx, "Could not apply the offline Felix configuration from the bundled Canal chart.")
				return e
			}
		}
		if ready {
			if e = m.removeCanalScan(kubectl); e != nil {
				return e
			}
			owned, e := m.ownsCanalHold()
			if e != nil || !owned {
				return e
			}
			return os.Remove(m.path(canalHold))
		}
		if !m.now().Before(deadline) {
			progress.Error(ctx, "The local Kubernetes API did not finish applying the offline Canal configuration. Check the RKE2 server and retry the join.")
			return errors.New("KUBERNETES_NOT_READY")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.poll):
		}
	}
}

// waitForCanalScan distinguishes a selected chart from a disabled chart that
// RKE2 has staged temporarily. The pinned deploy controller processes the
// comment-only marker after the Canal file and publishes its checksum.
func (m *Manager) waitForCanalScan(ctx context.Context, kubectl func(...string) ([]byte, error), node string, note []byte, checksum string) (scanned, ready bool, e error) {
	manifest := m.path(canalManifest)
	if _, e = os.Stat(manifest); errors.Is(e, os.ErrNotExist) {
		return false, m.ready(ctx, node), nil
	} else if e != nil {
		return false, false, e
	}
	marker := m.path(canalScanFile)
	current, e := os.ReadFile(marker)
	switch {
	case errors.Is(e, os.ErrNotExist), e == nil && bytes.HasPrefix(current, []byte(canalScanNote)):
		if !bytes.Equal(current, note) {
			if e = writeBytes(marker, note); e != nil {
				return false, false, e
			}
		}
	case e != nil:
		return false, false, e
	default:
		progress.Error(ctx, "A site manifest uses the Agent's Canal bootstrap marker filename.")
		return false, false, errors.New("KUBERNETES_CONFIG_CONFLICT")
	}
	output, e := kubectl("-n", "kube-system", "get", "addons.k3s.cattle.io", canalScanName, "--ignore-not-found", "-o", "jsonpath={.spec.checksum}")
	if e != nil || string(bytes.TrimSpace(output)) != checksum {
		return false, false, nil
	}
	_, e = os.Stat(manifest)
	if errors.Is(e, os.ErrNotExist) {
		return true, true, nil
	}
	return e == nil, false, e
}

func (m *Manager) removeCanalScan(kubectl func(...string) ([]byte, error)) error {
	marker := m.path(canalScanFile)
	content, e := os.ReadFile(marker)
	if errors.Is(e, os.ErrNotExist) || e == nil && !bytes.HasPrefix(content, []byte(canalScanNote)) {
		return nil
	}
	if e != nil {
		return e
	}
	if e = os.Remove(marker); e != nil {
		return e
	}
	_, _ = kubectl("-n", "kube-system", "delete", "addons.k3s.cattle.io", canalScanName, "--ignore-not-found", "--wait=false")
	return nil
}

// configureCanal performs only the missing bootstrap steps. API startup, CRD
// discovery, and conflicts with a site write can all be retried with the hold in place.
func (m *Manager) configureCanal(kubectl func(...string) ([]byte, error), scratch string) (bool, error) {
	manifest := m.path(canalManifest)
	crd, e := kubectl("get", "crd", felixResource, "--ignore-not-found", "-o", "name")
	if e != nil {
		return false, nil
	}
	if len(bytes.TrimSpace(crd)) == 0 {
		chart, e := kubectl("create", "--dry-run=client", "--validate=false", "-f", manifest, "-o", "jsonpath={.spec.chartContent}")
		if e != nil || len(bytes.TrimSpace(chart)) == 0 {
			return false, nil
		}
		schema, e := bundledFelixCRD(chart)
		if e != nil {
			return false, e
		}
		file := filepath.Join(scratch, "felix-crd.yaml")
		if e = writeBytes(file, schema); e != nil {
			return false, e
		}
		owned, e := kubectl("patch", "--local", "--type=merge", "-f", file, "-p", felixHelmOwnership, "-o", "json")
		if e != nil {
			return false, nil
		}
		if e = writeBytes(file, owned); e != nil {
			return false, e
		}
		if _, e = kubectl("create", "-f", file); e != nil {
			return false, nil
		}
	}
	current, e := kubectl("get", felixResource, "default", "--ignore-not-found", "-o", "json")
	if e != nil {
		return false, nil
	}
	if len(bytes.TrimSpace(current)) == 0 {
		file := filepath.Join(scratch, "felix-default.json")
		if e = writeBytes(file, []byte(offlineFelixDefault)); e != nil {
			return false, e
		}
		_, e = kubectl("create", "-f", file)
		return e == nil, nil
	}
	var config struct {
		Metadata struct {
			ResourceVersion string `json:"resourceVersion"`
		} `json:"metadata"`
		Spec struct {
			UsageReportingEnabled *bool `json:"usageReportingEnabled"`
		} `json:"spec"`
	}
	if e = json.Unmarshal(current, &config); e != nil || config.Metadata.ResourceVersion == "" {
		return false, errors.New("KUBERNETES_IO_FAILED")
	}
	if config.Spec.UsageReportingEnabled != nil {
		return true, nil
	}
	patch, e := json.Marshal(map[string]any{
		"metadata": map[string]string{"resourceVersion": config.Metadata.ResourceVersion},
		"spec":     map[string]bool{"usageReportingEnabled": false},
	})
	if e != nil {
		return false, e
	}
	_, e = kubectl("patch", felixResource, "default", "--type=merge", "-p", string(patch))
	return e == nil, nil
}

// bundledFelixCRD takes only the schema from the already verified RKE2 runtime
// archive. The packaged chart itself will install all other Canal resources.
func bundledFelixCRD(chart []byte) ([]byte, error) {
	invalid := errors.New("KUBERNETES_ASSET_INVALID")
	compressed, e := gzip.NewReader(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(chart)))
	if e != nil {
		return nil, invalid
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	for {
		entry, e := archive.Next()
		if e != nil {
			return nil, invalid
		}
		if entry.Name != felixCRDPath {
			continue
		}
		if entry.Typeflag != tar.TypeReg || entry.Size <= 0 || entry.Size > 1<<20 {
			return nil, invalid
		}
		schema, e := io.ReadAll(archive)
		if e != nil {
			return nil, invalid
		}
		return schema, nil
	}
}
