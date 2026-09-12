// Package docsite serves the Titan documentation from inside the binary.
//
// The public copy lives at titan.zybuu.com/docs, served from Cloudflare so it
// survives the machine being asleep. That is the right default and the wrong
// one for the deployment this product is actually built for: an air-gapped
// install has no route to Cloudflare, and telling an operator in an enclave to
// "check the website" is telling them nothing.
//
// So the same generated HTML ships inside the binary. One generator
// (deploy/sitegen/build-docs.py), two outputs: the public site, and this.
package docsite

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The generator writes here before a build. The directory is committed with a
// placeholder so the package compiles on a checkout that has never run it —
// a missing embed is a compile error, and failing to build because the docs
// have not been generated would be a hostile default.
//
//go:embed all:site
var site embed.FS

// Available reports whether real documentation was embedded, as opposed to the
// placeholder. The server uses it to decide whether to advertise /docs at all;
// a link to an empty page is worse than no link.
func Available() bool {
	f, err := site.Open("site/guide/01-getting-started.html")
	if err != nil {
		return false
	}
	defer f.Close()
	return true
}

// Handler serves the embedded documentation under the given prefix.
func Handler(prefix string) http.Handler {
	sub, err := fs.Sub(site, "site")
	if err != nil {
		// Only reachable if the embed directive above stops matching, which
		// is a build-time mistake rather than a runtime condition.
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(sub))

	return http.StripPrefix(prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")

		// The site is generated with extensionless links, because Cloudflare
		// Pages serves them that way. net/http does not, so the extension is
		// put back here rather than generating two different link styles for
		// two different servers.
		if p != "" && !strings.HasSuffix(p, "/") && !strings.Contains(path.Base(p), ".") {
			if _, err := fs.Stat(sub, p+".html"); err == nil {
				r2 := *r
				u := *r.URL
				u.Path = "/" + p + ".html"
				r2.URL = &u
				files.ServeHTTP(w, &r2)
				return
			}
		}

		// Documentation is static and public; it carries no session data, so
		// it is cacheable. Short, because a running binary is the thing being
		// documented and the two should not drift for long.
		w.Header().Set("Cache-Control", "public, max-age=300")
		files.ServeHTTP(w, r)
	}))
}
