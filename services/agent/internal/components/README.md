# Managed node-exporter lifecycle

This manager owns the admitted node-exporter/systemd component path, including installed version files, unit identity, maintenance and bounded observations. It is not a general package manager or arbitrary systemd controller.

[manager.go](manager.go) validates the configured root/service user and target profile before work. The current preflight requires the explicit Linux amd64 Ubuntu 24.04/root/systemd environment and a real non-root service account. [files.go](files.go) validates installed material; [systemd.go](systemd.go) and maintenance files implement the bounded service operations; [metrics.go](metrics.go) parses the owned exporter observations.

The private journal retains phase, archive/file/unit hashes, previous/candidate ownership, committed task identity and maintenance outcomes. A failed replacement or uncertain system operation must preserve its original recovery state. Never remove a unit or version merely because its name resembles the managed one.

Recovery after a possible unit switch reloads the verified prior unit and restarts
the owned service before accepting health, including when an earlier recovery
already restored the unit file. Same-version metrics cannot establish the running
configuration. A reload/restart failure or cancellation retains the transition
journal for a fresh recovery; a settled journal does not repeat those effects.
Unswitched staging preserves an unchanged healthy process. The tagged maintenance
tests model disk, loaded unit and running configuration separately; they do not
qualify real systemd.

Configuration is components.Config plus the injected [agent policy](../settings/README.md). Synchronize [agent recipes/contracts](../../../../contracts/agent), control/components and nodes, service-user/deployment setup and manager/lifecycle/maintenance tests. Focused check: `go -C services/agent test ./internal/components`; actual systemd, filesystem ownership, version upgrade and maintenance cases require the declared isolated Linux profile. Non-Linux parsing tests do not establish service lifecycle support.
