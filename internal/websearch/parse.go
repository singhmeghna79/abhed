package websearch

import (
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// DuckDuckGo's HTML endpoint returns results in a predictable structure. These
// patterns target the class names rather than positions, so ordinary layout
// changes upstream do not break parsing — only a rename of the result classes
// would, and the caller reports that case explicitly.
var (
	reResultBlock = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	reSnippet     = regexp.MustCompile(`(?s)<a[^>]*class="[^"]*result__snippet[^"]*"[^>]*>(.*?)</a>`)
	reTag         = regexp.MustCompile(`<[^>]*>`)
	reSpace       = regexp.MustCompile(`\s+`)
)

// parseDDG extracts results from the HTML endpoint's response.
func parseDDG(body string, limit int) []Result {
	titles := reResultBlock.FindAllStringSubmatch(body, -1)
	snippets := reSnippet.FindAllStringSubmatch(body, -1)

	out := make([]Result, 0, len(titles))
	for i, m := range titles {
		if limit > 0 && len(out) >= limit {
			break
		}
		link := unwrapDDG(m[1])
		if link == "" {
			continue
		}
		snippet := ""
		if i < len(snippets) {
			snippet = stripTags(snippets[i][1])
		}
		out = append(out, Result{
			Title:   stripTags(m[2]),
			URL:     link,
			Snippet: snippet,
		})
	}
	return out
}

// unwrapDDG turns DuckDuckGo's redirector into the real destination.
//
// Results arrive as //duckduckgo.com/l/?uddg=<encoded>. Handing the model the
// redirector would waste a fetch and hide the actual domain, which matters when
// the reader is judging whether a source is trustworthy.
func unwrapDDG(raw string) string {
	raw = html.UnescapeString(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if target := u.Query().Get("uddg"); target != "" {
		if decoded, err := url.QueryUnescape(target); err == nil {
			return decoded
		}
		return target
	}
	if u.Scheme == "http" || u.Scheme == "https" {
		return u.String()
	}
	return ""
}

// stripTags reduces HTML to readable text.
func stripTags(s string) string {
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

func readAllLimited(resp *http.Response, max int64) ([]byte, error) {
	return io.ReadAll(io.LimitReader(resp.Body, max))
}
