# Model services

[Model services](/services) shows the models the workspace in the switcher serves, and how each is
doing. A deployment runs one model on one whole GPU of one node, and is called by its model and
node. The tiles above the list count deployments by state and filter the list.

## What a node needs

- Ubuntu 24.04 on amd64 with at least 12 GiB of memory, online.
- Rootful Docker, the NVIDIA Container Toolkit, and the serving image from this release's offline
  bundle.
- An NVIDIA GPU with driver 580 or newer, MIG off, at least 8 GiB of memory, and most of it free.

A node serves one deployment at a time, so its other GPUs stay idle while it does.

## Deploying a model

1. Choose **Deploy a model**, here or on a model's page, or press `d`.
2. Choose the model. The page says which GPU it recommends: the eligible one with the most memory
   free. **Advanced settings** lets you choose another.
3. Read **What it takes**, then choose **Deploy**.

The deployment's page follows the work, which continues on the node if you leave. It is running
once the model has answered a test generation.

If nothing can take the model, the page gives each node's reason. Where a node's report is out of
date, **Read GPUs** asks it for a new one, and the page follows the answer.

## Looking after a deployment

- **Start** runs a stopped or failed deployment again.
- **Check** asks the node how the deployment is.
- **Verify** runs a test generation.
- **Stop** ends serving and keeps the deployment, so it can start again. Its GPU is released once
  the node confirms.
- **Remove** deletes its container and releases its GPU. It cannot be brought back.

Stopping and removing name the gateway names and keys that will stop getting answers, and ask you
to type the model's name.

## What is not ready

Replicas and scaling, a choice of engine, autoscaling, rollback, and a deployment's traffic and logs
are shown with the roadmap item that brings each. Until then, a deployment's last task shows what
the agent reported.
