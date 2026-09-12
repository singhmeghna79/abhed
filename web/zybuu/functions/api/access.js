// Access requests from the homepage.
//
// A Cloudflare Pages Function rather than an endpoint on Titan, deliberately.
// Titan runs an agent with a shell; it does not need a new unauthenticated
// write path so a stranger can leave their name. This runs on Cloudflare's
// edge, stores nothing, and forwards to a mailbox.
//
// Requires one binding, set in the Pages dashboard under Settings → Environment
// variables. Without it the endpoint refuses rather than silently dropping
// requests, because a form that appears to work and posts into nothing is worse
// than one that says it is not configured:
//
//   RESEND_API_KEY   an API key from resend.com (free tier is enough)
//
// And optionally:
//   ACCESS_TO        where requests land (default support@zybuu.com)
//   ACCESS_FROM      the From: address (default Resend's shared sender)
//
// Set them with deploy/set-access-email.sh, which prompts for the key and
// hands it to Cloudflare without it touching the repository.

import { screen } from "./_screen.js";

const MAX_FIELD = 2000;
const TO = "support@zybuu.com";
// Resend's shared sending address. It works without verifying a domain, which
// is why it is the default, but mail from it is likelier to be filtered. Set
// ACCESS_FROM to an address at a domain verified with Resend to send as Zybuu.
const FROM = "Zybuu <onboarding@resend.dev>";

export async function onRequestPost({ request, env }) {
  let form;
  try {
    form = await request.formData();
  } catch {
    return back(request, "invalid");
  }

  // Trimmed and length-capped before anything else touches them. These strings
  // end up in an email body; unbounded input is a way to make that email
  // enormous, and the cap costs a legitimate sender nothing.
  const field = (name) =>
    String(form.get(name) ?? "").trim().slice(0, MAX_FIELD);

  const name = field("name");
  const email = field("email");
  const company = field("company");
  const use = field("use");

  if (!name || !email) return back(request, "missing");
  // Not a validating regex — those reject real addresses. Just enough to catch
  // a typo before it becomes an email nobody can reply to.
  if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) return back(request, "email");

  // A bot filling every field is the common case, so a field no human sees is
  // the cheapest filter there is. Answered means automated: accept it silently
  // rather than telling the bot which check it failed.
  if (field("website")) return back(request, "ok");

  if (!env.RESEND_API_KEY) {
    return back(request, "unconfigured");
  }

  const body = [
    `Name:    ${name}`,
    `Email:   ${email}`,
    `Company: ${company || "—"}`,
    "",
    "Use case:",
    use || "—",
    "",
    `From: ${request.headers.get("CF-Connecting-IP") ?? "unknown"}`,
    `At:   ${new Date().toISOString()}`,
  ].join("\n");

  try {
    const r = await fetch("https://api.resend.com/emails", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${env.RESEND_API_KEY}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        from: env.ACCESS_FROM || FROM,
        to: [env.ACCESS_TO || TO],
        // The requester's address, so a reply goes to them rather than to the
        // sending domain.
        reply_to: email,
        subject: `Titan access request — ${name}${company ? ` (${company})` : ""}`,
        text: body,
      }),
    });
    if (!r.ok) return back(request, "failed");
  } catch {
    return back(request, "failed");
  }

  // Acknowledge the requester — but only once the request has been delivered
  // and only if it passed screening.
  //
  // The order matters. The notification above is sent first and its failure
  // is reported; the acknowledgement is best-effort and never changes what
  // the visitor is told, because a person whose request DID arrive should not
  // see an error just because the courtesy reply bounced.
  //
  // Screening gates it because an acknowledgement is an outbound email to an
  // address a stranger typed. Replying to everything turns this form into a
  // way to send mail to arbitrary people over our domain — the classic
  // backscatter abuse — and burns the sending reputation the DKIM records
  // exist to build.
  const verdict = screen({ name, email, company, use });
  if (verdict.action !== "reject") {
    await acknowledge(env, { name, email, from: env.ACCESS_FROM || FROM });
  }

  return back(request, "ok");
}

// The thank-you. Deliberately quiet about what happens next: it sets an
// expectation we can keep, names a human route, and promises no timeline the
// person reading it can hold us to beyond "the same day".
async function acknowledge(env, { name, email, from }) {
  const first = (name.split(/\s+/)[0] || "there").slice(0, 40);
  const text = [
    `Hi ${first},`,
    "",
    "Thanks — your request for Titan access is in, and a person will read it.",
    "",
    "Titan is a deep agent harness that runs on your own hardware, against a",
    "model you host. If the hosted console is what you want, the reply will",
    "carry a sign-in code. If you would rather run it on your own",
    "infrastructure, say so and we will talk about that instead.",
    "",
    "Two things worth knowing before you decide:",
    "",
    "  - The console runs on a single machine. It is a trial environment,",
    "    not a service with an uptime commitment.",
    "  - Zybuu holds no SOC 2, ISO 27001 or HIPAA certification. That is",
    "    stated on the site too, and it is better said now than discovered",
    "    in procurement.",
    "",
    "Documentation, if you want to read ahead:",
    "  https://titan.zybuu.com/docs",
    "",
    "Reply to this email and it reaches a person, not a queue.",
    "",
    "— Zybuu",
  ].join("\n");

  try {
    await fetch("https://api.resend.com/emails", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${env.RESEND_API_KEY}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        from,
        to: [email],
        reply_to: env.ACCESS_TO || TO,
        subject: "Your Titan access request",
        text,
      }),
    });
  } catch {
    // Swallowed on purpose. The request itself is already delivered; failing
    // the visitor's submission because a courtesy email bounced would be the
    // wrong trade.
  }
}

// Anything other than POST. Answering rather than 405ing means a stray GET
// lands the visitor back on the page instead of on an error document.
export async function onRequest({ request }) {
  return Response.redirect(new URL("/titan/#access", request.url).toString(), 303);
}

// The page is static, so the result is carried in the fragment and read by the
// form's own script. 303 rather than 302 so the browser follows with GET and a
// refresh cannot resubmit.
function back(request, status) {
  // Back to the page that submitted, not a path baked in here. This returned
  // people to "/" — correct when the form lived on the homepage, wrong the
  // moment it moved to /titan/, and the symptom was silent: the submission
  // worked, the email arrived, and the visitor landed on a page with no form
  // and no handler, so nothing acknowledged them.
  //
  // The Referer is the submitting page. It is same-origin here because the
  // CSP sets form-action 'self', so it cannot be pointed at another site.
  let path = "/titan/";
  const ref = request.headers.get("Referer");
  if (ref) {
    try {
      const u = new URL(ref);
      if (u.origin === new URL(request.url).origin) path = u.pathname;
    } catch {
      // Unparseable Referer: fall through to the default.
    }
  }
  const url = new URL(path, request.url);
  url.hash = `access-${status}`;
  return Response.redirect(url.toString(), 303);
}
