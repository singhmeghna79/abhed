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

// flat collapses runs of whitespace so a prose match is not defeated by the
// line wrapping in the HTML. "builds no sandbox" is three words on the page
// and two lines in the file.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// has reports whether the page contains any of the given markers, matched
// case-insensitively so a reword of the surrounding prose cannot disarm it.
func has(markers ...string) func(string) bool {
	return func(p string) bool {
		low := strings.ToLower(p)
		for _, m := range markers {
			if strings.Contains(low, strings.ToLower(m)) {
				return true
			}
		}
		return false
	}
}

// page returns the published homepage.
func page(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "zybuu", "index.html"))
	if err != nil {
		// Fatal, not Skip. Skipping turned this whole package into a silent
		// no-op when the page moved: every test reported ok with nothing
		// checked, and green-when-absent is the worst failure a guard can
		// have. The page is in the repository; its absence is the bug.
		t.Fatalf("homepage not readable, so nothing here was checked: %v", err)
	}
	return flat(string(b))
}

// prose returns the page's visible text: script and style bodies removed, tags
// stripped, entities unwrapped, lowercased.
//
// Claims about wording have to be read against prose, not markup. Checking the
// raw HTML meant attribute values ("0 0 256 256", "location.pathname") supplied
// periods, so splitting on "." produced fragments like "<" instead of
// sentences, and a sentence-scoped test silently matched nothing at all.
func prose(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "zybuu", "index.html"))
	if err != nil {
		t.Fatalf("homepage not readable, so nothing here was checked: %v", err)
	}
	s := string(b)
	// No backreference: Go's RE2 has none, so each element is named twice.
	s = regexp.MustCompile(`(?is)<script\b.*?</script>`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?is)<style\b.*?</style>`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`<[^>]*>`).ReplaceAllString(s, " ")
	r := strings.NewReplacer(
		"&mdash;", "—", "&ndash;", "–", "&amp;", "&", "&lt;", "<",
		"&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ")
	return strings.ToLower(flat(r.Replace(s)))
}

// sentences splits prose on terminators, so a claim can be judged in the
// sentence that carries it rather than against the whole page.
func sentences(p string) []string {
	parts := regexp.MustCompile(`[.!?]+\s+|[.!?]+$`).Split(p, -1)
	out := parts[:0]
	for _, s := range parts {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
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
	// The limitations section names these in order to DENY them, so only an
	// affirmative claim counts. Matching on the regime plus ANY attainment
	// verb, rather than on six fixed phrases: "SOC 2 audited" and "in scope
	// for SOC 2" are the same false claim as "SOC 2 certified" and both walked
	// past the literal list. This is the page's only legal exposure, so the
	// check is deliberately broad and a legitimate mention has to be negated
	// inside its own sentence.
	regime := regexp.MustCompile(`soc\s?2|iso\s?27001|hipaa|fedramp|pci[\s-]?dss`)
	attained := regexp.MustCompile(
		`\b(certified|certification|compliant|compliance|authori[sz]ed|audited|` +
			`attested|accredited|approved|in scope for|undergoing|achieved|` +
			`maintains?|holds?)\b`)
	// Words that turn a mention into a denial.
	denied := regexp.MustCompile(
		`\bno\b|\bnot\b|\bnone\b|\bwithout\b|\bnever\b|\boutstanding\b|` +
			`\bdoes not\b|\bdo not\b|\bcannot\b|\bunlike\b`)

	for _, s := range sentences(prose(t)) {
		if !regime.MatchString(s) || !attained.MatchString(s) {
			continue
		}
		if denied.MatchString(s) {
			continue // "Zybuu holds no SOC 2 … certification" — honest.
		}
		t.Errorf("the page appears to claim a certification it does not hold, "+
			"in: %q", s)
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
		t.Fatalf("_headers not readable, so the CSP was not checked: %v", err)
	}
	csp := string(hdr)

	// The rule, not the last instance of it. This test previously grepped for
	// the one literal that had been wrong — form-action 'none' — and passed
	// while a missing script-src blocked the page's status script in exactly
	// the same way. Under default-src 'none' every resource kind the page uses
	// has to be named, so the check is per kind.
	// Every resource kind the page can use, not the ones that have already
	// broken. Two rounds running, this table listed exactly the directives
	// that had bitten us — form-action, then script-src — and each time the
	// NEXT directive was the one that shipped broken. Deleting style-src from
	// the CSP left the whole page unstyled and this suite green.
	kinds := []struct {
		present   func(string) bool // does the page use this kind?
		directive string            // what the CSP must therefore permit
		why       string
	}{
		{has(`<form`), "form-action", "submissions are blocked before a request is made"},
		{has(`<script`), "script-src", "the script never runs and the page looks inert"},
		{has(`<style`, `style="`), "style-src", "the page renders completely unstyled"},
		{has(`<img`, `<link rel="icon"`, `<link rel='icon'`), "img-src", "images and the favicon do not load"},
		{has(`<iframe`, `<frame`), "frame-src", "the embedded frame is blocked"},
		{has(`<video`, `<audio`, `<source`), "media-src", "media does not play"},
		{has(`@font-face`, `fonts.googleapis`, `.woff`), "font-src", "webfonts fall back silently"},
		{has(`fetch(`, `XMLHttpRequest`, `new WebSocket`, `navigator.sendBeacon`), "connect-src", "the request is blocked and the failure is invisible"},
		{has(`<link rel="stylesheet"`, `<link rel='stylesheet'`), "style-src", "the external stylesheet is blocked"},
	}
	strict := strings.Contains(csp, "default-src 'none'")
	seen := map[string]bool{}
	for _, k := range kinds {
		if !k.present(p) || seen[k.directive] {
			continue
		}
		named := strings.Contains(csp, k.directive+" ")
		blocked := strings.Contains(csp, k.directive+" 'none'")
		if blocked || (strict && !named) {
			seen[k.directive] = true
			t.Errorf("the page uses a resource needing %s but the CSP does not "+
				"permit it — %s", k.directive, k.why)
		}
	}

	if !strings.Contains(p, "<form") {
		return
	}
	// A relative action stays same-origin on the apex, on www, and on a
	// preview deployment. An absolute one is cross-origin on two of the three.
	if regexp.MustCompile(`<form[^>]+action="https?://`).MatchString(p) {
		t.Error("the form action is absolute — it is cross-origin on www and " +
			"on preview deployments, which form-action 'self' then rejects")
	}
}

// A count and a list that disagree is how the last round's numbers were found
// wrong. If the page says seven events, the sentence had better name seven.
func TestEventListMatchesItsOwnCount(t *testing.T) {
	// Loose on the wording, strict on the existence. The previous version
	// matched one sentence shape and t.Skip'd otherwise, so rewording "on
	// seven live events" to anything else turned the guard off and the package
	// still reported ok. A guard that disarms on a reword is not a guard: if
	// the page names a count of events, the list must be found and checked.
	// Read from prose, not markup: periods inside attributes truncated the
	// list before it started and tags supplied commas, so the item count was
	// measured against a fragment.
	p := prose(t)
	loc := regexp.MustCompile(
		`(six|seven|eight|nine)\s+(live\s+|streamed\s+|extension\s+|lifecycle\s+)*(events|hooks)`,
	).FindStringSubmatchIndex(p)
	if loc == nil {
		if strings.Contains(p, "extension") &&
			regexp.MustCompile(`\b(events|hooks)\b`).MatchString(p) {
			t.Fatal("the page discusses extension events but no longer states " +
				"a count this test can check against the list")
		}
		t.Fatal("the page no longer enumerates extension events — if that is " +
			"deliberate, delete this test rather than letting it skip")
	}
	word := p[loc[2]:loc[3]]
	words := map[string]int{"six": 6, "seven": 7, "eight": 8, "nine": 9}
	want, ok := words[word]
	if !ok {
		t.Fatalf("unrecognised event count %q", word)
	}

	// The enumeration follows the count, to the end of that sentence.
	tail := p[loc[1]:]
	if i := strings.IndexAny(tail, ".!?"); i >= 0 {
		tail = tail[:i]
	}
	tail = strings.Trim(tail, " —–-:,")
	if tail == "" {
		t.Fatalf("the page says %s events but no longer names them — a count "+
			"with no list is unverifiable", word)
	}
	got := 0
	for _, item := range strings.Split(strings.ReplaceAll(tail, " and ", ", "), ",") {
		if strings.TrimSpace(item) != "" {
			got++
		}
	}
	if got != want {
		t.Errorf("the page says %s events and names %d: %q", word, got, tail)
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
		// Anchored on the SUBJECT, not on the sentence. Two earlier versions
		// of this test listed the phrasings that were wrong at the time, and
		// both were disarmed by an ordinary reword: the page could drop the
		// disclosure entirely and stay green. So the rule is now — if the page
		// sells embedding at all, it must disclose who owns isolation, in
		// whatever words.
		sellsEmbedding := regexp.MustCompile(
			`embed(s|ded|ding)?\b|in-process|inside your (own )?(go )?(service|program|binary)`,
		).MatchString(p)
		if sellsEmbedding {
			discloses := regexp.MustCompile(
				`builds no sandbox|no sandbox|sandbox is (the )?(caller|host)|` +
					`(caller|host)('s)? (own )?sandbox|isolation is (the )?(caller|host)`,
			).MatchString(p)
			if !discloses {
				t.Error("the page sells embedding without disclosing that the " +
					"SDK builds no sandbox — the host owns isolation")
			}
			// A blanket "nothing weakens" is the overclaim in any wording.
			blanket := regexp.MustCompile(
				`(all|every|the) guarantees?[^.]{0,40}(hold|survive|do not weaken|unchanged)|` +
					`nothing is lost when embed|every guarantee survives`,
			)
			if m := blanket.FindString(p); m != "" {
				t.Errorf("the page claims %q while the SDK builds tools.Bash{} "+
					"with no sandbox", strings.TrimSpace(m))
			}
		}
	}
}

// The provider count drifted to "20+" because it was the one number on the
// page nothing asserted. "20+" implies more than twenty; exactly twenty are
// registered. A plus sign is a small dishonesty and this page's whole argument
// is that its numbers are checkable.
func TestProviderCountOnPageMatchesRegistry(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "model")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("model package not present: %v", err)
	}
	re := regexp.MustCompile(`Register\("[a-z0-9-]+"`)
	n := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "providers_") ||
			strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		n += len(re.FindAll(b, -1))
	}
	if n == 0 {
		t.Fatal("no Register calls found — the provider registry moved")
	}

	m := regexp.MustCompile(`<b>(\d+)(\+?)</b><span>model providers`).
		FindStringSubmatch(page(t))
	if m == nil {
		t.Fatal("the page no longer states a provider count")
	}
	if m[2] == "+" {
		t.Errorf("the page says %s+ providers; exactly %d are registered, so "+
			"the plus claims something that is not there", m[1], n)
	}
	if m[1] != itoa(n) {
		t.Errorf("page says %s providers, registry has %d", m[1], n)
	}
}

// The SDK's package doc is godoc — the first thing a Go developer reads, and
// the same audience the page addresses. It carried "the guarantees do not
// weaken when embedded" for a week after the page retracted that exact
// sentence, which is a worse place to be wrong than the page.
func TestSDKDocDoesNotOverclaim(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "sdk", "titan.go"))
	if err != nil {
		t.Skipf("sdk not present: %v", err)
	}
	src := string(b)
	if strings.Contains(src, "tools.Bash{}") &&
		strings.Contains(src, "guarantees do not weaken when embedded") {
		t.Error("sdk/titan.go claims the guarantees do not weaken when " +
			"embedded while building tools.Bash{} with no sandbox")
	}
}

// TestDurabilityClaimsNameTheDriver keeps the page from promising persistence
// the default store does not provide.
//
// This has now shipped twice: once in the governance section, and once in the
// limitations table, which said "transcripts and accounts survive it" when
// only accounts do. Titan's own status string is the authority — main.go
// reports "memory (sessions do not survive restart)" — and the default driver
// is memory, so any unqualified survives-a-restart claim about transcripts,
// sessions or history is false for most readers.
func TestDurabilityClaimsNameTheDriver(t *testing.T) {
	// Confirm the premise against the code rather than trusting this comment.
	cfg, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "config.go"))
	if err != nil {
		t.Fatalf("config not readable, so the default driver was not checked: %v", err)
	}
	if !regexp.MustCompile(`Driver:\s*"memory"`).Match(cfg) {
		t.Skip("the default storage driver is no longer memory; revisit this test")
	}

	// Scoped to durability ACROSS A RESTART, which is what the memory driver
	// fails. Without the restart anchor this matched the compaction copy —
	// "how many turns are kept" is about the context window, not the store,
	// and flagging it would train the reader to ignore this test.
	subject := regexp.MustCompile(`\b(transcript|session|history|conversation)s?\b`)
	survives := regexp.MustCompile(
		`\b(survives?|persists?|outlasts?|retained|durable|kept)\b`)
	scoped := regexp.MustCompile(`restart|reboot|crash|process (ends|exits)|across runs`)
	// Naming Postgres, or denying the claim, makes the sentence honest.
	honest := regexp.MustCompile(
		`postgres|\bno\b|\bnot\b|\bnever\b|\bdoes not\b|\bdo not\b|end with|` +
			`\bends?\b|in-memory|\bmemory\b`)

	// The restart that scopes the claim is often in the PREVIOUS sentence —
	// "a restart ends running turns. Transcripts survive it." — so the topic
	// is read over a small window while the honesty check stays on the
	// sentence that actually makes the claim.
	all := sentences(prose(t))
	for i, s := range all {
		if !subject.MatchString(s) || !survives.MatchString(s) {
			continue
		}
		window := s
		if i > 0 {
			window = all[i-1] + ". " + s
		}
		if !scoped.MatchString(window) {
			continue
		}
		if honest.MatchString(s) {
			continue
		}
		t.Errorf("the page claims durability without naming the store, in: %q "+
			"— the default driver is memory, where it is false", s)
	}
}
