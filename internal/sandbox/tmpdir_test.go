package sandbox

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Compilers put their work directories in TMPDIR. On macOS that is a per-user
// path under /var/folders, and without an explicit allow every `go build`, `cc`
// and `cargo build` inside the sandbox fails with "operation not permitted" —
// which reads as an agent error rather than a sandbox one.
//
// Found by running the USAGE.md quickstart end to end.
func TestSandboxAllowsToolchainTempDir(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("TMPDIR handling is macOS-specific")
	}
	if os.Getenv("TMPDIR") == "" {
		t.Skip("no TMPDIR set")
	}
	dir := workspace(t)
	s := processSandbox(t, dir, false)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, _ := s.Command(ctx, dir,
		`touch "$TMPDIR/titan-probe" && echo WRITE_OK || echo WRITE_BLOCKED`).CombinedOutput()
	if !strings.Contains(string(out), "WRITE_OK") {
		t.Fatalf("toolchains cannot use TMPDIR inside the sandbox:\n%s", out)
	}
	os.Remove(os.Getenv("TMPDIR") + "/titan-probe")
}

// The real check: a Go build must actually work inside the sandbox.
func TestSandboxAllowsGoBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	dir := workspace(t)
	os.WriteFile(dir+"/go.mod", []byte("module probe\n\ngo 1.24\n"), 0o644)
	os.WriteFile(dir+"/main.go", []byte("package main\n\nfunc main() {}\n"), 0o644)

	s := processSandbox(t, dir, false)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	out, err := s.Command(ctx, dir, "go build ./... 2>&1").CombinedOutput()
	if err != nil || strings.Contains(string(out), "operation not permitted") {
		t.Fatalf("go build failed inside the sandbox:\n%s", out)
	}
}
