# Going live on titan.zybuu.com

Everything that can be done from this machine is done. Four steps remain, and
they are all in a browser or on your router.

## Before anything: two GoDaddy settings

**1. Turn on auto-renew.** Your domain expires **2027-07-03** and auto-renew is
off. The "your domain is hot" banner is partly an upsell, but expiry is real, and
losing `zybuu.com` would cost more than everything else here combined.

**2. Turn on DNSSEC.** You have 5 free credits. It signs your DNS records so they
cannot be forged in transit. One click, no downside.

## Step 1 — Add one DNS record

GoDaddy → Domain → DNS → **Add New Record**:

| Field | Value |
|---|---|
| Type | `A` |
| Name | `titan` |
| Data | `171.76.80.224` |
| TTL | 600 seconds |

**Add only. Change nothing else.** In particular leave alone:

- the `A @ → WebsiteBuilder Site` record — that is your live marketing site
- every `MX`, `TXT` (SPF/DMARC), and `_domainkey` CNAME — that is your email.
  Deleting a DKIM or SPF record silently sends your future mail to spam.

If your public IP has changed since this was written, use the current one:

```bash
curl -s https://api.ipify.org
```

## Step 2 — Forward two ports

Router at `http://192.168.1.1` → port forwarding:

| External | Internal | Protocol |
|---|---|---|
| 80 | `192.168.1.7:80` | TCP |
| 443 | `192.168.1.7:443` | TCP |

Port 80 is not optional. Nothing is served over plain HTTP, but Let's Encrypt's
HTTP-01 challenge needs it, and DNS-01 is unavailable — GoDaddy revoked DNS API
access in 2024 for accounts under ~10 domains.

While you are there, **reserve `192.168.1.7`** as a static DHCP lease for this
Mac, or the forward breaks the next time the address moves.

## Step 3 — Turn the VPN off

An active tunnel carries your outbound traffic and will not carry inbound port
forwards. Certificate issuance fails while everything else looks fine.

## Step 4 — Start it

```bash
cd ~/titan
./deploy/preflight.sh                    # should now say "ready"
podman build -t titan:local -f Dockerfile .
./deploy/run.sh serve -addr 0.0.0.0:8080
sudo caddy run --config deploy/Caddyfile  # sudo: binding 80/443
```

Create your account — there is no public signup:

```bash
podman exec -it titan titan user add yuvraj
```

## Publishing the homepage

`zybuu.com` is a Cloudflare Pages site, separate from Titan: Titan is up only
while this Mac is awake, and the homepage must not be. To publish a change:

```bash
./deploy/publish-site.sh
```

It renders the documentation, checks the access Function, then uploads
`web/zybuu/`:

| Path | What it is |
|---|---|
| `/` | the company homepage — thesis, product verticals |
| `/titan/` | Titan's product page — install, the session replay, limitations |
| `/docs/` | 32 pages generated from `docs/` at publish time |
| `functions/api/access.js` | the access-request endpoint |
| `_headers` | CSP and the rest of the security headers |

The publisher itself deliberately lives in `deploy/` rather than inside
`web/zybuu/`: Pages serves every file it is handed, and `zybuu.com/deploy.sh`
returned 200 for as long as the script sat in the published directory.

Editing the page does not publish it. Until this runs, the repository and the
live site disagree, and the live site is what a reader sees.

### Documentation lives under the product

Canonically `titan.zybuu.com/docs`, because documentation belongs with the
thing it documents — and the same will hold for the next product. But
`titan.zybuu.com` is the tunnel to this Mac, so the hostname is split at the
edge: a Worker answers `/docs*` from Pages, everything else goes down the
tunnel to the console. Docs survive the lid closing; the console does not.

The Worker is deployed separately, and only when it changes:

```bash
cd deploy/docs-worker && npx wrangler@3 deploy
```

One trap worth knowing: the Worker fetches from `zybuu.pages.dev`, not from
`zybuu.com`. A `_redirects` rule on `/docs/*` applies to the project origin as
well as the apex, so pointing the Worker at either one while such a rule exists
makes it fetch a redirect to its own route and loop. There is no `/docs` rule
in `_redirects` for exactly that reason.

The same generated HTML is embedded in the binary, so an air-gapped install
serves its own docs at `/docs` with no route to Cloudflare. `titan serve`
registers that route only when docs were generated before the build.

## The homepage form

`zybuu.com` carries an access-request form backed by a Cloudflare Pages
Function. It needs one secret, set by hand:

**Workers & Pages → zybuu → Settings → Environment variables** →
`RESEND_API_KEY`, from [resend.com](https://resend.com) (free tier is enough).
Optionally `ACCESS_TO` to route requests somewhere other than
`support@zybuu.com`.

Until it is set the form refuses honestly — "the form is not connected yet,
email support@zybuu.com" — rather than accepting a request and dropping it.
Nothing is lost either way, but nothing is emailed either.

### Sending as zybuu.com

Set up, mail goes out from Resend's shared `onboarding@resend.dev` sender.
That works, and it has two costs: the message does not look like it came from
Zybuu, and a shared sending address carries other people's reputation, so it is
likelier to land in spam.

Fixing it is DNS, once. In Resend, add the domain `zybuu.com`; it prints three
records (a DKIM `TXT` at `resend._domainkey`, an SPF `TXT`, and a `MX` for
bounce handling). Add them at GoDaddy — **alongside** the existing records, not
replacing them. The existing `MX` for `smtp.secureserver.net` is what receives
your mail; deleting it stops mail arriving. Resend's bounce `MX` goes on a
subdomain and does not conflict.

Then point the form at the verified address:

```bash
./deploy/set-access-email.sh        # answer the third prompt this time
./deploy/publish-site.sh
```

Verify with `dig +short TXT resend._domainkey.zybuu.com` — an answer means
verified. The form works either way; this only changes what the recipient sees
and how reliably it arrives.

## Verify

```bash
curl -I https://titan.zybuu.com                    # 200, valid cert
curl -I http://titan.zybuu.com                     # 308 → https
curl -s https://titan.zybuu.com/v1/health          # {"status":"ok"}
```

From your phone on mobile data (not wifi), open `https://titan.zybuu.com` —
that is the only real proof inbound works.

Then confirm the LAN is not exposed: from another device on your network,
`curl http://192.168.1.7:8080` must **fail**. Titan binds loopback only; the
proxy is the sole route in.

## What is protecting you

| Layer | Control |
|---|---|
| Filesystem | The agent runs in a container with **no host path mounted**. `/Users` does not exist to it — verified, not assumed |
| Privilege | uid 10001, `cap-drop ALL`, `no-new-privileges`, read-only rootfs |
| Blast radius | 2 GB memory, 512 pids, throwaway volume, no egress |
| Transport | TLS, HSTS, HTTP→HTTPS redirect |
| Browser | CSP, `frame-ancestors 'none'`, nosniff, no-referrer |
| Auth | Sign-in required on every route; signup disabled; bcrypt; unguessable session IDs |
| Authorization | A client can never widen its own permissions — `bypass` is refused even when signed in |
| Abuse | Rate limits on sign-in, body caps, read/idle timeouts |
| Policy | Config mounted at the managed path, so it cannot be escalated from inside |

## If it does not work

**Certificate fails.** Almost always port 80. Check the forward, check the VPN,
and confirm `dig +short titan.zybuu.com` returns your current public IP. Let's
Encrypt rate-limits failures, so fix the cause before retrying.

**Site loads but the console will not sign in.** Check `allowed_origins` in
`deploy/config.json` matches the exact hostname you are visiting.

**Everything worked, then stopped.** Your residential IP probably rotated. Check
`curl -s https://api.ipify.org` against the A record.

## The two limits worth remembering

**The site is up only while this Mac is awake and online.** A laptop is not a
server; when you want real uptime, the same container runs unchanged on a VPS.

**Your IP is dynamic.** When it changes, the A record is stale until you update
it. A DDNS updater would automate that, and is worth adding before you send the
link to a customer.
