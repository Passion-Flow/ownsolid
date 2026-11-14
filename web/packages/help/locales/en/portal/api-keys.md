# My API keys

[My API keys](/keys) lists the keys you issued in the workspace chosen in the switcher. A key lets an
application or a script call one model. It stops working when it expires, when you revoke it, or if you
leave the workspace. Nobody else's keys appear here, and yours do not appear in anyone else's portal.

## Issuing a key

1. Choose **Issue a key**, or press `k`.
2. Name it after what will use it, choose the model, and choose how long it lasts.
3. Give a reason, which is kept in the audit log, then choose **Issue**.
4. Copy the key. It is shown only this once and cannot be read back later.
5. Choose **Done**. The key opens with what you need to call it.

If no model can be chosen, none is published to this workspace yet: ask an administrator to publish one.

If the answer is lost on the way, **Try again** finds the same key, but its secret cannot be shown a second
time. Revoke that key and issue another.

## Calling a model

Open a key to see **Use this key**: the base URL, the model name, and a sample call in cURL, Python and
Node.js. Each sample reads the key from the `OWNSOLID_KEY` environment variable, so set it first and keep the
key out of your code.

The Python and Node.js samples need the `openai` package. If your organisation uses a private certificate
authority, cURL needs `--cacert` with the path of its certificate, Python needs `SSL_CERT_FILE` set to that
path, and Node.js needs `NODE_EXTRA_CA_CERTS` set to it.

## Rotating and revoking

**Rotate** gives the key a new secret, shown once. Choose how long the old secret keeps working, up to seven
days, so you can move what uses it without an interruption. Choose not to keep it if the secret leaked.

**Revoke** stops the key at once. Type its name to confirm.

## Reading your calls

Open a key to read every call made with it, newest first: when it ran, how it ended, the tokens it used and
how long it took.
