package engine

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

const storeNamespaceVersion = 1
const maximumStoreNamespaceBytes = 4096

var namespaceUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type StoreNamespace struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind,omitempty"`
	StoreID       string `json:"store_id"`
	TenantID      string `json:"tenant_id"`
}
type StoreTaskObservation struct {
	Status          string         `json:"status"`
	Namespace       StoreNamespace `json:"namespace"`
	NamespaceSHA256 string         `json:"namespace_sha256"`
	Qualification   string         `json:"qualification"`
}

func (c *Client) stoppedStorePeers(ctx context.Context, t DatabaseTask) (map[string]RuntimeContainer, error) {
	result := map[string]RuntimeContainer{}
	for role, id := range t.Peers {
		if role == "postgres" {
			continue
		}
		r, found, e := c.InspectRuntime(ctx, t.Owner.ScopeID, role)
		if e != nil || !found || r.ID != id || r.State.Running || r.State.PID != 0 || r.State.Paused || r.State.Restarting || r.State.Dead || r.State.Status != "created" && r.State.Status != "exited" {
			return nil, failure("OFFLINE_ENGINE_STORE_TASK_STOPPED_PEERS_REQUIRED")
		}
		result[role] = r
	}
	return result, nil
}
func (c *Client) InspectStoreTaskResult(ctx context.Context, t DatabaseTask, id string) (StoreTaskObservation, error) {
	p, ok := storeTask(t.Kind)
	if !ok {
		return StoreTaskObservation{}, failure("OFFLINE_ENGINE_STORE_TASK_REQUIRED")
	}
	before, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil {
		return StoreTaskObservation{}, e
	}
	if before.State.Status != "exited" {
		return StoreTaskObservation{}, failure("OFFLINE_ENGINE_EXITED_STORE_TASK_REQUIRED")
	}
	peers, e := c.stoppedStorePeers(ctx, t)
	if e != nil {
		return StoreTaskObservation{}, e
	}
	raw, status, e := c.request(ctx, http.MethodGet, "/v"+c.policy.APIVersion+"/containers/"+id+"/archive", url.Values{"path": {p.destination()}}, nil, c.config.RequestSeconds)
	if e != nil || status != http.StatusOK {
		return StoreTaskObservation{}, failure("OFFLINE_ENGINE_STORE_TASK_READBACK_FAILED")
	}
	result, e := storeTaskArchive(raw, p, t.UID, t.GID)
	if e != nil {
		return result, e
	}
	after, e := c.exactDatabaseTask(ctx, t, id)
	if e != nil || before.State != after.State {
		return StoreTaskObservation{}, failure("OFFLINE_ENGINE_STORE_TASK_CHANGED_DURING_READBACK")
	}
	observed, e := c.stoppedStorePeers(ctx, t)
	if e != nil || len(observed) != len(peers) {
		return StoreTaskObservation{}, failure("OFFLINE_ENGINE_STORE_TASK_PEERS_CHANGED")
	}
	for role, peer := range peers {
		if observed[role].ID != peer.ID || observed[role].State != peer.State {
			return StoreTaskObservation{}, failure("OFFLINE_ENGINE_STORE_TASK_PEERS_CHANGED")
		}
	}
	return result, nil
}
func storeTaskArchive(raw []byte, p storeTaskProfile, uid, gid int) (StoreTaskObservation, error) {
	bad := func() (StoreTaskObservation, error) {
		return StoreTaskObservation{}, failure("OFFLINE_ENGINE_STORE_TASK_NAMESPACE_INVALID")
	}
	if len(raw) < 3*tarBlockBytes || len(raw)%tarBlockBytes != 0 || len(bytes.Trim(raw[len(raw)-tarEndBytes:], "\x00")) != 0 {
		return bad()
	}
	root := path.Base(p.destination())
	prefix := root + "/private"
	children := map[string]bool{}
	for _, n := range p.children {
		children[prefix+"/"+n] = true
	}
	buffer := bytes.NewReader(raw)
	reader := tar.NewReader(buffer)
	seen := map[string]bool{}
	result := StoreTaskObservation{}
	for {
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return bad()
		}
		n := strings.TrimSuffix(h.Name, "/")
		if seen[n] || h.Linkname != "" || len(h.PAXRecords) > 0 {
			return bad()
		}
		seen[n] = true
		switch {
		case n == root:
			if h.Typeflag != tar.TypeDir || h.Mode != 0755 || h.Uid != 0 || h.Gid != 0 || h.Size != 0 {
				return bad()
			}
		case n == prefix || children[n]:
			if h.Typeflag != tar.TypeDir || h.Mode != 0700 || h.Uid != uid || h.Gid != gid || h.Size != 0 {
				return bad()
			}
		case n == prefix+"/owner.lock":
			if h.Typeflag != tar.TypeReg || h.Mode != 0600 || h.Uid != uid || h.Gid != gid || h.Size != 0 {
				return bad()
			}
		case n == prefix+"/namespace.json":
			if h.Typeflag != tar.TypeReg || h.Mode != 0600 || h.Uid != uid || h.Gid != gid || h.Size < 1 || h.Size > maximumStoreNamespaceBytes {
				return bad()
			}
			data, e := io.ReadAll(io.LimitReader(reader, maximumStoreNamespaceBytes+1))
			if e != nil || int64(len(data)) != h.Size || wire.StrictJSON(data, &result.Namespace) != nil {
				return bad()
			}
			result.NamespaceSHA256 = wire.Digest(data)
		default:
			return bad()
		}
	}
	ns := result.Namespace
	expectedKind := p.kind
	if p.kind == "dataset" {
		expectedKind = ""
	}
	if p.kind == "preparation" {
		expectedKind = "model_preparation"
	}
	if len(seen) != len(p.children)+4 || result.NamespaceSHA256 == "" || ns.SchemaVersion != storeNamespaceVersion || ns.Kind != expectedKind || !namespaceUUID.MatchString(ns.StoreID) || !namespaceUUID.MatchString(ns.TenantID) || ns.StoreID == "00000000-0000-0000-0000-000000000000" || ns.TenantID == "00000000-0000-0000-0000-000000000000" || len(bytes.Trim(raw[len(raw)-buffer.Len():], "\x00")) != 0 {
		return bad()
	}
	result.Status = "observed_private_fresh_store_namespace"
	result.Qualification = "private_namespace_bytes_only_not_backend_audit_schema_scope_application_or_target_qualification"
	return result, nil
}

func ValidStoreTaskObservation(kind string, o StoreTaskObservation) bool {
	p, ok := storeTask(kind)
	if !ok {
		return false
	}
	expected := p.kind
	if expected == "dataset" {
		expected = ""
	}
	if expected == "preparation" {
		expected = "model_preparation"
	}
	return o.Status == "observed_private_fresh_store_namespace" && o.Qualification == "private_namespace_bytes_only_not_backend_audit_schema_scope_application_or_target_qualification" && wire.ValidSHA256(o.NamespaceSHA256) && o.Namespace.SchemaVersion == storeNamespaceVersion && o.Namespace.Kind == expected && namespaceUUID.MatchString(o.Namespace.StoreID) && namespaceUUID.MatchString(o.Namespace.TenantID) && o.Namespace.StoreID != "00000000-0000-0000-0000-000000000000" && o.Namespace.TenantID != "00000000-0000-0000-0000-000000000000"
}
