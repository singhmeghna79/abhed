// Package sitecheck holds the tests that keep zybuu.com honest.
//
// The homepage argues that its numbers are checkable in the source, which is a
// good argument and a standing liability: every one of them was hand-maintained
// and three drifted within a week. A reader who verifies one claim, finds it
// wrong, and re-audits the rest spends the credibility the honest sections
// bought.
//
// These tests fail when the page and the source disagree, so the drift is a red
// build rather than a discovery made by a prospect.
package sitecheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// page returns the published homepage.
func page(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "zybuu", "index.html"))
	if err != nil {
		t.Skipf("homepage not present: %v", err)
	}
	return string(b)
}

// countFuncs counts functions matching a prefix across a package's test files.
func countFuncs(t *testing.T, dir, prefix string) int {
	t.Helper()
	n := 0
	re := regexp.MustCompile(`(?m)^func ` + prefix)
	err := filepath.Walk(filepath.Join("..", "..", dir),
		func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			n += len(re.FindAllIndex(b, -1))
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The page claims every adversarial attack in the suite is blocked. The number
// was 16 while the suite held 24 — it counted one of the two files and was
// never updated when the second was added. It understated the product, which
// is the harmless direction, and it was still wrong on the page's own headline
// evidence.
func TestAttackCountOnPageMatchesSuite(t *testing.T) {
	got := countFuncs(t, "internal/redteam", "TestAttack_")
	if got == 0 {
		t.Fatal("no TestAttack_ functions found — the suite moved")
	}
	want := regexp.MustCompile(`(\d+)\s*/\s*(\d+)</b><span>adversarial`)
	m := want.FindStringSubmatch(page(t))
	if m == nil {
		t.Fatal("the page no longer states an adversarial attack count")
	}
	if m[1] != m[2] {
		t.Errorf("the page claims %s of %s blocked — say all or say which", m[1], m[2])
	}
	if m[2] != itoa(got) {
		t.Errorf("page says %s attacks, suite has %d — one of them is stale",
			m[2], got)
	}
}

// "N test functions across the engine" is the kind of number that drifts every
// time anyone writes a test, which is exactly why it needs a test of its own.
func TestFunctionCountOnPageMatchesRepo(t *testing.T) {
	total := 0
	for _, dir := range []string{"internal", "cmd", "sdk"} {
		total += countFuncs(t, dir, "Test")
	}
	m := regexp.MustCompile(`<b>(\d+)</b><span>test functions`).
		FindStringSubmatch(page(t))
	if m == nil {
		t.Fatal("the page no longer states a test-function count")
	}
	// Exact, deliberately. A tolerance here is a licence to drift, and the
	// page's whole argument is that the reader can check.
	if m[1] != itoa(total) {
		t.Errorf("page says %s test functions, repo has %d", m[1], total)
	}
}

// The page must not claim an install path that does not exist. The repository
// is unpublished, so `go install` 404s and there is no curl installer; both are
// tempting and both would be caught in the first thirty seconds by the audience
// this page is written for.
func TestPageClaimsNoInstallPathThatDoesNotExist(t *testing.T) {
	p := page(t)
	for _, forbidden := range []string{
		"curl -fsSL", "| sh", "go install github.com/yuvrajsingh",
		"brew install", "npm install -g",
	} {
		if strings.Contains(p, forbidden) {
			t.Errorf("the page offers %q, which does not work: the repo is "+
				"unpublished", forbidden)
		}
	}
}

// A compliance claim is a legal commitment, not a marketing line. None of these
// certifications is held.
func TestPageClaimsNoCertification(t *testing.T) {
	p := page(t)
	// The limitations section names these in order to DENY them, so only an
	// affirmative claim counts.
	for _, phrase := range []string{
		"SOC 2 certified", "SOC2 certified", "ISO 27001 certified",
		"HIPAA compliant", "FedRAMP authorized", "FedRAMP authorised",
	} {
		if strings.Contains(strings.ToLower(p), strings.ToLower(phrase)) {
			t.Errorf("the page claims %q, which is not true", phrase)
		}
	}
}

// The form's action and the CSP that governs it live in two files and have to
// agree. They did not: form-action was 'none' while the page carried a form, so
// the browser blocked every submission before a request left — indistinguishable
// from a missing endpoint, and not fixed by supplying one.
func TestFormActionAgreesWithCSP(t *testing.T) {
	p := page(t)
	hdr, err := os.ReadFile(filepath.Join("..", "..", "web", "zybuu", "_headers"))
	if err != nil {
		t.Skipf("_headers not present: %v", err)
	}
	hasForm := strings.Contains(p, "<form")
	if !hasForm {
		return
	}
	if strings.Contains(string(hdr), "form-action 'none'") {
		t.Error("the page has a form and the CSP is form-action 'none' — " +
			"submissions are blocked in the browser, silently")
	}
	// A relative action stays same-origin on the apex, on www, and on a
	// preview deployment. An absolute one is cross-origin on two of the three.
	if regexp.MustCompile(`<form[^>]+action="https?://`).MatchString(p) {
		t.Error("the form action is absolute — it is cross-origin on www and " +
			"on preview deployments, which form-action 'self' then rejects")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The page must not claim a control that is declared and never executed.
//
// Two of these shipped: MCP digest pinning, carried through config and verified
// nowhere, and "the guarantees do not weaken when embedded" while the SDK
// builds no sandbox at all. Both were sold to the air-gapped buyer the page
// targets, which is the worst audience to be wrong in front of.
func TestPageDoesNotClaimUnenforcedControls(t *testing.T) {
	p := strings.ToLower(page(t))

	// Digest is enforced when connectOne reads it. Until then the page may not
	// mention it, and when it is wired up this test is what unblocks the claim.
	gw, err := os.ReadFile(filepath.Join("..", "..", "internal", "mcp", "gateway.go"))
	if err == nil {
		enforced := regexp.MustCompile(`(?s)func \(g \*Gateway\) connectOne.*?\n}`).Find(gw)
		if enforced != nil && !strings.Contains(string(enforced), "Digest") {
			if strings.Contains(p, "digest") {
				t.Error("the page claims digest pinning; connectOne does not " +
					"read Digest, so setting it protects nothing")
			}
		}
	}

	// The SDK's own tool registry decides this one.
	sdk, err := os.ReadFile(filepath.Join("..", "..", "sdk", "titan.go"))
	if err == nil && strings.Contains(string(sdk), "tools.Bash{}") {
		if strings.Contains(p, "guarantees do not weaken when embedded") {
			t.Error("the page claims the guarantees do not weaken when " +
				"embedded, but sdk builds tools.Bash{} with no sandbox")
		}
	}
}
