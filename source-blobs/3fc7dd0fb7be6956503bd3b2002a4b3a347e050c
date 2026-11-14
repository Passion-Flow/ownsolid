package inference

import (
	"net/netip"
	"path/filepath"
	"regexp"
	"strconv"

	wire "github.com/Passion-Flow/ownsolid/api/agent/v1"
)

// Bounds of a gateway container's memory. The gateway holds request bodies and its report
// spool in memory budgets of at most 256 MiB each, so less than 64 MiB cannot run it.
const (
	minimumGatewayMemoryBytes int64 = 64 << 20
	maximumGatewayMemoryBytes int64 = 16 << 30
)

// gatewayUser is the account the gateway image runs as (deploy/images/gateway/Dockerfile).
// The agent hands the gateway's private files to it and to nobody else.
const (
	gatewayUID  = 10001
	gatewayGID  = 10001
	gatewayUser = "10001:10001"
)

var imageID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// GatewayConfig is the model gateway a serving node runs beside its model (GW-114, ADR 0052).
// The node declares where callers reach it; control learns that origin when the gateway is
// activated and names it in the gateway's certificate and in its aliases' base URL.
type GatewayConfig struct {
	// ListenAddress is the IPv4 address and port the gateway listens on, for example
	// 0.0.0.0:18443 or the address of the interface callers use.
	ListenAddress string `json:"listen_address"`
	// Origin is the HTTPS origin callers use to reach that listener. It is configured apart
	// from the listener, because a name or a forwarded port may stand in front of it.
	Origin string `json:"origin"`
	// Image is the ID of the gateway image loaded from the offline bundle, sha256:<hex>.
	// The agent runs no other image and never pulls one.
	Image string `json:"image"`
	// RootDirectory holds one private directory per gateway instance: its configuration,
	// credential, key, certificate and report spool.
	RootDirectory string `json:"root_directory"`
	// MemoryBytes caps the gateway container's memory.
	MemoryBytes int64 `json:"memory_bytes"`
}

// Validate checks the configuration without touching the host.
func (c GatewayConfig) Validate() error {
	listen, e := netip.ParseAddrPort(c.ListenAddress)
	if e != nil || !listen.Addr().Is4() || listen.Port() == 0 || listen.Addr().IsMulticast() || listen.Addr() == netip.AddrFrom4([4]byte{255, 255, 255, 255}) || listen.String() != c.ListenAddress {
		return fail("GATEWAY_CONFIGURATION_INVALID")
	}
	// The model's own port on the node's loopback belongs to the engine.
	if int(listen.Port()) == wire.ApprovedInferenceRecipe().HostPort && (listen.Addr().IsLoopback() || listen.Addr().IsUnspecified()) {
		return fail("GATEWAY_CONFIGURATION_INVALID")
	}
	if _, e := wire.ParseGatewayOrigin(c.Origin); e != nil {
		return fail("GATEWAY_CONFIGURATION_INVALID")
	}
	if !imageID.MatchString(c.Image) || !filepath.IsAbs(c.RootDirectory) || filepath.Clean(c.RootDirectory) != c.RootDirectory || c.RootDirectory == "/" || !protectedPath.MatchString(c.RootDirectory) {
		return fail("GATEWAY_CONFIGURATION_INVALID")
	}
	if c.MemoryBytes < minimumGatewayMemoryBytes || c.MemoryBytes > maximumGatewayMemoryBytes {
		return fail("GATEWAY_CONFIGURATION_INVALID")
	}
	return nil
}

// probeAddress is where the agent checks that the gateway serves: the listener itself, or
// the node's loopback when the gateway listens on every address.
func (c GatewayConfig) probeAddress() string {
	listen := netip.MustParseAddrPort(c.ListenAddress)
	if listen.Addr().IsUnspecified() {
		return "127.0.0.1:" + strconv.Itoa(int(listen.Port()))
	}
	return c.ListenAddress
}
