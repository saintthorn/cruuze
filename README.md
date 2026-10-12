# Cruuze

A local-first issue tracker in the style of Linear. Go API, Vue 3 web app, PostgreSQL.

- Instant UI: the app reads from a local copy and syncs over a WebSocket; works offline.
- Teams, cycles, projects, milestones, roadmap, triage, saved views.
- GitHub automation: branches, pull requests and commits move issues.
- Insights, forecasts, time tracking, customer request portal.
- Import from Linear, Jira, GitHub, CSV.
- REST API, webhooks, and a built-in MCP endpoint at `/api/mcp`.

## Run

Needs Go 1.27+, Node 22+ and Docker.

```bash
make setup    # install web dependencies, create .env
make up       # Postgres and Mailpit
make server   # API on :8080
make web      # web app on :5173
```

Open http://localhost:5173 and sign in with any email; the link lands in Mailpit at http://localhost:8025.

## Test

```bash
make test     # server tests need TEST_DATABASE_URL
make e2e
```

After changing `api/openapi.yaml` or the SQL in `server/internal/db`, run `make generate`.

## Deploy

- Your own machine: [docs/self-hosting.md](docs/self-hosting.md)
- Cloudflare Containers: [docs/cloudflare.md](docs/cloudflare.md)

Settings are environment variables; see [.env.example](.env.example).

## License

MIT. See [LICENSE](LICENSE).
