# Moving the hosted console from the Mac to a Linux host

The cutover from the laptop deployment to a host prepared by
`deploy/cloud/bootstrap.sh`. Read `docs/ops/hosting-options.md` first for
why and where. Everything below is one afternoon with a short outage; the
order matters, because the database must not be written on two machines at
once, and the tunnel must not be served by two connectors carrying different
data.

What moves, and how each thing survives:

| What | Where it lives | How it moves |
|---|---|---|
| Accounts, invites, sessions, the audit record | Postgres, `abhed-db-data` volume | `pg_dump` custom-format dump, `pg_restore` on the host. Passwords are bcrypt hashes in the `users` table, so every invited user signs in with the same password afterwards. |
| Workspaces and uploads | `abhed-workspace` volume | tar of the volume, untarred on the host |
| Per-user state and keys | `abhed-state` volume | tar of the volume, untarred on the host |
| Database role passwords | `deploy/.db-password`, `deploy/.db-admin-password` (mode 600) | copied to the host's deploy directory before `run.sh` first runs there, so the restored database and the server agree |
| Server configuration | `deploy/config.json` | already in the repository; the host's copy adds the hosted model provider |
| Model API key | `deploy/.env` (mode 600) | created on the host; never existed on the Mac |
| The tunnel | `~/.cloudflared/<tunnel-id>.json` | copied to `/etc/cloudflared/` on the host. The DNS records point at the tunnel, not at a machine, so nothing changes in DNS. |

## Before the day

1. Provision the host and run the bootstrap once with no tunnel
   credentials present:

   ```bash
   sudo ABHED_REF=main ./deploy/cloud/bootstrap.sh
   ```

   It stops before starting the connector and says so. The server is up on
   the host's port 8080 with an empty database, which is fine: the restore
   replaces it.

2. Give the host a model. Edit `~abhed/abhed-config/config.json`: add an
   OpenAI-compatible provider for the hosted API, set `model.default` to it,
   and put its key in `~abhed/abhed-config/.env` as the variable the
   provider's `api_key_env` names:

   ```bash
   sudo -u abhed sh -c 'umask 077; printf "ABHED_MODEL_API_KEY=%s\n" "<key>" > ~/abhed-config/.env'
   ```

   Restart with `sudo ABHED_RESTART=1 ./deploy/cloud/bootstrap.sh` and run
   `sudo abhed-as podman exec abhed abhed doctor`. It must report the
   endpoint, tool calling and the sandbox as ok before you go further.

3. Copy the two database password files from the Mac into the host's
   `~abhed/abhed/deploy/` with mode 600, owned by `abhed`. Then run
   `bootstrap.sh` once more with `ABHED_RESTART=1` so the roles on the host
   take those passwords. Without this step the restored data and the
   server's credentials disagree and nothing can sign in.

4. Sign in to the host's console directly (`ssh -L 8080:127.0.0.1:8080`)
   with a throwaway account created there, run one prompt, and confirm the
   audit record appears. This proves the host end to end before any real
   data touches it.

## On the day

Announce a short outage if anyone is mid-trial; the sequence below takes
about fifteen minutes.

1. **Stop writes on the Mac.** Stop the server container but leave the
   database up:

   ```bash
   podman stop abhed
   ```

   The tunnel now answers 502 for the console, which is the honest state.

2. **Final backup on the Mac**, with the same script the host will run
   nightly:

   ```bash
   ABHED_BACKUP_DIR=~/abhed-cutover ./deploy/cloud/backup.sh
   ```

   It writes `abhed-<stamp>.dump`, `abhed-workspace-<stamp>.tgz` and
   `abhed-state-<stamp>.tgz`.

3. **Copy to the host** (`scp ~/abhed-cutover/* <host>:/tmp/cutover/`), then
   on the host, as root:

   ```bash
   abhed-as podman stop abhed
   abhed-as podman cp /tmp/cutover/abhed-<stamp>.dump abhed-db:/tmp/restore.dump
   abhed-as podman exec abhed-db pg_restore -U abhed_admin -d abhed --clean --if-exists /tmp/restore.dump
   for v in abhed-workspace abhed-state; do
     abhed-as podman run --rm -v "$v":/data -v /tmp/cutover:/backup:ro docker.io/library/postgres:16-alpine \
       sh -c "rm -rf /data/* /data/.[!.]* 2>/dev/null; tar xzf /backup/$v-<stamp>.tgz -C /data"
   done
   abhed-as podman start abhed
   ```

   `--clean --if-exists` drops and recreates each table before loading it,
   so the empty schema the host created is replaced, not merged. Ownership
   inside the volumes is preserved by tar; the service user is uid 10001 on
   both machines.

4. **Check the host before it is public.** Through the SSH tunnel: sign in
   with a real invited account, open an old chat and replay it, run a prompt,
   check the deployment page shows the hosted provider. Then:

   ```bash
   abhed-as podman exec abhed abhed doctor
   ```

5. **Hand over the tunnel.** Copy the credentials and start the connector on
   the host, then stop the Mac's:

   ```bash
   # Mac
   scp ~/.cloudflared/6eae1767-91e8-453f-8766-a329c64a63a1.json <host>:/tmp/
   # host, as root
   install -m 600 /tmp/6eae1767-91e8-453f-8766-a329c64a63a1.json /etc/cloudflared/
   ./deploy/cloud/bootstrap.sh          # now installs and starts cloudflared.service
   # Mac
   launchctl bootout gui/$(id -u)/com.zybuu.abhed.cloudflared 2>/dev/null; pkill -f 'cloudflared tunnel run'
   ```

   For the few seconds both connectors are registered, Cloudflare balances
   between them; the Mac's server is stopped, so a request that lands there
   gets a 502 rather than stale data. Stopping the Mac's connector ends that.

6. **Verify from outside**, from a phone on mobile data or with curl:

   ```bash
   curl -sS -o /dev/null -w '%{http_code}\n' https://abhed.zybuu.com/         # 200
   curl -sS -o /dev/null -w '%{http_code} %{redirect_url}\n' https://titan.zybuu.com/   # 301 to abhed.zybuu.com
   curl -sS -o /dev/null -w '%{http_code}\n' https://abhed.zybuu.com/docs/    # 200
   ```

   Sign in from the public hostname and run one prompt. The audit record for
   it is the proof the cutover is complete.

7. **Confirm the nightly backup** is scheduled on the host:
   `abhed-as systemctl --user list-timers abhed-backup.timer`. Set
   `ABHED_BACKUP_RCLONE_REMOTE` in the unit for an off-host copy; a backup
   on the same disk as the database is a backup against mistakes, not
   against the disk.

## Rollback

Until step 5, nothing public has changed: start the Mac's server container
again (`podman start abhed`) and the console is as it was. After step 5,
rollback is the reverse of step 5: start the Mac's connector, stop the host's.
Any writes made on the host in between are on the host only; take a backup
there before rolling back so they are not lost.

## Afterwards

- Leave the Mac deployment stopped for two weeks, then remove it. Its
  volumes are the last independent copy of the pre-cutover state until the
  host has two weeks of nightly backups.
- The console's deployment page shows the hosted provider and model. A
  prospect who asks why the demo of an on-prem product uses a hosted model
  gets the true answer: the demo runs on a free host with no GPU, and their
  install will not.
- Set `server.canonical_host` on the host exactly as on the Mac
  (`deploy/config.json` already has it), so the old hostname keeps
  redirecting.
