# Keeping the Mac deployment up until the move

The stopgap while a Linux host is provisioned: the laptop stops sleeping,
and three launchd jobs keep the model server, the containers and the tunnel
connector running through reboots and crashes. Everything here is reversible
with the commands at the end.

What it costs you: the laptop cannot sleep, so it must stay on power with
the lid open or an external display attached; it will be warm and the fan
will run when the model is answering; and the 26B model holds roughly 18 GB
of RAM whenever it is loaded. Take it anywhere and the console is down.
That is why this is a stopgap.

## 1. Stop the machine sleeping

```bash
sudo pmset -a sleep 0 disksleep 0 displaysleep 10
sudo pmset -a womp 1 autorestart 1
```

`sleep 0` disables system sleep on both power and battery; `displaysleep 10`
still turns the screen off, which is fine. `womp` is wake-on-network,
`autorestart` brings the machine back after a power failure. Closing the lid
still sleeps a MacBook unless an external display and keyboard are attached;
if you must close it, `sudo pmset -a disablesleep 1` prevents that too, at
the cost of the laptop running with the lid shut, which it is not designed
to do for long. Check with `pmset -g`.

## 2. The launchd jobs

Three property lists live in `deploy/cloud/launchd/`. Each runs as your user
(the containers are rootless and belong to you), starts at login, and is
restarted by launchd if its process exits.

| Label | What it runs | Why |
|---|---|---|
| `com.zybuu.abhed.ollama` | `ollama serve` | the model endpoint the container reaches at `host.containers.internal:11434` |
| `com.zybuu.abhed.containers` | `deploy/cloud/mac-up.sh` every two minutes | starts the Podman machine if it is stopped, runs `deploy/run.sh` if either container is down, exits quietly when all is well |
| `com.zybuu.abhed.cloudflared` | `cloudflared --no-autoupdate tunnel run titan` | the tunnel connector, using `~/.cloudflared/config.yml` |

Install them:

```bash
cd ~/abhed
mkdir -p ~/Library/Logs/abhed ~/Library/LaunchAgents
for p in deploy/cloud/launchd/com.zybuu.abhed.*.plist; do
  sed "s|__HOME__|$HOME|g" "$p" > ~/Library/LaunchAgents/"$(basename "$p")"
  launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/"$(basename "$p")"
done
launchctl list | grep com.zybuu.abhed
```

If a connector is already running from a terminal (`pgrep -fl 'cloudflared
tunnel run'`), stop it first, or two connectors will register for the same
tunnel; harmless but confusing in the logs.

Logs go to `~/Library/Logs/abhed/` (one file per job). The containers job
logs only when it had to do something, so a quiet log is a healthy one.

## 3. Check

```bash
launchctl list | grep com.zybuu.abhed          # three entries, no non-zero exit status
podman ps --format '{{.Names}} {{.Status}}'    # abhed and abhed-db up
curl -s http://127.0.0.1:11434/api/tags | head -c 200   # the model server answers
curl -sS -o /dev/null -w '%{http_code}\n' https://abhed.zybuu.com/   # 200 from outside
```

Reboot once, wait three minutes, and run the same four lines. If the
containers are up, the tunnel answers and you did nothing, the stopgap
works.

## 4. Remove it after the move

```bash
for l in ollama containers cloudflared; do
  launchctl bootout gui/$(id -u)/com.zybuu.abhed.$l
  rm -f ~/Library/LaunchAgents/com.zybuu.abhed.$l.plist
done
sudo pmset -a sleep 1 disksleep 10 disablesleep 0
```

Leave the containers and volumes in place for two weeks after the cutover,
as `migrate-from-mac.md` says; they are the last independent copy of the
data until the new host has its own backup history.
