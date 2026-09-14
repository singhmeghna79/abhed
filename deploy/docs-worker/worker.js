// Serve the Abhed documentation at abhed.zybuu.com/docs.
//
// Docs belong under the product they document — abhed.zybuu.com/docs, and the
// same shape for every product Zybuu ships after this. But abhed.zybuu.com is
// a Cloudflare Tunnel to a laptop, and documentation that disappears when a lid
// closes is not documentation. So the path is split at the edge: /docs/* is
// answered from Cloudflare Pages, which is always up, and everything else
// continues down the tunnel to the console.
//
// This Worker sits on that one route. It is a proxy and nothing else: no
// rewriting, no auth, no logic that could disagree with what Pages serves.

// The pages.dev origin, deliberately, not zybuu.com. The apex carries a
// _redirects rule sending /docs/* here to abhed.zybuu.com — which is this
// Worker's own route, so fetching the apex would make the Worker redirect
// into itself. The project origin serves the same files with no such rule.
const ORIGIN = "https://zybuu.pages.dev";

export default {
  async fetch(request) {
    const url = new URL(request.url);

    // The product was renamed; the old hostname redirects, permanently, path
    // and query intact, so nothing printed before the rename goes dark.
    if (url.hostname === "titan.zybuu.com") {
      return Response.redirect("https://abhed.zybuu.com" + url.pathname + url.search, 301);
    }

    // /docs and /docs/ both mean the index.
    let path = url.pathname;
    if (path === "/docs") {
      return Response.redirect(url.origin + "/docs/", 308);
    }

    const target = new URL(ORIGIN + path + url.search);

    // Only GET and HEAD. Documentation has nothing to POST to, and refusing
    // the rest keeps this from becoming an open relay into the Pages project.
    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("Method not allowed", {
        status: 405,
        headers: { allow: "GET, HEAD" },
      });
    }

    const res = await fetch(target, {
      method: request.method,
      headers: request.headers,
      redirect: "manual",
      cf: { cacheEverything: true, cacheTtl: 300 },
    });

    // A redirect from Pages points at zybuu.com; rewrite it so a reader who
    // arrived at abhed.zybuu.com stays there rather than being bounced to the
    // other hostname mid-navigation.
    const loc = res.headers.get("location");
    if (loc) {
      const h = new Headers(res.headers);
      h.set("location", loc.replace(ORIGIN, url.origin));
      return new Response(res.body, { status: res.status, headers: h });
    }

    return res;
  },
};
