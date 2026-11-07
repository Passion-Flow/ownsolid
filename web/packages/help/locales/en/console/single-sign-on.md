# Single sign-on

Single sign-on lets people sign in with an account on your company's Gitea instead of a password
kept by ownsolid. The [Single sign-on](/sso) page manages the connectors.

## Adding a connector

1. Register an OAuth application on your Gitea with the callback URLs the dialog shows.
2. Choose **Add connector** and enter the provider address, client ID and secret. A new connector
   starts turned off.
3. Open the connector and run **Test connection**. It names what Gitea refused, such as the client
   ID, the secret or a callback URL that is not registered, or says that Gitea cannot be reached or
   trusted.
4. Turn the connector on, with a reason.

Single sign-on must also be on for the installation and the provider must be approved there; the
page says when it is not.

## Turning a connector off

Turning a connector off asks you to type its name. It ends the sessions that started through it,
and they do not come back, so there is no undo.
