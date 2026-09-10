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

const MAX_FIELD = 2000;
const TO = "support@zybuu.com";
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
        from: FROM,
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

  return back(request, "ok");
}

// Anything other than POST. Answering rather than 405ing means a stray GET
// lands the visitor back on the page instead of on an error document.
export async function onRequest({ request }) {
  return Response.redirect(new URL("/#access", request.url).toString(), 303);
}

// The page is static, so the result is carried in the fragment and read by the
// form's own script. 303 rather than 302 so the browser follows with GET and a
// refresh cannot resubmit.
function back(request, status) {
  const url = new URL("/", request.url);
  url.hash = `access-${status}`;
  return Response.redirect(url.toString(), 303);
}
