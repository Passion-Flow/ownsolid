package v1

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
)

// Agent gateways (ARC-102, ADR 0048). A cell can run several agent gateways in front of one
// control plane. An agent holds one session at a time, on the first gateway of its own order
// that answers; the next gateway in that order is where it goes when that one fails. Every
// gateway serves every node, because tasks reach a gateway through the database, so no
// gateway needs to know which agents the others hold.

// MaxSessionAddresses bounds the gateway addresses one agent is configured with.
const MaxSessionAddresses = 16

// ValidateSessionAddresses checks a list of gateway addresses: each one host:port, with a
// host name or IP address and a port from 1 to 65535, none repeated. An empty list is valid
// and means the session goes to the control origin.
func ValidateSessionAddresses(addresses []string) error {
	bad := errors.New("SESSION_ADDRESSES_INVALID")
	if len(addresses) > MaxSessionAddresses {
		return bad
	}
	seen := make(map[string]bool, len(addresses))
	for _, a := range addresses {
		host, port, e := net.SplitHostPort(a)
		if e != nil || host == "" || len(a) > 261 || strings.ContainsAny(host, "/ \t\r\n%@") || seen[a] {
			return bad
		}
		if n, e := strconv.Atoi(port); e != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return bad
		}
		seen[a] = true
	}
	return nil
}

// SessionAddressOrder returns addresses in the order node tries them. The order is the
// rendezvous (highest random weight) order of the node ID over the addresses: each node gets
// its own order, nodes spread evenly over the gateways, and adding or removing one gateway
// moves only the nodes whose first choice it was or becomes. The input is not changed.
func SessionAddressOrder(node string, addresses []string) []string {
	type weighted struct {
		address string
		weight  uint64
	}
	scored := make([]weighted, len(addresses))
	for i, a := range addresses {
		sum := sha256.Sum256([]byte(a + "\x00" + node))
		scored[i] = weighted{address: a, weight: binary.BigEndian.Uint64(sum[:8])}
	}
	slices.SortFunc(scored, func(x, y weighted) int {
		if x.weight != y.weight {
			if x.weight > y.weight {
				return -1
			}
			return 1
		}
		return strings.Compare(x.address, y.address)
	})
	out := make([]string, len(scored))
	for i, s := range scored {
		out[i] = s.address
	}
	return out
}
