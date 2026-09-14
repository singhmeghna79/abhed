// The recorded demo and the deck, on request only.
//
// Everything under /demo/ is served through this function, never as a plain
// static asset: Pages runs a Function before it would serve a file at the same
// path, and env.ASSETS.fetch below is the only way the file gets out. A visitor
// needs a signed link — ?t=<expiry>.<hmac> — minted by deploy/send-demo.sh
// with the same DEMO_SECRET this function holds. The link is time-limited, and
// once it has been presented the token is kept in a cookie scoped to /demo so
// the video element's own requests do not each need it.
//
// What this is not: it is not access control on a secret. The recording shows
// the product; a link that is forwarded shows it to one more person, and the
// expiry bounds how long. It is on request because the founder wants to know
// who asked, which the request form and the send script give him.
const TEXT = { "Content-Type": "text/plain; charset=utf-8" };

async function hmac(secret, msg) {
  const key = await crypto.subtle.importKey("raw", new TextEncoder().encode(secret),
    { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(msg));
  return [...new Uint8Array(sig)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

function same(a, b) {
  if (a.length !== b.length) return false;
  let d = 0;
  for (let i = 0; i < a.length; i++) d |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return d === 0;
}

// A token is "<unix expiry>.<hex hmac of 'demo|<expiry>'>". Returns the expiry
// when valid and unexpired, else 0.
async function verify(token, secret) {
  const m = /^(\d{9,11})\.([0-9a-f]{64})$/.exec(token || "");
  if (!m) return 0;
  const exp = Number(m[1]);
  if (exp * 1000 < Date.now()) return 0;
  const want = await hmac(secret, "demo|" + m[1]);
  return same(want, m[2]) ? exp : 0;
}

function cookieToken(request) {
  const c = request.headers.get("Cookie") || "";
  const m = /(?:^|;\s*)zb_demo=([^;]+)/.exec(c);
  return m ? decodeURIComponent(m[1]) : "";
}

function refused(status, msg) {
  const html = `<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Zybuu demo</title><style>body{margin:0;background:#06090F;color:#E8EEF7;font:16px/1.6 -apple-system,Inter,Segoe UI,system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;padding:24px}
.c{max-width:440px;border:1px solid #182231;border-radius:16px;padding:28px;background:#0D131C}h1{font-size:22px;margin:0 0 10px;letter-spacing:-.02em}p{color:#B0BFD2;margin:0 0 8px}a{color:#3BA9FF}</style>
<div class="c"><h1>${msg}</h1><p>The demo and the deck are shared on request, with a link that expires.</p><p>Ask for a fresh one at <a href="mailto:support@zybuu.com">support@zybuu.com</a>, or through the form on <a href="https://zybuu.com/abhed/#access">zybuu.com/abhed</a>.</p></div>`;
  return new Response(html, { status, headers: { "Content-Type": "text/html; charset=utf-8", "X-Robots-Tag": "noindex, nofollow", "Cache-Control": "no-store" } });
}

export async function onRequest({ request, env }) {
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("Method not allowed", { status: 405, headers: TEXT });
  }
  if (!env.DEMO_SECRET) {
    return new Response("The demo is not configured on this deployment.", { status: 503, headers: TEXT });
  }
  const url = new URL(request.url);
  const presented = url.searchParams.get("t");
  let exp = presented ? await verify(presented, env.DEMO_SECRET) : 0;
  let token = presented;
  if (!exp) {
    token = cookieToken(request);
    exp = await verify(token, env.DEMO_SECRET);
  }
  if (!exp) return refused(403, presented ? "This link has expired." : "This page is by invitation.");

  // Serve the underlying file. The query string is dropped so the asset cache
  // key is the path alone, and a directory request gets its index.
  const clean = new URL(url.pathname, url.origin);
  const res = await env.ASSETS.fetch(new Request(clean.toString(), { method: request.method, headers: request.headers }));
  if (res.status === 404) return refused(404, "There is nothing here.");

  const out = new Response(res.body, res);
  out.headers.set("X-Robots-Tag", "noindex, nofollow, noarchive");
  out.headers.set("Cache-Control", "private, no-store");
  out.headers.set("Referrer-Policy", "no-referrer");
  out.headers.set("Content-Security-Policy",
    "default-src 'none'; media-src 'self'; style-src 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'");
  const maxAge = Math.max(60, exp - Math.floor(Date.now() / 1000));
  out.headers.append("Set-Cookie",
    `zb_demo=${encodeURIComponent(token)}; Path=/demo; Max-Age=${maxAge}; Secure; HttpOnly; SameSite=Lax`);
  return out;
}
