package server

import (
	"strings"
	"testing"
)

// The link back to the operator's site must be optional, not merely
// configurable. An air-gapped install has no route to the internet, so a
// hardcoded link there is a dead end — and the console's own air-gap guard
// forbids an external URL in the shipped HTML, which is why this is
// substituted at render time rather than written into the page.
func TestHomeLinkOnlyRendersWhenConfigured(t *testing.T) {
	const page = `<header>x<!--HOME--></header>`

	off := withHome(page, "")
	if strings.Contains(off, "<!--HOME-->") {
		t.Error("placeholder left in the page when no home URL is set")
	}
	if strings.Contains(off, "href=") {
		t.Errorf("unset home URL still rendered a link: %q", off)
	}

	on := withHome(page, "https://zybuu.com/")
	if !strings.Contains(on, `href="https://zybuu.com/"`) {
		t.Errorf("configured home URL was not linked: %q", on)
	}

	// The URL comes from config, which an operator edits; escaping it means a
	// mistake there cannot become markup in every page the server serves.
	evil := withHome(page, `" onmouseover="alert(1)`)
	if strings.Contains(evil, `onmouseover="alert(1)`) {
		t.Errorf("home URL was not escaped: %q", evil)
	}
}
