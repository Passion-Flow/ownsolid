# Gateway and keys

[Gateway and keys](/gateway) lists the keys that call the models of the workspace in the switcher.
A key calls exactly one model, the name the gateway serves it under, and nothing else. It belongs
to the person who issued it: it stops working when it expires, when it is revoked, or when that
person loses access to the workspace.

The tiles above the list count keys that expire within a week and keys never used, and filter the
list. Open a key to see its details and every call made with it, newest first.

## Publishing a model to the gateway

A key calls a model by the name it is published under. Publish a deployment from its page in
[Model services](/services): the **On the gateway** card offers **Publish to the gateway** once the
deployment is running and a test generation has verified its answer.

1. Choose the name callers send as the model. It is made from the model's name; it uses lower-case
   letters, digits, dots and hyphens, and a name is used only once in a workspace.
2. If no gateway serves the deployment's node yet, give the HTTPS address callers reach it at and a
   name for it. Publishing registers that gateway and shows its credential, gateway ID and node ID
   once: configure the gateway on that node with them.
3. Give a reason, then choose **Publish**.

The card then shows the base URL, a sample call and **Issue a key** for that name. If publishing
fails after the gateway was registered, its credential stays on screen and **Try again** publishes
only. If the answer that carried the credential was lost, revoke that gateway from the dialog and
register another.

**Stop publishing** refuses calls to the name from then on and lists the keys that stop working;
you type the name to confirm. The name cannot be published again, and the deployment keeps running.

## Issuing a key

1. Choose **Issue a key**, or press `k`.
2. Name it after whoever will use it, choose the model, and choose how long it lasts. The longest
   lifetime is set by the installation.
3. Give a reason, then choose **Issue**.
4. Copy the key. It is shown only this once and is never stored in a form anyone can read back.

Send the key as a bearer token. If the answer is lost on the way, choosing **Try again** returns
the same key, but its secret cannot be shown again: revoke it and issue another.

## Rotating a key

**Rotate** gives a key a new secret, shown once, and keeps its name, model and owner. Choose how
long the old secret keeps working, up to seven days, so whoever uses it can switch without an
outage. Choose **Not at all** when the secret leaked. A key can be rotated once; afterwards rotate
the key that replaced it.

## Revoking a key

**Revoke** stops a key at once and for good. You type the key's name to confirm. Calls with it
fail from then on.

## What is not ready

- A key cannot have rate or token limits of its own yet; it uses the workspace's limits, and the
  **Over limit** tile says so.
- The installer does not start the model gateway yet, so no call is made through it on an
  installation until it does.
- A gateway's lost credential cannot be replaced yet: revoking the gateway retires every name it
  serves.

Every publication, issue, rotation and revocation is in the [audit trail](help:audit-trail).
