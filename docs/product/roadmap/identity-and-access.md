# Identity and access

Accounts, enterprise sign-in, sessions, roles and permissions for people, services and
agents. Every check happens on the server.

- Owns: `services/control/internal/modules/identity`, `sso`, `workspaces`.
- Depends on: architecture-and-scale.

## P1 Enterprise release

- [ ] IAM-101 Local accounts: invitation, login, logout, password reset, lockout (P1) - Catalog: F001
- [ ] IAM-102 Generic OIDC sign-in (replaces the Gitea-only connector) (P1) - Catalog: F445
- [ ] IAM-103 LDAP and Active Directory directory login and group sync (P1) - Catalog: F447
- [ ] IAM-104 SAML 2.0 sign-in (P1) - Catalog: F446
- [ ] IAM-105 MFA with TOTP and recovery codes; step-up for sensitive actions (P1) - Catalog: F434, F456
- [ ] IAM-106 ownsolid as an OIDC provider for catalog apps such as DeepParser (P1)
- [ ] IAM-107 Roles: platform admin, tenant admin, workspace admin, developer, operator, member, viewer, auditor; custom roles (P1) - Catalog: F479
- [ ] IAM-108 Resource-level permissions with inheritance and an explain view (P1) - Catalog: F480, F487
- [ ] IAM-109 Service accounts and scoped API keys with expiry, rotation and revocation (P1) - Catalog: F464, F484
- [ ] IAM-110 Group-to-role mapping from upstream identity with preview (P1) - Catalog: F453
- [ ] IAM-111 Session management: idle and absolute timeout, device list, forced sign-out, revocation propagation (P1) - Catalog: F454, F571
- [ ] IAM-112 Separate Admin and Portal entrances with audience-bound sessions (P1) - Catalog: F424, F432, F545
- [ ] IAM-113 Enable single sign-on from the typed install configuration: `ownsolid-install` has no input for providers, their CA or the keyring, so an installed product cannot turn it on (P1)
- [ ] IAM-114 Refuse two browser entrances on one hostname unless the deployment opts in, because browsers do not isolate cookies by port; the opt-in, the sign-in page and the operator documentation state that the audience check is then the only separation ([ADR 0027](../../decisions/0027-entrance-addresses.md)) (P1)

## P2 Fleet operations

- [ ] IAM-201 SCIM 2.0 provisioning (P2) - Catalog: F450
- [ ] IAM-202 Attribute and relationship-based policies with versioned rules and dry run (P2) - Catalog: F481, F573
- [ ] IAM-203 Approval workflows and two-person review for high-risk actions (P2) - Catalog: F430, F440
- [ ] IAM-204 Just-in-time access and break-glass accounts with automatic expiry (P2) - Catalog: F441, F455
- [ ] IAM-205 Agent identity: on-behalf-of tokens with the user as subject and the agent as actor (P2) - Catalog: F572, F485

## P3 Multi-site

- [ ] IAM-301 Global identity across sites with local fallback when the WAN is down (P3)

## P4 Breadth

- [ ] IAM-401 Kerberos, CAS and WS-Federation (P4) - Catalog: F448, F449

Vendor identity products (Entra ID, Okta, Keycloak, Feishu, DingTalk and others) are in
the compatibility matrix. Catalog rows absorbed: F001, F005 to F008, F424, F432 to F434,
F444 to F465, F478 to F488, F571 to F573, F575.
