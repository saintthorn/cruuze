# Cruuze

An issue tracker in the style of Linear. Go API, Vue 3 web app, PostgreSQL.

## Layout

| Path | What |
|---|---|
| `api/openapi.yaml` | The HTTP API contract. Go handlers and TypeScript types are generated from it. |
| `server/` | Go API server (chi, pgx, sqlc, goose). |
| `web/` | Vue 3 + TypeScript + Vite + Tailwind web app. |
| `docker-compose.yml` | Postgres and Mailpit for local development. |
| `deploy/` | Everything to run it on your own machine: compose stack with TLS, backup and restore. See [docs/self-hosting.md](docs/self-hosting.md). `deploy/cloudflare/` runs it on Cloudflare Containers instead: [docs/cloudflare.md](docs/cloudflare.md). |

## How sync works

The web app keeps a full local copy of the open workspace and reads only from
it, so the UI never waits on the network.

- **Sync log.** Every change to a synced record (users, teams, team members,
  workflow states, labels, issues, comments, reactions, activity, attachments,
  notifications, projects, milestones, cycles, relations, templates, views,
  favorites, pull requests, time entries, initiatives, project dependencies and
  updates, customer requests) appends a row to `sync_actions` in the same
  transaction, with a per-workspace sync ID that is gapless and in commit order.
- **Reading.** A client loads `GET /workspaces/{id}/sync/bootstrap` once, caches
  it in IndexedDB, then holds a WebSocket at `/workspaces/{id}/sync/ws?after=N`
  that replays the log after its last sync ID and pushes new entries as they
  commit (Postgres `NOTIFY` wakes the sockets). The log is filtered per user, so
  private teams stay private and notifications reach only their recipient.
  When a client's access changes (its role, a team's privacy, its membership),
  or it is further behind than the log reaches (30 days), the server tells it
  to bootstrap again. A client that was removed from the workspace finds out
  the same way and drops its local copy.
- **Discussions load on demand.** Comments, reactions and activity grow without
  bound, so the bootstrap leaves them out (except the comments the inbox
  quotes). Opening an issue fetches its discussion once from
  `GET /issues/{id}/details`; after that the socket keeps it current like
  everything else.
- **Writing.** Edits apply locally at once and wait in an IndexedDB outbox until
  the server confirms them, surviving reloads and dropped connections. What the
  UI shows is the confirmed state with pending edits replayed on top, so a
  rejected edit is undone by dropping it. Concurrent edits to different fields
  both apply; the last write to the same field wins.

The server side lives in `server/internal/syncer`; the client side in
`web/src/sync`. Any new write to a synced table must call `syncer.Record` in
its transaction.

## Cycles

A team with a cycle length set gets its cycles from the server: a job that runs
at startup and then hourly keeps a current cycle and the next two in place, and
moves unfinished issues out of a cycle once it has ended. Cycle days are UTC
calendar days. The job takes a per-team advisory lock, so several server
instances can run it at once.

## Text and files

Descriptions and comments are stored as Markdown. A mention is a link to
`mention://user/{id}`, which is how the server finds who to notify.

Attachments are uploaded through the API (25 MB each) and served back through
it, so the same access rules apply as to the issue. The bytes go to an
S3-compatible bucket when `S3_BUCKET` is set, and to `STORAGE_DIR` on local disk
otherwise. Only PNG, JPEG, GIF and WebP are shown in the browser; every other
type is sent as a download.

## Search, API and integrations

**Search** (`/` in the app) is Postgres full-text search over issue titles,
descriptions and comments. Words match by prefix and by stem, and every word
has to appear in the same issue or the same comment.

**API keys** (Settings → API) let a script call the same API the web app uses,
described in `api/openapi.yaml`. A key acts as the person who made it, in one
workspace:

```sh
curl -H "Authorization: Bearer cruuze_..." https://your-host/api/me
```

**Assistants (MCP).** The server speaks the Model Context Protocol at
`/api/mcp`, so an assistant such as Claude can work in a workspace: list,
search, read, create and change issues, comment, log time, and read projects
with their forecasts, a team's flow numbers and customer requests. It
connects with an API key and acts as the key's owner, so it sees and can do
exactly what that person can, through the same code paths as the web app;
nothing it does can delete anything. With Claude Code:

```sh
claude mcp add --transport http cruuze https://your-host/api/mcp \
  --header "Authorization: Bearer cruuze_..."
```

Settings → API shows this command with a new key filled in. Tools take names
(a status, a person, a label, a project) rather than IDs, and answer a wrong
name with the valid ones. The protocol layer is `server/internal/mcp`; the
tools are in `server/internal/server/mcp_tools.go`.

**Webhooks** (Settings → API, admins) POST a JSON body to your URL when an
issue, comment, project, milestone, cycle, label, attachment or pull request
is created, updated or removed:

```json
{ "action": "update", "type": "Issue", "data": { "id": "...", "title": "..." },
  "workspaceId": "...", "syncId": 123, "createdAt": "2026-10-01T12:00:00Z" }
```

- `Cruuze-Signature: sha256=<hex>` is the HMAC-SHA256 of the raw body under the
  webhook's secret. Verify it before trusting a request.
- `Cruuze-Delivery` is the same on every retry of a delivery, so you can drop
  duplicates. `syncId` orders changes; deliveries can arrive out of order.
- Anything but a 2xx answer within 10 seconds is retried after 1 minute, 5
  minutes, 30 minutes and 2 hours, then given up on.
- Webhooks never carry private teams' records unless they are set up for that
  team, and never anyone's notifications, favorites or private views.
- URLs must be `https://` and resolve to a public address. For local
  development set `WEBHOOK_ALLOW_PRIVATE=true`.

**GitHub** (Settings → Integrations, admins): add a webhook in GitHub with the
payload URL and secret shown there, content type `application/json`, and the
"Pull requests", "Branch or tag creation" and "Pushes" events. See
"Issues that update themselves" below for what it does.

**Importers** (Settings → Import, admins) bring a team's work in from GitHub
Issues, Linear, Jira (Cloud, Server or Data Center), a CSV export, or a JSON
file in [the import format](docs/import-format.md). Issues keep their numbers,
dates and status history, so links and forecasts carry over; running an
import again updates instead of duplicating; and an import can be undone.
Every source is translated into one neutral bundle (`server/internal/importer`)
that a single loader writes, so a new source is only "fetch and translate".
Imports run in the background, write no per-record sync entries (clients
reload the workspace when one finishes) and send no notifications. A pasted
link is enough to start one (a public GitHub repository needs nothing else),
with every team taken and people matched automatically; "Review first" shows
the choices instead. The source is read ahead of the writing, attachments are
copied a few at a time behind both, and once a GitHub or Jira import has run
to the end, the next one reads only what changed since (`full: true`, or
unticking "Changes only", reads everything again). With a `SECRETS_KEY`, an
import that was running when the server restarted carries on by itself, and
GitHub, Jira Cloud and Linear can be connected through their own sign-in
pages instead of a pasted token ([setting that up](docs/import-connections.md)).

**CSV import** (team settings) creates an issue per row. `Title` is required;
`Description`, `Status`, `Priority`, `Assignee` (email or name), `Labels` and
`Due date` are read when present. Up to 2,000 rows and 5 MB.
Export writes the same columns.

**Email notifications**: a notification still unread after
`NOTIFICATION_EMAIL_DELAY` (default `5m`; a negative value turns them off) is
emailed, several at once as a digest. People opt out under Settings → Account.
Email goes through Resend when `RESEND_API_KEY` is set (for hosts that block
SMTP), through SMTP when `SMTP_HOST` is set, and to the server log otherwise.

## Statuses, speed and the archive

Each team has its own statuses (team settings → Workflow): add, rename,
reorder and remove them within the six kinds the app understands (triage,
backlog, unstarted, started, completed, canceled). The database stamps
`started_at` and `completed_at` on an issue and keeps a history of every
status it has been in, from a trigger, so no code path can skip it. Cycle
time, time in progress and forecasts all read that history.

The app is built to stay fast at 50,000 issues in a workspace
(`server/cmd/seed` makes one to test with):

- Lists draw only the rows on screen; boards load a column 60 cards at a time.
- Closed issues are archived after a team's "archive after" days (default 90)
  and leave the local copy, which is what keeps it bounded. They stay
  searchable, open by their address, and can be unarchived.
- Edits to many issues go to the server as one request (`POST /issues/batch`).
- A service worker caches the app itself, so it starts offline from the local
  copy; the editor loads only when a description is opened.

There is no query language. Filters are point-and-click and combine with AND.

## Issues that update themselves

With GitHub connected, an issue's key in a branch name, pull request or
commit message is enough:

| On GitHub | The issue |
|---|---|
| A branch named for the issue is pushed | moves to the first started status |
| A pull request opens (or leaves draft) | is linked; moves to started |
| Review is requested | moves to the team's review status, if one is set |
| The last open pull request merges | is completed |
| A commit on the default branch says `fixes ENG-12` | is completed |

Whoever did it on GitHub is credited when their GitHub username is set under
Settings → Account. Each team also has switches (team settings → Automation),
all on by default: starting an unassigned issue assigns it to you, a started
issue joins the current cycle, and a parent closes when its last sub-issue
does. There is no rule builder.

## Keeping the backlog honest

Each team's **Cleanup** page lists open issues that need a decision: untouched
for the team's "stale after" days (default 60), started but stuck, started but
waiting on an issue that is still open, in progress with no owner, or overdue. Each has a
one-click answer (keep, archive, take it, push a week). The new-issue dialog
shows similar open issues while you type, to stop duplicates at the door.

A team can also have unstarted issues that nobody has touched for 90, 180 or
365 days canceled automatically (team settings → Cleanup; off by default).
Work in progress and triage are never canceled, the people following an issue
are told, and reopening it is one click.

## Insights and forecasts

**Insights** (per team) shows cycle time (median and 85th percentile),
throughput per week, the age of work in progress, and cumulative flow, all
from the status history. They are team-level on purpose: there are no
per-person numbers anywhere.

**Forecasts** on projects, milestones and cycles answer "when will this be
done?" by simulation: the remaining issues are played forward 5,000 times
against the team's own completed-per-week and added-per-week history, giving
the dates by which it finishes in 50%, 85% and 95% of runs. No estimates are
needed. A forecast appears once there are four weeks of history with
completions, and says how well past forecasts held up.

## Time

Time **in progress** is derived from the status history and needs nothing
from anyone. People who want more can start a timer on an issue (one at a
time; it shows in the status line) or log time by hand ("1h 30m", "90m",
"1:30"). You see your own entries; everyone sees the total on the issue;
Settings → General exports a timesheet CSV: your own, or everyone's for admins.

## Planning across projects

- **Initiatives** group projects under a goal with a target date.
- **Dependencies**: a project can wait on other projects; loops are refused.
  A project is flagged when it is due before something it waits on, or waits
  on something late, off track or canceled.
- **Updates**: a dated note saying a project is on track, at risk or off
  track, written in Markdown. The newest is the project's health.
- The **roadmap** lays projects out by initiative on a timeline. Drag a bar
  to move a project, or an end to change one date (Shift+arrow moves it a
  week from the keyboard). Lines join each project to the ones waiting on it,
  and turn amber where the plan can't work.

## Customer requests

A workspace can open a **portal** (Settings → General) at `/portal/<workspace>`:
a public page where customers sign in with an emailed link and ask for
things. They see only their own requests and the team's answers. The team
works them under **Requests**: set a status, write a response, and link the
request to the issue that would deliver it (or create one from it). Requests
can also be noted down by hand for a customer who emailed.

Completing the linked issue marks the request shipped, and a few minutes
later the requester is emailed, once. With the **public board** on, requests
the team marks public are listed on the portal for others to vote on; voters
are told when it ships too. Guests of the workspace never see customers'
requests.

## Teams that aren't engineering

A team has a **kind**: engineering, support, marketing, design or general.
The kind sets the statuses a new team starts with (support gets a triage
queue and "Waiting on Customer"; marketing runs from Ideas to Published) and
what the app offers it: non-engineering teams get a **Calendar** of work by
due date, where dragging an item changes its date, and don't see branch
names. Everything stays editable afterwards.

**Focus** is each person's own page: what you have started, what to pick up
next and why (it unblocks someone, it is late, it is in this cycle), and what
is stuck behind someone else. Nobody else sees it.

## Develop

Requires Go 1.27+, Node 22+ and Docker (or your own Postgres; 18 is what it is tested on).

```bash
make setup    # install web dependencies, create .env
make up       # start Postgres and Mailpit
make server   # API on http://localhost:8080 (applies migrations on start)
make web      # web app on http://localhost:5173
```

Docker exposes Postgres on `localhost:5433`, leaving port `5432` available for
another local Postgres installation. Connections between containers use `postgres:5432`.

Open http://localhost:5173 and sign in with any email address. The sign-in
email lands in Mailpit at http://localhost:8025. If `SMTP_HOST` is empty the
server logs the email instead.

Google sign-in appears once `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` are
set. Register `{APP_URL}/api/auth/google/callback` as the redirect URI.

## Test

```bash
export TEST_DATABASE_URL='postgres://cruuze:cruuze@localhost:5433/cruuze_test?sslmode=disable'
make test
```

Server integration tests run against a real Postgres database and are skipped
when `TEST_DATABASE_URL` is unset. They truncate every table, so point it at a
disposable database (`createdb cruuze_test`), never at your development one.

Browser tests (Playwright, in `web/e2e`) drive the real app end to end:

```bash
cd web && npx playwright install chromium    # once
export E2E_DATABASE_URL='postgres://cruuze:cruuze@localhost:5433/cruuze_e2e?sslmode=disable'
make e2e
```

They start their own API (port 8081) and web server (port 5174), so a running
development server is left alone. The API writes email to `web/e2e/.mail`
(`MAIL_DIR`) instead of sending it, which is where the tests find their sign-in
links. Each test makes its own users and workspace and leaves them behind, so
give the tests a database of their own (`createdb cruuze_e2e`).

## Change the API or schema

- **API:** edit `api/openapi.yaml`, run `make generate`, implement the new
  method on `server.Server`.
- **Schema:** add a goose migration in `server/internal/db/migrations/`.
- **Queries:** edit `server/internal/db/queries/*.sql`, run `make generate`.

Generated code is committed; CI fails if it is stale.

## Build and deploy

```bash
make build    # server/bin/cruuze, with the web app embedded
make docker   # the same as a container image, cruuze:latest
```

The binary serves the API under `/api` and the web app everywhere else, and
applies database migrations when it starts. To try the image with the
docker-compose services: `docker compose --profile app up --build`.

To run it for real on your own machine, with TLS, backups and upgrades, follow
[docs/self-hosting.md](docs/self-hosting.md). Pushing a tag like `v1.2.0`
publishes a multi-architecture image to GitHub's registry
(`.github/workflows/release.yml`). To run it on Cloudflare Containers instead,
follow [docs/cloudflare.md](docs/cloudflare.md).

What a deployment needs:

| Variable | |
|---|---|
| `DATABASE_URL` | Postgres connection string. Required. |
| `APP_URL` | The public origin, e.g. `https://cruuze.example.com`. Used in emailed links and to reject cross-site requests; `https` also turns on secure cookies and HSTS. |
| `PORT` | Port to listen on; defaults to 8080. (`LISTEN_ADDR` overrides it.) |
| `RESEND_API_KEY` or `SMTP_*`, and `MAIL_FROM` | How email is sent. Without either, sign-in links only go to the log. |
| `S3_*` | Where attachments go. Without `S3_BUCKET` they are files under `STORAGE_DIR` (`/data/attachments` in the image), which needs a volume to survive a redeploy. |
| `SECRETS_KEY` | 32 random bytes in base64 (`openssl rand -base64 32`). Encrypts the import credentials the server keeps: a running import's, so it resumes after a restart, and connected trackers' tokens. Without it none are kept. |
| `IMPORT_GITHUB_*`, `IMPORT_ATLASSIAN_*`, `IMPORT_LINEAR_*` (`_CLIENT_ID`, `_CLIENT_SECRET`) | OAuth applications for connecting those trackers on the import page. Optional; each needs `SECRETS_KEY`. See [docs/import-connections.md](docs/import-connections.md). |
| `TRUST_PROXY=true` | Set when behind a reverse proxy or load balancer that sets `X-Real-IP` / `X-Forwarded-For`, so rate limits and logs see the client's address. Leave unset otherwise: the headers could be forged. |
| `LOG_FORMAT=json` | One JSON object per log line (the image's default). |
| `METRICS_TOKEN` | Turns on `/metrics` (Prometheus format) for requests with `Authorization: Bearer <token>`. |
| `RATE_LIMIT_PER_MINUTE` | API requests allowed per client per minute; default 1200, `0` for no limit. |
| `IMPORT_ALLOW_PRIVATE=true` | Lets importers read from private and loopback addresses, for a Jira Server or GitHub Enterprise inside your network. Off by default, like the same check on webhooks. |
| `INSTANCE_ADMINS` | Comma-separated addresses that administer the install, besides the first person who signed in. They get Settings → Instance: who can make an account and who can create workspaces. |

Operating it:

- `/healthz` says the process is up; `/readyz` also checks the database. Use
  `/readyz` as the platform's health check. In a container without a shell,
  `/cruuze healthcheck` does the same and is what the image's `HEALTHCHECK` runs.
- Every response carries `X-Request-Id`, which is also on its log line.
- Several instances can run at once: the background jobs (cycle upkeep,
  webhook delivery, notification emails, archiving, shipped notices)
  coordinate through the database.
- Back up Postgres, and the attachment bucket or volume. Nothing else holds state.

## License

MIT. See [LICENSE](LICENSE).
