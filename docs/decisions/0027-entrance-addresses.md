# 27. Entrance addresses

Date: 2026-11-06

## Status

Accepted.

## Context

ownsolid is reached by four audiences that have nothing in common but the product:

| Entrance | Who | Authentication | Exposure a customer usually wants |
| --- | --- | --- | --- |
| Admin console | platform, tenant and workspace administrators, operators | browser session, audience `admin` | an operations network only |
| Portal | people who use the applications and models | browser session, audience `portal` | the whole company |
| Model gateway | customer code, SDKs, coding tools | API key | application servers |
| Agent endpoint | nodes dialling out to the control plane | mTLS client certificate | the compute network only |

The inherited implementation already separates the two browser entrances: `origins.admin`
and `origins.portal` must differ, the Host header selects the audience, the session
cookies are named `__Host-admin_session` and `__Host-portal_session`, and a session
issued for one audience is refused on the other. Everything is served by one HTTPS
listener, and the generated certificate carries both names.

Two questions were open: what a deployment is allowed to choose as those addresses, and
whether the four entrances must share one listener.

A browser fact decides most of it. **Cookies are not isolated by port.** A cookie set by
`host:8443` is sent to `host:9443`; the `__Host-` prefix pins a cookie to one host, not
to one port, and `SameSite` treats both ports as the same site. Two entrances that differ
only by port therefore share one cookie jar. The audience check and the CSRF token still
prevent a session from being used on the wrong entrance, but the separation a customer
believes they bought is not the separation they get. Different hostnames give both.

## Decision

**Each entrance has its own hostname. One TLS port is the default.** A deployment
declares four hostnames; the control plane virtual-hosts them on a single listener and
one certificate carries all four names. One port is the easiest thing to get through an
intranet firewall, and the hostname carries the meaning.

**A deployment may give any entrance its own listen address and port** when it wants
network separation, different firewall zones, or to scale the gateway and agent tiers
apart from the console. This is configuration, not a different build.

**Two browser entrances on one hostname, differing only by port, require an explicit
opt-in** that records what it costs: cookies are shared between the two ports, so the
audience check is the only thing keeping the sessions apart. The real case is a
deployment with no DNS at all, reached by IP address. Such a deployment is supported, the
certificate carries the IP as an IP name, and the limitation is stated where the operator
chooses it.

**Path separation on one origin is not supported.** `/admin` and `/portal` on one origin
share a cookie jar and a script origin, so a flaw in either reaches both. The two browser
entrances are never served from one origin.

Addresses are deployment configuration. The product never infers its public address from
a request header; it derives origins, cookie flags and redirects from the declared
configuration, and trusts forwarded headers only from explicitly listed proxy ranges.

## Consequences

- The default install asks for four hostnames instead of one address, and documents the
  DNS records and firewall rules a site needs.
- An IP-only installation stays possible and honest about what it gives up.
- Separating listeners later is a configuration change, not a redesign: the audience is
  already bound to the session rather than to the socket.
- Changing an entrance address after install touches the certificate, the single sign-on
  callbacks and every live session, so it needs a supported procedure rather than an
  edit (DEL-115).

Roadmap: ARC-110 (a listener per entrance), IAM-114 (the shared-hostname opt-in),
DEL-114 (the address and transport matrix in the install configuration), DEL-115
(changing an entrance address).
