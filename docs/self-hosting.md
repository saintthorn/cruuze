# Self-hosting Cruuze

One Linux machine with Docker is enough: the stack in `deploy/` runs the app,
PostgreSQL and Caddy, which gets and renews the TLS certificate by itself.

## What you need

- A machine with Docker and the compose plugin. 1 GB of memory is plenty for a
  team; a 50,000-issue workspace runs comfortably in 2 GB.
- A domain name whose DNS points at the machine, and ports 80 and 443 open to
  the internet (Let's Encrypt checks the domain over them).
- A way to send email: an SMTP account or a Resend API key. Sign-in is by
  emailed link, so without one you can only read the links from the app's log.

## Install

```sh
git clone <repository URL> cruuze && cd cruuze/deploy
cp .env.example .env
```

Edit `.env`: set `DOMAIN`, a long random `POSTGRES_PASSWORD`
(`openssl rand -hex 24`), and the email settings. Then:

```sh
docker compose up -d --build
```

The first start builds the image from the checkout, which takes a few
minutes. To run a published release instead of building, set `CRUUZE_IMAGE`
and `CRUUZE_VERSION` in `.env` and leave out `--build`.

Open `https://<your domain>` and sign in. **The first person to sign in
becomes the instance admin.** Do that before telling anyone the address.

## Decide who can get in

Under **Settings → Instance** (instance admins only):

- **Who can make an account**: anyone (the default), only people invited to a
  workspace, or invited people plus everyone at domains you list. People who
  already have an account can always sign in.
- **Who can create workspaces**: anyone who can make an account, or only
  instance admins. On a company server you usually want "invited people" and
  "only instance admins": you create the workspace, then invite everyone.

Customers can always sign in to a workspace's portal once that workspace has
opened it; a portal account can't create a workspace on a restricted server.

More admins: list their addresses in `INSTANCE_ADMINS` in `.env` and run
`docker compose up -d`. An install that predates instance admins gets its
earliest account as admin when it upgrades.

## Back up

```sh
./backup.sh                 # into ./backups/<timestamp>/
KEEP=14 ./backup.sh         # and delete backups older than 14 days
```

A backup is a PostgreSQL dump and an archive of the attachments. It is taken
while the app runs. Run it from cron, and copy the directory somewhere that is
not this machine: a backup on the disk it protects is not a backup.

```
15 3 * * * cd /path/to/cruuze/deploy && KEEP=14 ./backup.sh >> backup.log 2>&1
```

If attachments are in S3 (`S3_BUCKET`), the bucket needs its own backup; the
archive will be empty.

Restore, onto the same or a fresh install:

```sh
./restore.sh backups/2026-10-01T031500Z
```

It asks before replacing anything, stops the app while it works, and starts
it again. Try a restore once before you need one.

## Upgrade

```sh
./backup.sh
git pull                       # or change CRUUZE_VERSION in .env
docker compose up -d --build   # without --build when using a released image
```

Database migrations run when the app starts. Browsers pick up the new web app
on their next load and re-download their local copy of the workspace if its
shape changed. To go back, restore the backup taken before the upgrade:
migrations are not reversed automatically.

The running version is shown under Settings → Instance.

## Operate

- `docker compose logs -f app` shows the app's log, one JSON object per line.
  Every response carries `X-Request-Id`, which is on its log line too.
- `docker compose ps` shows health. The app's health check asks `/readyz`,
  which also checks the database.
- Set `METRICS_TOKEN` to serve Prometheus metrics at `/metrics` to requests
  with `Authorization: Bearer <token>`.
- Email not arriving: `docker compose logs app | grep -i mail`. With no SMTP
  host or Resend key the email itself is in the log.
- The certificate: `docker compose logs caddy`. It fails if the domain does
  not resolve to this machine or port 80 is closed.

## Without the bundled web server

Behind your own proxy or load balancer, run only `postgres` and `app`
(`docker compose up -d postgres app`, with the app's port published), proxy
to port 8080, pass `X-Forwarded-For`, and allow WebSocket upgrades on
`/api/workspaces/*/sync/ws`. Keep `TRUST_PROXY=true` only when a proxy you
control sets that header.

With your own PostgreSQL (14 or newer; 18 is what it is tested on), set
`DATABASE_URL` for the app and drop the `postgres` service. The database
needs the `citext` extension, which the first migration creates.

## What is stored where

| What | Where |
|---|---|
| Everything except files | PostgreSQL (`pgdata` volume) |
| Attachments | `appdata` volume, or the S3 bucket |
| TLS certificates | `caddydata` volume (re-issued if lost) |

Nothing leaves the machine except the email you send and webhooks you set up.
There is no telemetry and no licence check.
