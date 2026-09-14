# Database operations

The chart creates PostgreSQL in namespace `gasolinerabot`. These commands assume release name `gasolinerabot`. Adjust names if needed. The PVC is retained when the StatefulSet is removed. Node or disk loss can still destroy data.

## Back up

Run from a trusted administrator machine:

```bash
umask 077
mkdir -p backups
kubectl -n gasolinerabot exec gasolinerabot-postgresql-0 -- \
  pg_dump -U postgres -d gasolinerabot -Fc > "backups/gasolinerabot-$(date +%Y%m%d-%H%M%S).dump"
```

Check the command exit status. Copy the dump to encrypted storage outside the cluster. Automate this in your existing backup system. This repo does not install a backup scheduler.

## Restore

Stop the bot first. Restore into an empty database owned by the `gasolinerabot` role. For a fresh PostgreSQL instance initialized by this chart:

```bash
kubectl -n gasolinerabot exec -i gasolinerabot-postgresql-0 -- \
  pg_restore -U postgres -d gasolinerabot --exit-on-error < /private/path/backup.dump
```

Do not restore over a running or populated database without a reviewed replacement plan. Test this procedure on a separate database before relying on backups.

## Password rotation

Changing `POSTGRES_PASSWORD` or `POSTGRES_APP_PASSWORD` in OpenBao does not change an initialized PostgreSQL role. Coordinate the database change and the Secret change during maintenance:

1. Stop the bot through your deployment configuration.
2. Open an interactive PostgreSQL session:

   ```bash
   kubectl -n gasolinerabot exec -it gasolinerabot-postgresql-0 -- psql -U postgres
   ```

3. Run `\password gasolinerabot` and enter the new application password at the hidden prompts. Use `\password postgres` separately for the administrator password if needed.
4. Update the matching OpenBao keys without replacing unrelated fields. Wait for ESO to refresh the Kubernetes Secret.
5. Restart PostgreSQL's pod to update its environment when convenient. Stored database passwords have already changed.
6. Start the bot again and check readiness.

The bot uses a non-superuser role that owns its application database. PostgreSQL's local socket is for maintenance inside its pod. Network access to PostgreSQL uses SCRAM passwords. The administrator password is not injected into the bot pod.
