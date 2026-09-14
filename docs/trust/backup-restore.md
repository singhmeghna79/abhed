# Backup and restore

How the hosted console's data is backed up today, how to restore it, and the
recovery point that gives you. Written against `deploy/run.sh`'s actual
container topology.

## What exists today

**No automated backups exist today.** `deploy/run.sh` provisions the
database and its volumes for durability across a restart of the container
(that is the stated point of running Postgres at all — "Durability is the
point. With the in-memory store every session and every account vanished on
restart, which is not a property you can ask a user to accept"), but nothing
in this repository schedules a periodic export. Backup, if you want one, is
a `cron` job or equivalent that an operator sets up around the procedure
below — this document describes the mechanism, not a running schedule.

## What to back up

Two things, both named in `deploy/run.sh`:

1. **The database**, via `pg_dump` from the `titan-db` container, connecting
   as `titan_admin` (the provisioning superuser — see
   `docs/trust/security-posture.md` for why the application itself never
   connects this way).
2. **The two named volumes** the container engine manages directly:
   - `titan-workspace` (`$TITAN_VOLUME`) — the files the agent reads and
     writes.
   - `titan-state` (`$TITAN_STATE_VOLUME`) — account and session state kept
     outside the workspace (`deploy/run.sh`'s comment: "Accounts live on
     their own volume rather than in the workspace").

   `titan-db-data` (`$TITAN_DB_VOLUME`), the Postgres data directory itself,
   is what `pg_dump` reads from live and does not need a separate raw-volume
   backup if the `pg_dump` step runs — a logical dump is the recommended
   path because it is consistent and version-portable, where a raw copy of
   the data directory is not.

## How to back up

```bash
# 1. Database — logical dump via pg_dump, run inside the titan-db container.
podman exec -t titan-db pg_dump -U titan_admin -d titan \
  --format=custom --file=/tmp/titan-$(date +%F).dump
podman cp titan-db:/tmp/titan-$(date +%F).dump ./backups/

# 2. Workspace and state volumes — a throwaway helper container tars each
#    volume's contents; no dedicated backup tooling exists, so this is the
#    same pattern deploy/run.sh itself uses for one-off container work.
podman run --rm \
  --volume titan-workspace:/data:ro \
  --volume "$(pwd)/backups":/backup \
  docker.io/library/alpine tar czf /backup/titan-workspace-$(date +%F).tgz -C /data .

podman run --rm \
  --volume titan-state:/data:ro \
  --volume "$(pwd)/backups":/backup \
  docker.io/library/alpine tar czf /backup/titan-state-$(date +%F).tgz -C /data .
```

Substitute `docker` for `podman` if that is the runtime in use — `run.sh`
itself is written to accept either via `TITAN_RUNTIME`.

`deploy/.db-password` and `deploy/.db-admin-password` are not part of this
backup by design: they are regenerable secrets, not data, and a backup
archive is a worse place for a database credential than the mode-0600 file
`run.sh` already keeps them in.

## Restore order

Order matters because the server applies its schema on connect
(`deploy/run.sh`'s comment: "Titan applies its schema on connect
(internal/store/postgres.go), but only once the server is actually accepting
connections"):

1. **Bring up a fresh `titan-db` container** against empty
   `titan-db-data`/replacement volumes, and let `deploy/run.sh` provision
   `titan_admin`/`titan_app` as it normally does on first run.
2. **Restore the database** into it, before starting the Titan server:
   ```bash
   podman cp ./backups/titan-2026-09-14.dump titan-db:/tmp/restore.dump
   podman exec -t titan-db pg_restore -U titan_admin -d titan \
     --clean --if-exists /tmp/restore.dump
   ```
3. **Restore the workspace and state volumes** before the Titan container
   starts, so it never observes a partially-restored workspace:
   ```bash
   podman run --rm \
     --volume titan-workspace:/data \
     --volume "$(pwd)/backups":/backup \
     docker.io/library/alpine sh -c "rm -rf /data/* && tar xzf /backup/titan-workspace-2026-09-14.tgz -C /data"
   # repeat for titan-state
   ```
4. **Start Titan** (`./deploy/run.sh serve -addr 0.0.0.0:8080`) only after
   steps 2 and 3 complete, and confirm with `titan doctor` and the
   `/v1/health` endpoint per `deploy/GO-LIVE.md`'s verify steps.
5. **Re-run `deploy/set-access-email.sh`** if Pages secrets
   (`RESEND_API_KEY`, `ACCESS_TO`) were part of what was lost — they live in
   Cloudflare, not in these volumes, and are unaffected by a database
   restore, but are called out here because an incident that requires a full
   restore may also be one where those were rotated per
   `docs/trust/incident-response.md`.

## Recovery point objective

**Whatever the operator schedules.** Because no automated backup job exists
in this repository today, the RPO is exactly the gap between manual runs of
the procedure above. Run it nightly and the RPO is up to 24 hours of lost
events, accounts, and workspace changes in the worst case; run it hourly and
it is up to an hour. This document does not claim a number because there is
no schedule to point at — stating one would be exactly the kind of
unverifiable claim `README.md`'s evidence-discipline section and
`internal/sitecheck` exist to catch.

Until an automated schedule exists, treat this as: **RPO = however long
since someone last ran the backup command by hand.**
