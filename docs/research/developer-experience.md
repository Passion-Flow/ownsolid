# Developer experience module review

- [Kubernetes CLI](https://kubernetes.io/docs/reference/kubectl/generated/)
  exposes server operations with automation-friendly output and a distinct
  API discovery surface. The CLI, public contract and generated clients
  belong to DEV-101 through DEV-103.
- [GitHub's webhook guidance](https://docs.github.com/en/webhooks/using-webhooks/best-practices-for-using-webhooks)
  uses signed deliveries, delivery IDs and bounded receiver response time.
  [Delivery history](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/viewing-webhook-deliveries)
  and [redelivery](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks)
  let administrators diagnose and recover a subscriber. DEV-104 covers
  sending product events. DEV-206 separately covers subscriber verification,
  retained attempt history and authorized replay.
- [JupyterHub KubeSpawner](https://jupyterhub-kubespawner.readthedocs.io/en/stable/spawner.html)
  declares GPU requests at spawn. [VS Code Dev Containers](https://code.visualstudio.com/docs/devcontainers/attach-container)
  attaches to an existing workload. DEV-105 and DEV-106 need workspace
  admission, workload lifetime, scoped credentials and explicit offline
  editor asset delivery.
- [BuildKit](https://docs.docker.com/build/buildkit/) separates the build
  engine, frontend and build context. DEV-107 needs a dedicated isolated
  worker, approved offline bases, and verification before the image enters
  the workspace registry.
- [Kubernetes API deprecation policy](https://kubernetes.io/docs/reference/deprecation-policy/)
  treats server resources and CLI flags as contracts. DEV-204 covers these
  stability rules. [Terraform provider mirroring](https://developer.hashicorp.com/terraform/cli/commands/providers/mirror)
  provides a model for shipping a provider to disconnected installations
  under DEV-201.

These are comparisons of product behavior. This review adds no dependency,
model, or dataset. Individual implementation notes must verify the licence
and offline packaging of the exact selected components.
