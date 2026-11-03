package middleware

import (
	"encoding/json"
	"net/netip"
	"strings"

	wire "github.com/Passion-Flow/ownsolid/api/release/v1"
)

// Missing boolean facts must not become a successful false observation. The
// optional platform tenant is present only for an observed installed platform.
func (o *Observation) UnmarshalJSON(raw []byte) error {
	type plain Observation
	var decoded plain
	var members map[string]json.RawMessage
	if wire.StrictJSON(raw, &decoded) != nil || json.Unmarshal(raw, &members) != nil || !Observation(decoded).valid() {
		*o = Observation{}
		return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
	}
	for _, field := range []string{"observed_at", "database", "user", "server_address", "server_port", "version_number", "in_recovery", "tls_version", "tls_cipher", "superuser", "create_role", "create_database", "create_schema", "create_any_schema_objects", "read_all_tables", "write_any_table", "table_count"} {
		if _, ok := members[field]; !ok {
			*o = Observation{}
			return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
		}
	}
	if _, present := members["platform_tenant_id"]; present && !validID(decoded.PlatformTenantID) {
		*o = Observation{}
		return fail("MIDDLEWARE_STORED_EVIDENCE_INVALID")
	}
	*o = Observation(decoded)
	return nil
}

func (o Observation) valid() bool {
	return !o.ObservedAt.IsZero() && profileKey.MatchString(o.Database) && profileKey.MatchString(o.User) && observedHostAddress(o.ServerAddress) && o.ServerPort > 0 && o.ServerPort <= 65535 && o.VersionNumber >= 140000 && o.VersionNumber < 190000 && (o.TLSVersion == "TLSv1.2" || o.TLSVersion == "TLSv1.3") && len(o.TLSCipher) > 0 && len(o.TLSCipher) <= 100 && !strings.ContainsAny(o.TLSCipher, "\x00\r\n") && o.TableCount >= 0 && o.TableCount <= 9007199254740991 && (o.PlatformTenantID == "" || validID(o.PlatformTenantID))
}

func observedHostAddress(value string) bool {
	if address, e := netip.ParseAddr(value); e == nil {
		return address.Zone() == ""
	}
	// The existing inet_server_addr()::text query preserves the full host mask,
	// including /32 or /128. Keep those original bytes, not a rewritten address.
	prefix, e := netip.ParsePrefix(value)
	return e == nil && prefix.Bits() == prefix.Addr().BitLen()
}
