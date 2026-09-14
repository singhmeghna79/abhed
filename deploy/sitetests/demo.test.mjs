// The demo gate, exercised without Cloudflare: web/zybuu/functions/demo/[[path]].js
// is imported directly and given a fake env and a fake ASSETS binding. What is
// pinned here is the contract deploy/send-demo.sh relies on — the token
// format, expiry, the cookie — and the two ways the gate must fail closed:
// no secret configured, and no valid token presented.
//
//   node deploy/sitetests/demo.test.mjs
import { onRequest } from "../../web/zybuu/functions/demo/[[path]].js";
import { createHmac } from "node:crypto";

let failed = 0;
function t(name, got, want) {
  const ok = got === want;
  if (!ok) failed++;
  console.log(`${ok ? "ok  " : "FAIL"} ${name}${ok ? "" : ` — got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`}`);
}

const SECRET = "test-secret-not-the-real-one";
const sign = (exp) => exp + "." + createHmac("sha256", SECRET).update("demo|" + exp).digest("hex");
const ASSETS = {
  fetch: async (req) => {
    const u = new URL(req.url);
    if (u.pathname === "/demo/" || u.pathname === "/demo/index.html") return new Response("<video>", { status: 200, headers: { "Content-Type": "text/html" } });
    if (u.pathname === "/demo/abhed-demo.mp4") return new Response("mp4", { status: 200, headers: { "Content-Type": "video/mp4" } });
    return new Response("nope", { status: 404 });
  },
};
const env = { DEMO_SECRET: SECRET, ASSETS };
const req = (path, headers = {}) => new Request("https://zybuu.com" + path, { headers });

const future = Math.floor(Date.now() / 1000) + 3600;
const past = Math.floor(Date.now() / 1000) - 60;

// Fails closed.
t("no secret → 503", (await onRequest({ request: req("/demo/"), env: { ASSETS } })).status, 503);
t("no token → 403", (await onRequest({ request: req("/demo/"), env })).status, 403);
t("garbage token → 403", (await onRequest({ request: req("/demo/?t=abc"), env })).status, 403);
t("expired token → 403", (await onRequest({ request: req("/demo/?t=" + sign(past)), env })).status, 403);
t("wrong signature → 403", (await onRequest({ request: req("/demo/?t=" + future + "." + "0".repeat(64)), env })).status, 403);
t("signed with another secret → 403", (await onRequest({ request: req("/demo/?t=" + future + "." + createHmac("sha256", "other").update("demo|" + future).digest("hex")), env })).status, 403);
t("POST → 405", (await onRequest({ request: new Request("https://zybuu.com/demo/?t=" + sign(future), { method: "POST" }), env })).status, 405);

// Opens with a good link, and the cookie carries it to the video.
const good = await onRequest({ request: req("/demo/?t=" + sign(future)), env });
t("valid token → 200", good.status, 200);
t("valid token → noindex", good.headers.get("X-Robots-Tag"), "noindex, nofollow, noarchive");
t("valid token → no-store", good.headers.get("Cache-Control"), "private, no-store");
const cookie = good.headers.get("Set-Cookie") || "";
t("valid token → cookie scoped to /demo", /zb_demo=.*; Path=\/demo;.*HttpOnly; SameSite=Lax/.test(cookie), true);
const tok = decodeURIComponent(/zb_demo=([^;]+)/.exec(cookie)[1]);
t("cookie alone → video served", (await onRequest({ request: req("/demo/abhed-demo.mp4", { Cookie: "zb_demo=" + tok }), env })).status, 200);
t("cookie alone → missing file → 404", (await onRequest({ request: req("/demo/none.txt", { Cookie: "zb_demo=" + tok }), env })).status, 404);
t("expired cookie → 403", (await onRequest({ request: req("/demo/abhed-demo.mp4", { Cookie: "zb_demo=" + sign(past) }), env })).status, 403);

// The shell script and the function agree on the token format.
import { execFileSync } from "node:child_process";
const shellSig = execFileSync("sh", ["-c", `printf 'demo|%s' "${future}" | openssl dgst -sha256 -hmac "${SECRET}" -hex | awk '{print $NF}'`]).toString().trim();
t("send-demo.sh signature matches", future + "." + shellSig, sign(future));

console.log(failed ? `\n${failed} check(s) failed` : "\n  all demo gate checks pass");
process.exit(failed ? 1 : 0);
