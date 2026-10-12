# Cruuze on Cloudflare

`deploy/cloudflare/` runs Cruuze on Cloudflare Containers: a small Worker
receives every request and passes it to one container running the image built
from the repository's `Dockerfile`. The database and the attachments live
elsewhere, because a container's disk is wiped whenever it restarts.

## What you need

- A Cloudflare account on the Workers Paid plan; Containers are not on the
  free plan.
- Docker running on the machine you deploy from. Wrangler builds the image
  there and uploads it.
- PostgreSQL 14 or newer that the internet can reach, with the `citext`
  extension available. Use its direct connection string, not a pooler in
  transaction mode: the server keeps a `LISTEN` connection open for live sync.
- An R2 bucket for attachments, and an R2 API token (access key and secret)
  that can read and write it.
- A way to send email: a Resend API key or an SMTP account.

## Deploy

```sh
cd deploy/cloudflare
npm install
npx wrangler login
```

Edit `wrangler.jsonc`:

- `APP_URL` and the hostname in `routes`: the address people will open. The
  hostname's zone must be on the Cloudflare account you deploy to. Without
  `routes` and `workers_dev` the address is
  `https://cruuze.<your subdomain>.workers.dev`.
- `MAIL_FROM`, and `S3_ENDPOINT` and `S3_BUCKET` for the R2 bucket.
- `constraints`, to keep the container in the region the database is in.

Put the secrets in a file named `.secrets` next to it (git ignores it):

```sh
DATABASE_URL=postgres://user:password@host:5432/cruuze?sslmode=require
RESEND_API_KEY=...
S3_ACCESS_KEY=...
S3_SECRET_KEY=...
```

Then:

```sh
npx wrangler deploy --secrets-file .secrets
```

The first deploy builds the image, which takes a few minutes (longer on an
Apple Silicon Mac, where it is built for another processor), and Cloudflare
needs a few more minutes before the container answers. Open `APP_URL` and
sign in. **The first person to sign in becomes the instance admin.**

Every variable and secret of the Worker reaches the server as an environment
variable, so anything in `.env.example` at the repository root can be set the
same way: Google sign-in, `METRICS_TOKEN`.

## Change a setting

- A variable: edit `wrangler.jsonc`, then `npx wrangler deploy`. Variables
  changed in the Cloudflare dashboard are overwritten by the next deploy.
- A secret: `npx wrangler secret put NAME`.

The server reads its settings when it starts. Within a minute of either
change the Worker notices and restarts it; open pages reconnect by
themselves.

## Upgrade

```sh
git pull
cd deploy/cloudflare && npm install && npx wrangler deploy
```

This builds the new image and replaces the running container. Database
migrations run when the new one starts. The app is unreachable for a few
seconds in between.

## Operate

- `npx wrangler tail` shows the Worker's log. The server's own log, one JSON
  object per line, is under the container in the Cloudflare dashboard.
- `/readyz` answers 204 when the server and its database are up. The Worker
  asks it every minute, which also starts the container again after a crash
  or after Cloudflare moves it to another machine.
- Back up the database with your database host's own backups.
  `deploy/backup.sh` is for the compose stack in `deploy/`.
- There is one container: the Worker sends every request to it. For more
  load, pick a larger `instance_type`. Running several containers has not
  been tried, so leave `max_instances` at 1.
