# gasolinerabot

A small Go Telegram bot for fuel prices in Spain. Rebuilt from the original Python bot, with the same Spanish commands, buttons, and guided flows. PostgreSQL replaces SQLite.

## User features

- `/start`, `/ajustes`: save a base location, fuels, and search radius, with a map preview.
- `/precios`, `/precio`: rank nearby stations by price, then distance. `/top 1` through `/top 15` control the result count.
- Send a location, or use `/cerca`, for a temporary search. It does not change the saved location.
- `/repostar [destination]`, `/ahorro`: compare fuel price and travel cost, with both one-way and return-trip results. Remembers the last amount and vehicle consumption.
- `/alertas`, `/alertas_periodo`, `/avisos_on`, `/avisos_off`: daily or weekly reports in `Europe/Madrid`.
- `/tendencia`: seven-day price trends. History starts fresh and needs at least two days of data.
- `/cancelar`: cancel a guided flow.
- Main buttons: **⛽ Precios**, **🚗 Repostar**, **⏰ Alertas**, **⚙️ Ajustes**.
- Price results include numbered map images, trend buttons, and Google Maps links.

Fuels: Gasolina 95, Gasolina 98, Diésel / Gasóleo A, Diésel premium, GLP.

## Small design

One Go process handles Telegram long polling, a 30-minute MITECO price cache, image rendering, and a one-minute alert scheduler. PostgreSQL stores preferences and daily station prices. No Python runtime, ORM, Redis, PostGIS, public webhook, or separate worker is needed.

```text
Telegram -> Go bot -> MITECO / OSRM / Nominatim / OSM tiles
               |
               -> PostgreSQL: preferences and price history

GitHub Actions -> GHCR -> image reference in Git -> Argo CD -> Kubernetes
OpenBao -> External Secrets Operator -> Kubernetes Secret -> bot and database
```

The nearest station is the savings baseline. Candidate travel cost is subtracted from savings relative to that baseline, as in the original bot. Destination filtering is approximate direction filtering, not a measured route detour. OSRM supplies road distances when available. Otherwise, the estimate is straight-line distance multiplied by 1.25. Public upstream services can fail or limit requests.

Maps use OSM tiles with attribution and an in-memory cache. A labelled schematic is used if tiles fail. Images are generated in memory and are not retained on disk. Price history is written once per feed refresh, not for each chat query. It retains approximately 30 days.

## Development

Use Go 1.26.7 or later, a C compiler for race tests, PostgreSQL 18, Python 3 for the one-time exporter tests, and Just. Helm and Terraform are needed for infrastructure checks.

```bash
cp .env.example .env
chmod 600 .env
# Set the token and local database URLs in .env.
just run
just build
just check-db
```

Just loads `.env`. The executable reads process environment variables only. Without Just, export them before running `go run ./cmd/gasolinerabot`.

**`TEST_DATABASE_URL` must name a disposable database. Tests truncate its preference and price-history tables.** Without it, Go database tests are skipped. CI always provides a separate PostgreSQL test service. No tests need real Telegram credentials or external map/price APIs.

Database migrations run transactionally at startup, under an advisory lock. The same migrations run before a preferences import. The runtime DB user owns only this application's database and is not a PostgreSQL superuser.

Configuration:

| Variable | Purpose |
| --- | --- |
| `TELEGRAM_BOT_TOKEN` | Required for the bot, not for import |
| `DATABASE_URL` | PostgreSQL connection URI for local or external databases |
| `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER`, `PGPASSWORD`, `PGSSLMODE` | Standard PostgreSQL variables, used by the chart instead of a URI |
| `HEALTH_ADDR` | Health listener, default `:8080` |

`--version` prints the build version without needing credentials. `/healthz` checks the process. `/readyz` checks startup state and the database. Readiness does not test current upstream availability.

## Deployment

The chart is at `infra/chart`. It prepares one bot Deployment and an optional small PostgreSQL 18 StatefulSet in the same namespace. PostgreSQL has a **2 GiB retained PVC**, 128 MiB memory request, and 256 MiB limit. Set `postgresql.storageClass` if the cluster has no default. This is a single database instance, not a high-availability setup.

Both `deployment.enabled` and `postgresql.enabled` remain **false** until you configure secrets and storage. No live deployment was changed during the rebuild.

Add this source and destination to your existing Argo CD Application:

```yaml
source:
  repoURL: https://github.com/kevinpita/gasolinerabot.git
  targetRevision: main
  path: infra/chart
destination:
  server: https://kubernetes.default.svc
  namespace: gasolinerabot
syncPolicy:
  syncOptions:
    - CreateNamespace=true
```

This is a spec fragment, not a full Application. Follow [OpenBao setup](infra/openbao/README.md), then:

1. Set `openbao.enabled: true` and the correct Kubernetes audience. Provision the application policy and role.
2. Store the Telegram and database credentials in `kv/apps/gasolinerabot`.
3. Enable `postgresql.enabled` and sync. Wait for PostgreSQL initialization.
4. Import the old preferences, if wanted, using [the private import procedure](docs/import.md).
5. Set `image.tag` to a published eight-character commit SHA, or set `image.digest`. Enable `deployment.enabled` and sync.
6. Stop the old Python bot before starting the Go bot with the same Telegram token.

The bot runs as UID/GID 10001 with a read-only root filesystem. PostgreSQL runs as UID/GID 999 with persistent data and writable temporary mounts. The chart uses a namespace-only database Service and a NetworkPolicy that permits bot pods only. Your CNI must enforce NetworkPolicy. Internal database traffic does not use TLS. Use an external TLS-enabled database if this does not match your cluster's security requirements.

CI checks Go formatting, tests, vulnerabilities, Helm rendering, and Terraform. It builds and smoke-tests a Linux amd64 image, then publishes `ghcr.io/kevinpita/gasolinerabot:<8-character-commit>` on `main` and version tags. Pull requests do not publish. No token or cluster credentials belong in CI.

An image push alone does not deploy it. Commit the new image reference and sync Argo CD. For private images, configure `imagePullSecrets` or make the GHCR package public.

## Database operations

- Back up the database before rollout and on a regular schedule. A retained PVC is **not a backup**.
- Store backups outside the cluster and test restoration. Backup automation is not included.
- See [database operations](docs/database.md) for backup, restore, and password rotation.
- Monitor PVC usage. Increase the claim size through your storage provider's supported procedure.
- PostgreSQL initialization scripts run only on an empty data directory. Editing a Secret does not rotate an existing database role password.
- Do not change the PostgreSQL major version in place. Use a planned dump/restore or upgrade procedure.

## Intentional limits and fixes

The rewrite preserves the main user experience, not every old bug or message boundary. Spanish labels remain, but validation rejects negative values and unsupported minute-level schedules. Settings changes preserve paused alerts and existing schedules. Maps cap markers at eight, as before.

Conversations are held in memory for 30 minutes. A restart ends an unfinished conversation. Preferences persist. Requests are processed one at a time, which keeps this small bot simple but can delay other requests while routes or maps load.

Alerts retry during their scheduled hour, including after a restart. They do not catch up after that hour. A crash after Telegram accepts a message but before the database records it can cause a duplicate. Multi-message reports can be partially delivered. This is not an exactly-once delivery system.

Chat IDs and saved locations are private data. The ZIP, SQLite database, token, and exported preferences must never be committed. The import tool copies preferences only. It does not import price history or map images.
