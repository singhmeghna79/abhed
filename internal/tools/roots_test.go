package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddRootGrantsAccess(t *testing.T) {
	work := t.TempDir()
	other := t.TempDir()
	s, err := NewSession(work)
	if err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(other, "file.go")
	if err := os.WriteFile(target, []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Resolve(target); err == nil {
		t.Fatal("a second directory was reachable before it was granted")
	}
	if err := s.AddRoot(other); err != nil {
		t.Fatalf("AddRoot: %v", err)
	}
	if _, err := s.Resolve(target); err != nil {
		t.Errorf("granted directory still unreachable: %v", err)
	}
	// The original root must keep working.
	if _, err := s.Resolve(filepath.Join(work, "a.go")); err != nil {
		t.Errorf("original workspace root broke after AddRoot: %v", err)
	}
}

// Everything outside the granted set stays denied. Adding one directory must
// not become a general escape.
func TestAddRootDoesNotWidenBeyondItself(t *testing.T) {
	work, other := t.TempDir(), t.TempDir()
	s, _ := NewSession(work)
	if err := s.AddRoot(other); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"/etc/passwd",
		filepath.Join(other, "..", "escape.txt"),
		filepath.Join(work, "..", "escape.txt"),
	} {
		if _, err := s.Resolve(bad); err == nil {
			t.Errorf("%s was reachable after granting an unrelated directory", bad)
		}
	}
}

// Granting / or $HOME defeats the boundary and is almost never intended.
func TestAddRootRefusesDangerousRoots(t *testing.T) {
	s, _ := NewSession(t.TempDir())

	if err := s.AddRoot("/"); err == nil {
		t.Error("granting / was accepted; that removes the boundary entirely")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if err := s.AddRoot(home); err == nil {
		t.Error("granting the home directory was accepted; that puts ~/.ssh in reach")
	}
}

func TestAddRootRejectsNonDirectories(t *testing.T) {
	work := t.TempDir()
	s, _ := NewSession(work)

	f := filepath.Join(work, "afile")
	os.WriteFile(f, []byte("x"), 0o644)
	if err := s.AddRoot(f); err == nil {
		t.Error("a file was accepted as a workspace root")
	}
	if err := s.AddRoot(filepath.Join(work, "does-not-exist")); err == nil {
		t.Error("a missing directory was accepted as a workspace root")
	}
}

func TestAddRootIsIdempotent(t *testing.T) {
	other := t.TempDir()
	s, _ := NewSession(t.TempDir())
	for i := 0; i < 3; i++ {
		if err := s.AddRoot(other); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.Roots) != 1 {
		t.Errorf("Roots = %d entries, want 1 after adding the same dir 3 times", len(s.Roots))
	}
}

// The denial message has to name the operator action. A bare "access denied"
// sent the model into rephrasing the same call, because it cannot tell a
// permanent boundary from a transient error.
func TestDenialExplainsHowToGrantAccess(t *testing.T) {
	s, _ := NewSession(t.TempDir())
	_, err := s.Resolve("/etc/passwd")
	if err == nil {
		t.Fatal("expected a denial")
	}
	msg := err.Error()
	for _, want := range []string{"--add-dir", "abhed -C", "Do not retry"} {
		if !strings.Contains(msg, want) {
			t.Errorf("denial does not mention %q: %s", want, msg)
		}
	}
}
