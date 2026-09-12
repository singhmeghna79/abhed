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

### Sending as support@zybuu.com

Out of the box mail goes out from Resend's shared `onboarding@resend.dev`. It
delivers, but it does not look like it came from Zybuu, and a shared sending
address carries strangers' reputation.

To send as `support@zybuu.com`, Resend has to prove it is allowed to. That is
three DNS records at GoDaddy, and one of them needs care.

**Why you cannot just change the From address.** Your SPF record ends in
`-all`, which tells receivers to reject anything from a sender not on the list,
and your DMARC is `p=quarantine`, which tells them to act on that. Sending as
the domain before the records exist is *worse* than the status quo — the mail
gets quarantined instead of merely looking generic.

In Resend: **Domains → Add Domain → `zybuu.com`**, then **Auto update**.
Because Cloudflare runs this zone, Resend uses Domain Connect: it opens a
Cloudflare authorization page listing the exact records and adds them for you.
It is a one-time grant, not standing write access.

The records it adds:

| Type | Name | Value | Note |
|---|---|---|---|
| `MX` | `send` | `feedback-smtp.<region>.amazonses.com`, priority 10 | bounce handling |
| `TXT` | `resend._domainkey` | (a long public key) | DKIM signing key |
| `TXT` | `send` | `v=spf1 include:amazonses.com ~all` | SPF for the sending subdomain |

All three sit on `send` or on a dedicated DKIM name, so nothing at the root is
touched. That matters for two reasons:

- The root `MX` (`smtp.secureserver.net`) is what *receives* your mail,
  including the access requests this form sends. It stays as it is.
- The root `SPF` stays a single record. Two `v=spf1` records at the same name
  is a permerror under RFC 7208 and fails SPF for **all** mail from the domain
  — the likeliest way to break working mail while adding a sender. The `send`
  SPF is a different DNS name, so it is a separate scope and does not collide.

Check "DNS only" (grey cloud) on all three if you ever add them by hand. A
proxied mail record does not work.

If your DNS were not on Cloudflare, these would be added manually at the
registrar, and the root-SPF warning above would be the thing to get right.

**Then verify before switching**, because a wrong switch is a silent one:

```bash
./deploy/sitetests/check-mail-dns.sh
```

It checks that the inbound `MX` is still intact, that DKIM resolves, and that
SPF lists Resend *without* having dropped the existing provider. When it is
clean:

```bash
./deploy/set-access-email.sh     # it detects verification and suggests the address
./deploy/publish-site.sh
./deploy/sitetests/check-access-live.sh
```

The requester's address is already the `Reply-To`, so a request arrives looking
like it came from Zybuu and hitting Reply answers the person who sent it.

## Handling an access request

A request arrives at `support@zybuu.com`. The requester has already had an
automatic acknowledgement — sent only if the submission passed screening, so a
stranger cannot use the form to mail arbitrary addresses over our domain.

Read it, decide, then:

```bash
export TITAN_ADMIN_USER=yuvraj TITAN_ADMIN_PASS=...   # or TITAN_SESSION=<cookie>
export RESEND_API_KEY=...

./deploy/grant-access.sh priya@siemens.com "Priya Raman"        # 7 days
./deploy/grant-access.sh priya@siemens.com "Priya Raman" 720    # 30 days
```

It mints a single-use invite on the live console, formats the expiry, and
emails the code with what the account can and cannot do. Without
`RESEND_API_KEY` it prints the code for you to send by hand; the invite is
real either way.

**Why this is not automatic.** An invite is a shell on a machine. Screening
decides who is worth reading; it cannot decide who is worth trusting, and a
convincing lookalike domain costs a few dollars. The decision stays yours —
what the script removes is the tedium, not the judgement.

The account the code creates lands with no groups. It can use Titan Chat and
read its own sessions; it cannot administer the console, change policy, or
invite anyone else. The agent's shell runs in a container with no network and
no host access, so what an invited user can reach is bounded by
`internal/sandbox` rather than by trust.

### Turning it off

An invite expires on its own and is single use, so an unsent or unused code
lapses without action. To cut off an account that already exists:

```bash
titan user list                 # who exists
titan user remove <username>    # the account is gone; their sessions are not
```

There is no `disable` — the subcommands are `add`, `list`, `passwd` and
`remove`. Removing the account ends their access; the audit log of what they
did stays, which is the point of keeping it.

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
