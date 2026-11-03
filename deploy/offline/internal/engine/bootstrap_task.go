package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// BootstrapIntent contains a credential. Callers must persist Contents exclusively
// in protected storage. JSON diagnostics intentionally expose no credential bytes.
type BootstrapIntent struct {
	Contents []byte `json:"-"`
	SHA256   string `json:"sha256"`
	Bytes    int    `json:"bytes"`
}

// ReadBootstrapIntent observes only an exact exited task's private state volume.
// It neither writes a helper nor treats file presence as a verified SQL outcome.
func (c *Client) ReadBootstrapIntent(ctx context.Context, t DatabaseTask, id string) (BootstrapIntent, error) {
	if !t.bootstrap() {
		return BootstrapIntent{}, failure("OFFLINE_ENGINE_BOOTSTRAP_TASK_REQUIRED")
	}
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return BootstrapIntent{}, e
	}
	if before.State.Status != "exited" {
		return BootstrapIntent{}, failure("OFFLINE_ENGINE_EXITED_BOOTSTRAP_TASK_REQUIRED")
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {BootstrapTaskDestination}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return BootstrapIntent{}, failure("OFFLINE_ENGINE_BOOTSTRAP_INTENT_READBACK_FAILED")
	}
	result, e := bootstrapIntentArchive(raw, t.UID, t.GID)
	if e != nil {
		return result, e
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return BootstrapIntent{}, failure("OFFLINE_ENGINE_BOOTSTRAP_TASK_CHANGED_DURING_READBACK")
	}
	return result, nil
}

// The backend's format bounds and metadata are repeated at this untrusted archive
// boundary. Semantic request/database/password binding remains the backend's job.
const maximumBootstrapIntentBytes = 16 << 10

func bootstrapIntentArchive(raw []byte, uid, gid int) (BootstrapIntent, error) {
	bad := func() (BootstrapIntent, error) {
		return BootstrapIntent{}, failure("OFFLINE_ENGINE_BOOTSTRAP_INTENT_INVENTORY_INVALID")
	}
	if len(raw) < 3*tarBlockBytes || len(raw)%tarBlockBytes != 0 || len(bytes.Trim(raw[len(raw)-tarEndBytes:], "\x00")) != 0 {
		return bad()
	}
	buffer := bytes.NewReader(raw)
	reader := tar.NewReader(buffer)
	seen := map[string]bool{}
	result := BootstrapIntent{}
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return bad()
		}
		n := strings.TrimSuffix(h.Name, "/")
		if seen[n] || h.Linkname != "" || len(h.PAXRecords) > 0 || h.Mode&^0777 != 0 {
			return bad()
		}
		seen[n] = true
		switch n {
		case "bootstrap":
			if h.Typeflag != tar.TypeDir || h.Size != 0 {
				return bad()
			}
		case "bootstrap/private":
			if h.Typeflag != tar.TypeDir || h.Size != 0 || h.Mode != 0700 || h.Uid != uid || h.Gid != gid {
				return bad()
			}
		case "bootstrap/private/" + BootstrapCredentialFile:
			if h.Typeflag != tar.TypeReg || h.Mode != 0600 || h.Uid != uid || h.Gid != gid || h.Size < 1 || h.Size > maximumBootstrapIntentBytes {
				return bad()
			}
			b, e := io.ReadAll(io.LimitReader(reader, maximumBootstrapIntentBytes+1))
			if e != nil || int64(len(b)) != h.Size {
				return bad()
			}
			result = BootstrapIntent{Contents: b, SHA256: wire.Digest(b), Bytes: len(b)}
		default:
			return bad()
		}
	}
	if len(seen) != 3 || result.Bytes == 0 || len(bytes.Trim(raw[len(raw)-buffer.Len():], "\x00")) != 0 {
		return bad()
	}
	return result, nil
}
