//go:build linux

package engine

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// All archive and metadata HTTP calls share this original kernel-custody
// bracket; pooled connections to a different daemon are never substituted.
func (c *Client) preparedReleaseChannel(ctx context.Context) (preparedReleaseConnection, error) {
	bad := failure("OFFLINE_ENGINE_PREPARED_TARGET_BACKING_UNPROVEN")
	if c == nil || ctx == nil || c.config.Profile != c.policy.NativeProfile {
		return preparedReleaseConnection{}, bad
	}
	dialer := net.Dialer{Timeout: time.Duration(c.config.DialSeconds) * time.Second}
	var identity *nativePostgreSQLDaemonPeer
	var mu sync.Mutex
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxConnsPerHost: 1, DialContext: func(call context.Context, _, _ string) (net.Conn, error) {
		conn, e := dialer.DialContext(call, "unix", c.socket)
		if e != nil {
			return nil, bad
		}
		unix, ok := conn.(*net.UnixConn)
		if !ok {
			conn.Close()
			return nil, bad
		}
		peer, e := nativePostgreSQLPeerIdentityContext(call, unix, c.socket, c.config.SocketFile)
		mu.Lock()
		defer mu.Unlock()
		if e != nil || nativePostgreSQLBindOriginalPeer(&identity, peer) != nil {
			conn.Close()
			return nil, bad
		}
		return conn, nil
	}}
	copy := *c
	copy.http = &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return bad }}
	proof := func() string {
		mu.Lock()
		defer mu.Unlock()
		if identity == nil {
			return ""
		}
		if identity.ActivatedDaemon != nil {
			return "kernel_original_systemd_dockerd_listener"
		}
		return "kernel_root_dockerd"
	}
	return preparedReleaseConnection{client: &copy, close: transport.CloseIdleConnections, proof: proof}, nil
}
func preparedReleaseStorage(ctx context.Context, channel preparedReleaseConnection, o Ownership) (NativePostgreSQLStorageObservation, error) {
	var out NativePostgreSQLStorageObservation
	bad := failure("OFFLINE_ENGINE_PREPARED_TARGET_BACKING_UNPROVEN")
	if !o.valid() || o.Role != RegistryPreparedReleaseRole || channel.client == nil {
		return out, bad
	}
	c := channel.client
	preflight, e := c.Preflight(ctx)
	if e != nil || preflight.Status != NativeStatus {
		return out, bad
	}
	before, e := c.nativePostgreSQLStorageInfo(ctx)
	if e != nil || wire.Digest([]byte(before.ID)) != preflight.EngineSHA256 {
		return out, bad
	}
	root, e := nativePostgreSQLStorageDirectory(before.DockerRootDir)
	if e != nil {
		return out, e
	}
	allocation := filepath.Join(before.DockerRootDir, "volumes")
	volumes, e := nativePostgreSQLStorageDirectory(allocation)
	if e != nil || root.Device != volumes.Device || root.TotalBytes != volumes.TotalBytes || root.AllocationUnitBytes != volumes.AllocationUnitBytes {
		return out, bad
	}
	if e = nativePostgreSQLStorageMountLayout(allocation); e != nil {
		return out, e
	}
	mount, e := c.nativePostgreSQLStorageVolume(ctx, o, allocation)
	if e != nil {
		return out, e
	}
	target, e := nativePostgreSQLStorageDirectory(mount)
	if e != nil || target.Device != volumes.Device || target.TotalBytes != volumes.TotalBytes || target.AllocationUnitBytes != volumes.AllocationUnitBytes {
		return out, bad
	}
	again, e := c.nativePostgreSQLStorageVolume(ctx, o, allocation)
	lastTarget, te := nativePostgreSQLStorageDirectory(again)
	if e != nil || te != nil || again != mount || !nativePostgreSQLSameStorageIdentity(target, lastTarget) {
		return out, bad
	}
	after, e := c.nativePostgreSQLStorageInfo(ctx)
	lastRoot, re := nativePostgreSQLStorageDirectory(before.DockerRootDir)
	lastVolumes, ve := nativePostgreSQLStorageDirectory(allocation)
	if e != nil || re != nil || ve != nil || !reflect.DeepEqual(before, after) || !nativePostgreSQLSameStorageIdentity(root, lastRoot) || !nativePostgreSQLSameStorageIdentity(volumes, lastVolumes) {
		return out, bad
	}
	if e = nativePostgreSQLStorageMountLayout(allocation); e != nil {
		return out, e
	}
	// A final metadata request also forces a fresh, identically proven connection.
	if _, found, e := c.InspectVolume(ctx, o); e != nil || !found {
		return out, bad
	}
	target.AvailableBytes = min(target.AvailableBytes, lastTarget.AvailableBytes, volumes.AvailableBytes, lastVolumes.AvailableBytes)
	volumes.AvailableBytes = min(volumes.AvailableBytes, lastVolumes.AvailableBytes, target.AvailableBytes)
	out = NativePostgreSQLStorageObservation{Status: NativePostgreSQLStorageObserved, PeerProof: channel.proof(), EngineSHA256: preflight.EngineSHA256, EndpointSHA256: preflight.EndpointSHA256, Filesystem: volumes, AllocationRoot: allocation, SourceOwners: []NativePostgreSQLStorageOwner{{Owner: o, Filesystem: target, Mountpoint: mount}}, Qualification: "readonly_original_kernel_daemon_and_actual_single_registry_release_volume_not_reserved_capacity_or_application_acceptance"}
	if out.PeerProof == "" {
		return NativePostgreSQLStorageObservation{}, bad
	}
	return out, nil
}
func preparedReleaseCapacityNow(owner NativePostgreSQLStorageOwner, reserve, chunk int64) error {
	fs, e := nativePostgreSQLStorageDirectory(owner.Mountpoint)
	if e != nil || !nativePostgreSQLSameStorageIdentity(fs, owner.Filesystem) || fs.AvailableBytes < reserve || fs.AvailableBytes-reserve < chunk {
		return failure("OFFLINE_ENGINE_PREPARED_TARGET_CAPACITY_CHANGED")
	}
	return nil
}
