package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Session carries the per-session state tools need: the workspace root that
// scopes all filesystem access, and the read-tracking that makes editing safe.
type Session struct {
	Root string // absolute workspace root; nothing outside it is reachable
	Cwd  string // persists across bash calls (shell state does not)

	mu    sync.Mutex
	reads map[string]string // abs path -> content hash at time of read
}

func NewSession(root string) (*Session, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	// Resolve symlinks so a symlinked root cannot be used to escape scoping.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	return &Session{Root: abs, Cwd: abs, reads: make(map[string]string)}, nil
}

// Resolve validates a model-supplied path and returns its absolute form.
//
// Two guarantees: the path is absolute (relative paths are ambiguous across
// turns after a cd), and it stays inside the workspace. Both failures return
// messages that tell the model how to correct the call.
func (s *Session) Resolve(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be absolute. Did you mean %s?",
			filepath.Join(s.Cwd, path))
	}

	clean := filepath.Clean(path)

	// Compare against the symlink-resolved root. We resolve the deepest
	// existing ancestor so that a path to a not-yet-created file still gets
	// checked against its real parent directory.
	check := clean
	for {
		if resolved, err := filepath.EvalSymlinks(check); err == nil {
			rel, err := filepath.Rel(s.Root, resolved)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "", fmt.Errorf("%s is outside the session workspace (%s). Access denied", path, s.Root)
			}
			break
		}
		parent := filepath.Dir(check)
		if parent == check {
			break // reached the filesystem root without resolving
		}
		check = parent
	}

	// Also check the lexical path, to catch traversal on paths that do not exist.
	rel, err := filepath.Rel(s.Root, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside the session workspace (%s). Access denied", path, s.Root)
	}
	return clean, nil
}

// MarkRead records that a file was read, with a hash of what was seen.
//
// This is the single most effective guard against destructive edits: write and
// edit both require a prior read, so the model can never replace content it has
// not observed (docs §06 §3-4).
func (s *Session) MarkRead(path, content string) {
	sum := sha256.Sum256([]byte(content))
	s.mu.Lock()
	s.reads[path] = hex.EncodeToString(sum[:])
	s.mu.Unlock()
}

func (s *Session) WasRead(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found := s.reads[path]
	return found
}

// ChangedSinceRead reports whether the file on disk differs from what was read.
// Catches the case where an external process (or a bash command) modified a
// file between the model reading it and editing it.
func (s *Session) ChangedSinceRead(path string) bool {
	s.mu.Lock()
	want, found := s.reads[path]
	s.mu.Unlock()
	if !found {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) != want
}

// Rel renders a path relative to the workspace root for display. Output stays
// short and clickable without leaking absolute paths into the transcript.
func (s *Session) Rel(path string) string {
	if rel, err := filepath.Rel(s.Root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}
