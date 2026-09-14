# Import existing preferences

The exporter reads the original ZIP or a SQLite file in read-only mode. It exports only chat preferences, alert settings and last-send time, saved consumption, saved amount, and Top N. It does not export price history, map files, or `.env`.

The Go importer validates and inserts all rows in one transaction. It skips chat IDs already in PostgreSQL. Repeating the import is safe, but it will not update existing settings.

## Before import

1. Stop the old Python bot. Keep the new bot disabled until the import finishes.
2. Back up PostgreSQL and keep the original ZIP private.
3. Start the chart's PostgreSQL instance with the reviewed credentials.
4. Build the importer with `just build`.

For a cluster database, port-forward it in a separate terminal:

```bash
kubectl -n gasolinerabot port-forward service/gasolinerabot-postgresql 5433:5432
```

From the repository root, use Bash. Enter the same application password stored as `POSTGRES_APP_PASSWORD` in OpenBao:

```bash
export PGHOST=127.0.0.1 PGPORT=5433 PGDATABASE=gasolinerabot PGUSER=gasolinerabot PGSSLMODE=disable
unset DATABASE_URL
IFS= read -r -s -p 'Database application password: ' PGPASSWORD
printf '\n'
export PGPASSWORD
set -o pipefail
python3 scripts/export-preferences.py /private/path/gasolineras-bot.zip |
  bin/gasolinerabot --import-preferences
unset PGPASSWORD
```

The only normal output is the imported row count. Do not run the exporter alone in a recorded terminal, pipe it to `tee`, or commit its output. A ZIP import uses a private temporary directory for the SQLite file and removes it when done.

Verify settings through Telegram after starting the new bot. History starts fresh. Do not run the old and new bot at the same time with the same token.
