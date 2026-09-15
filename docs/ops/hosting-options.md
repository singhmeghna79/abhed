# Where the hosted console should run

The invite-only console at `abhed.zybuu.com` runs today on a laptop: two
rootless Podman containers (the server image and Postgres 16) and a
Cloudflare Tunnel connector, with the model served by Ollama on the same
machine. That was the right first deployment, because it cost nothing and
proved the product on the hardware it is designed for. It is the wrong
second one: a laptop sleeps, travels, reboots for updates, and shares its
memory with everything else its owner does. This page compares the places it
could run instead, with costs as of September 2026, and recommends one.

The comparison is shaped by three facts about the deployment:

- **The image is single-architecture, built where it runs.** The Dockerfile
  is a plain two-stage Go build with no `TARGETARCH` handling; `podman build`
  on a host produces that host's architecture. The release workflow builds
  linux/amd64 and linux/arm64 images separately with QEMU. So any host works,
  ARM or x86, as long as the image is built (or pulled) for it.
  `deploy/cloud/bootstrap.sh` builds on the host.
- **The model is the expensive part, not the server.** The server and the
  database are comfortable in 2 GB. A 26B-parameter model is not: it needs a
  GPU, or about 20 GB of RAM and patience on a CPU. Every option below has to
  answer "where does the model run".
- **The console is a demo, not a customer's deployment.** A customer runs
  Abhed on their own hardware against their own model; that is the product.
  The hosted console exists so a prospect can try it before an install. It is
  acceptable for the demo to use a hosted model, provided the deployment page
  in the console says so, which it does: the overview shows the provider and
  model in force.

## The options

| | Host | Monthly | RAM / CPU | Model | Verdict |
|---|---|---|---|---|---|
| A | Oracle Cloud, Always Free, Ampere A1 | $0 | up to 24 GB / 4 OCPU (ARM) | hosted API for the demo | **Recommended** |
| B | Hetzner CAX21 (ARM) or CX32 (x86) | about €7 to €8 | 8 GB / 4 vCPU | hosted API for the demo | Fallback |
| C | GPU box, spot (RunPod, Vast, Lambda) | $70 to $200 | 24 GB VRAM class | the real local model | When a prospect insists on it |
| D | Keep the Mac, made always-on | $0 | 36 GB / M3 | the real local model, locally | Stopgap, this week |

Prices are list prices at the time of writing and move; the shape of the
comparison does not.

**A. Oracle Cloud Always Free.** The free tier includes ARM compute up to
4 OCPU and 24 GB of RAM, 200 GB of block storage, and 10 TB of egress a
month, with no expiry as long as the account stays active. That is more
machine than the console needs and costs nothing. Two honest caveats: free
ARM capacity in a region is frequently exhausted, so provisioning can take
several attempts over days, and an idle free instance can be reclaimed if it
sits below Oracle's utilisation thresholds for a week, which a running server
with a nightly backup does not. The model runs elsewhere: a hosted
OpenAI-compatible API through the provider configuration, keyed by a secret
in `deploy/.env` (read by `run.sh`, never committed). Several providers offer
free or near-free tiers for open-weight models such as Gemma; check the
current limits before choosing, and pick one whose data-use terms say prompts
are not retained for training, since the demo's purpose is to show what a
private deployment feels like.

**B. Hetzner.** The cheapest reliable paid option. CAX21 (4 ARM vCPU, 8 GB)
or CX32 (4 x86 vCPU, 8 GB) at about €7 to €8 a month with 20 TB of traffic.
Provisioning is instant, there is no free-tier reclamation risk, and the
console's own footprint fits with room to spare. The model is hosted, as in A.
This is the fallback if Oracle has no ARM capacity after a few days of
trying, and the better choice if a few euros a month is acceptable in
exchange for never thinking about it again.

**C. A GPU box.** Only this option runs the actual local model the product
is about. Spot pricing for a 24 GB-class GPU sits around $0.10 to $0.30 an
hour; always-on that is $70 to $200 a month, and spot instances can be
reclaimed. Worth it for a specific prospect who wants to see a 26B model
answering from a machine with no internet route, and for benchmark runs.
Not worth it for an always-on demo whose visitors are trying the harness,
not the model.

**D. The Mac, made always-on.** Zero cost and the real model, at the price
of the laptop being a server: sleep disabled, a launchd job restarting the
containers and the tunnel, and the founder unable to close the lid or take
it anywhere. The exact setup is in `deploy/cloud/mac-always-on.md`. It is
the right thing to do this week while A or B is provisioned, and the wrong
thing to still be doing in a month.

## Recommendation

Run the console on **Oracle Always Free ARM with a hosted model for the
demo** (A), and keep the Mac always-on (D) until it is up. If Oracle has no
ARM capacity within a few days, take Hetzner CAX21 (B) and stop trying.
One-time cost: nothing but an afternoon. Monthly: $0 on A, about €7 on B,
plus whatever the hosted model's free tier does not cover, which at demo
traffic is expected to be nothing.

The reasons, in order: it removes the laptop from the path of every prospect
who clicks "Open console"; it costs nothing, which matters to a company that
is pre-revenue; the hosted model is honest for a demo as long as the console
says so, and it does; and the same bootstrap script and runbook move the
deployment again later, to a GPU host or a customer's network, because
nothing in it is specific to Oracle.

What this does not solve: high availability. It is still one host, with a
nightly backup and a documented restore. That is stated on the product page
under limitations and stays there until there are two hosts.

## How to do it

1. `deploy/cloud/bootstrap.sh` prepares a fresh Ubuntu 24.04 host of either
   architecture: packages, a service account with rootless Podman, the
   repository at a chosen ref, the image, the deployment through
   `deploy/run.sh`, the tunnel connector as a system service, and a nightly
   backup timer.
2. `deploy/cloud/migrate-from-mac.md` is the cutover: the final backup on
   the Mac, the restore on the host, the tunnel handover, the checks, and
   the rollback.
3. `deploy/cloud/mac-always-on.md` is the stopgap for the days in between.
