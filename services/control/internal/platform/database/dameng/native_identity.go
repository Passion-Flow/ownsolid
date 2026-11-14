//go:build cgo && odbc_manager

package dameng

import (
	"context"
	"strings"
)

// NETWORK_PROTOCOL is read on this actual authenticated DM session, not from a
// separately probed socket. Unknown/native spelling is a refusal pending target
// qualification; TCP/IP is never an accepted alias for the local protocol.
const nativeSocketProtocol = "UNIXSOCKET"
const nativeSessionQuery = "SELECT SYS_CONTEXT('USERENV','SESSION_USER'), SYS_CONTEXT('USERENV','CURRENT_SCHEMA'), SESSIONTIMEZONE, SYS_CONTEXT('USERENV','ISDBA'), SYS_CONTEXT('USERENV','NETWORK_PROTOCOL'), SYS_CONTEXT('USERENV','LANGUAGE') FROM DUAL"

func (c *odbcConnection) verifyNativeConnection(ctx context.Context, b *connectionBinding) error {
	_, e := joinODBC(ctx, nil, func() { c.owner.markDraining(c.ownerID) }, func() (any, error) { return nil, odbcVerifyIdentity(c.handle, b.manifest) })
	if e != nil {
		return e
	}
	r, e := c.execute(ctx, nativeSessionQuery, nil, true)
	if e != nil {
		return e
	}
	if r == nil || len(r.rows) != 1 || len(r.rows[0]) != 6 {
		return odbcError("DAMENG_NATIVE_SESSION_INVALID")
	}
	v := make([]string, 6)
	for i, value := range r.rows[0] {
		var ok bool
		v[i], ok = value.(string)
		if !ok {
			return odbcError("DAMENG_NATIVE_SESSION_INVALID")
		}
	}
	if v[0] != b.configuration.User || v[1] != b.configuration.Schema || v[2] != "+00:00" || v[3] != "FALSE" || v[4] != nativeSocketProtocol || !strings.HasSuffix(v[5], ".UTF8") {
		return odbcError("DAMENG_NATIVE_SESSION_INVALID")
	}
	return b.verify(ctx)
}
