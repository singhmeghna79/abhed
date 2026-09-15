package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The Community binary must not depend on anything under ee/. This is the
// edition boundary stated as a fact about the build rather than as a
// convention: an import added in the wrong direction shows up here, not in a
// review, and not after the enterprise tree has moved to its own repository
// and the Community build simply fails.
func TestCommunityBinaryHasNoEnterpriseDependencies(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go tool not on PATH")
	}
	out, err := exec.Command(goTool, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list -deps: %v", err)
	}
	for _, dep := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.Contains(dep, "/ee/") {
			t.Errorf("cmd/abhed depends on %s — the Community binary must not carry enterprise packages", dep)
		}
	}
}
