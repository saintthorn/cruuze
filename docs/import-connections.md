# Import connections and resuming

Two things on the import page depend on the server keeping credentials, which
it only does when it has a key to encrypt them with.

## The key

```bash
openssl rand -base64 32
```

Set the result as `SECRETS_KEY`. With it:

- **An import resumes after a restart.** The import's request, token
  included, is kept encrypted beside it while it runs and deleted when it
  ends. A server that shuts down hands its imports on; one that starts picks
  up any that nobody is running, at most three times each. Imports from an
  uploaded file are not kept and do not resume.
- **Trackers can be connected** (below).

Without the key nothing is kept: tokens are used for the one request, and an
import cut off by a restart is marked as stopped, to be run again by hand.

The key is the only way to read what was kept. Lose or change it and running
imports can't resume and every connection has to be made again; nothing else
is affected.

## Connecting a tracker

A connection replaces the pasted token: an admin presses Connect, signs in at
the tracker, and comes back. The tracker's tokens are stored encrypted. A
connection belongs to the admin who made it, in that workspace; other admins
make their own. Removing someone from the workspace removes theirs.

Each tracker needs an OAuth application registered by whoever runs the
server. The redirect address is always

```
{APP_URL}/api/imports/connect/{github|jira|linear}/callback
```

| Tracker | Where to register | Settings |
|---|---|---|
| GitHub | Settings → Developer settings → OAuth Apps | `IMPORT_GITHUB_CLIENT_ID`, `IMPORT_GITHUB_CLIENT_SECRET`. Asks for the `repo` scope, which GitHub requires to read issues of private repositories. github.com only. |
| Jira Cloud | developer.atlassian.com → Developer console → OAuth 2.0 integration | `IMPORT_ATLASSIAN_CLIENT_ID`, `IMPORT_ATLASSIAN_CLIENT_SECRET`. Under Permissions add the Jira API scopes `read:jira-work` and `read:jira-user`; under Authorization add the redirect address; under Distribution turn on sharing if people outside your own Atlassian site will connect. |
| Linear | Settings → API → OAuth applications | `IMPORT_LINEAR_CLIENT_ID`, `IMPORT_LINEAR_CLIENT_SECRET`. Asks for `read`. |

Notes:

- Jira Server and Data Center, and GitHub Enterprise Server, are still
  imported with a token.
- An Atlassian account with several sites is connected to the first Jira
  site Atlassian lists for it.
- Through a Jira connection, sprints are only read if the application was
  also given Jira Software's board and sprint scopes; without them issues
  still arrive, without cycles.
- An import run through a connection is re-run with one click, and resumes
  after a restart, with nothing typed.
