package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConsoleRenderNoDuplicateReply drives the console's render() headlessly.
//
// The console is JavaScript embedded in a Go string, which makes it the one
// part of Titan with no test coverage — and it shipped a bug that printed every
// reply twice. The logic is ordinary and testable; only its packaging is
// awkward. So the test extracts render() and its helpers, runs them against a
// minimal DOM in node, and asserts the reply appears exactly once under every
// event ordering the server can produce.
//
// Skipped when node or python3 is unavailable: this must not break a build on a
// machine that has neither.
func TestConsoleRenderNoDuplicateReply(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping console render test")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed; skipping console render test")
	}

	dir := t.TempDir()
	extracted, err := exec.Command(py, "testdata/extract.py", "console.go").Output()
	if err != nil {
		t.Fatalf("extract render(): %v", err)
	}

	harness := `import { El } from './dom.mjs';
const tx = new El('div'); tx.id='tx';
globalThis.__root = tx;
const els = { tx };
globalThis.$ = id => els[id] || null;
let turnEl=null, streamEl=null, streamBody=null, live=true, current='s1';
const calls = new Map();
let approvals = new Map();
const stats = {turns:0,tin:0,tout:0,cached:0,tools:{},reason:null,compactions:0};
globalThis.hideThinking = ()=>{};
globalThis.showThinking = ()=>{};
globalThis.refresh = ()=>{};
globalThis.openDrawer = ()=>{};
function newTurn(){ turnEl = node('turn'); tx.appendChild(turnEl); return turnEl; }
function approval(){}
function resolveApproval(){}
`
	cases, err := os.ReadFile(filepath.Join("testdata", "render_cases.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	dom, err := os.ReadFile(filepath.Join("testdata", "dom.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dom.mjs"), dom, 0o644); err != nil {
		t.Fatal(err)
	}
	script := harness + string(extracted) + "\n" + string(cases)
	if err := os.WriteFile(filepath.Join(dir, "test.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(node, "test.mjs")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	t.Log("\n" + strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("console render produced a duplicate reply:\n%s", out)
	}
}
