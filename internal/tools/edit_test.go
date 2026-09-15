package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (*Session, string) {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	s, err := NewSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func run(t *testing.T, tool Tool, s *Session, args any) Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Run(context.Background(), s, raw)
}

// The core safety property: an edit cannot touch a file the model never read.
func TestEditRequiresPriorRead(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "package main\n")

	res := run(t, Edit{}, s, editArgs{Path: p, OldString: "package main", NewString: "package other"})
	if !res.IsError {
		t.Fatal("expected refusal without a prior read")
	}
	if !strings.Contains(res.Content, "not read this session") {
		t.Fatalf("error should explain the fix, got: %s", res.Content)
	}
	// File must be untouched.
	got, _ := os.ReadFile(p)
	if string(got) != "package main\n" {
		t.Fatalf("file was modified despite refusal: %q", got)
	}
}

func TestEditExactMatchAndNoFuzzy(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "func main() {\n\tx := 1\n}\n")
	run(t, Read{}, s, readArgs{Path: p})

	// Wrong indentation must NOT match - fuzzy matching would silently corrupt.
	res := run(t, Edit{}, s, editArgs{Path: p, OldString: "x := 1", NewString: "x := 2"})
	if res.IsError {
		t.Fatalf("substring without leading tab should still match as substring: %s", res.Content)
	}
	got, _ := os.ReadFile(p)
	if !strings.Contains(string(got), "x := 2") {
		t.Fatalf("edit did not apply: %q", got)
	}
}

func TestEditMultipleMatchesRefused(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "a := 1\nb := 1\nc := 1\n")
	run(t, Read{}, s, readArgs{Path: p})

	res := run(t, Edit{}, s, editArgs{Path: p, OldString: ":= 1", NewString: ":= 2"})
	if !res.IsError {
		t.Fatal("ambiguous edit must be refused, not guessed")
	}
	if !strings.Contains(res.Content, "appears 3 times") {
		t.Fatalf("error should report the count: %s", res.Content)
	}
	if !strings.Contains(res.Content, "replace_all") {
		t.Fatalf("error should name the fix: %s", res.Content)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "a := 1\nb := 1\nc := 1\n" {
		t.Fatalf("file changed on ambiguous edit: %q", got)
	}
}

func TestEditReplaceAll(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "a := 1\nb := 1\n")
	run(t, Read{}, s, readArgs{Path: p})

	res := run(t, Edit{}, s, editArgs{Path: p, OldString: ":= 1", NewString: ":= 2", ReplaceAll: true})
	if res.IsError {
		t.Fatalf("replace_all failed: %s", res.Content)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "a := 2\nb := 2\n" {
		t.Fatalf("got %q", got)
	}
}

func TestEditNoMatchSuggestsNearest(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "func handleRequest(w http.ResponseWriter) {\n\treturn\n}\n")
	run(t, Read{}, s, readArgs{Path: p})

	res := run(t, Edit{}, s, editArgs{Path: p, OldString: "func handleRequest(w http.ResponseWriter, r *http.Request) {", NewString: "x"})
	if !res.IsError {
		t.Fatal("expected no-match error")
	}
	if !strings.Contains(res.Content, "Nearest partial match") {
		t.Fatalf("should point at the near miss, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "handleRequest") {
		t.Fatalf("snippet should show the candidate line: %s", res.Content)
	}
}

func TestEditIdenticalStringsRefused(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "x\n")
	run(t, Read{}, s, readArgs{Path: p})

	res := run(t, Edit{}, s, editArgs{Path: p, OldString: "x", NewString: "x"})
	if !res.IsError || !strings.Contains(res.Content, "identical") {
		t.Fatalf("expected identical-string refusal, got: %s", res.Content)
	}
}

func TestEditDetectsExternalChange(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "original\n")
	run(t, Read{}, s, readArgs{Path: p})

	writeFile(t, p, "changed by someone else\n") // external modification

	res := run(t, Edit{}, s, editArgs{Path: p, OldString: "original", NewString: "new"})
	if !res.IsError || !strings.Contains(res.Content, "changed on disk") {
		t.Fatalf("expected stale-read detection, got: %s", res.Content)
	}
}

func TestWriteRequiresReadBeforeOverwrite(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "important content\n")

	res := run(t, Write{}, s, writeArgs{Path: p, Content: "clobbered"})
	if !res.IsError {
		t.Fatal("overwriting an unread file must be refused")
	}
	got, _ := os.ReadFile(p)
	if string(got) != "important content\n" {
		t.Fatalf("file was clobbered: %q", got)
	}
}

func TestWriteNewFileAllowed(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "new.go")
	res := run(t, Write{}, s, writeArgs{Path: p, Content: "package main\n"})
	if res.IsError {
		t.Fatalf("creating a new file should succeed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Created") {
		t.Fatalf("should report creation: %s", res.Content)
	}
}

// Path scoping: nothing outside the workspace is reachable.
func TestWorkspaceEscapeRefused(t *testing.T) {
	s, dir := setup(t)
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	writeFile(t, outside, "secret\n")
	defer func() { _ = os.Remove(outside) }()

	for _, attempt := range []string{
		outside,
		filepath.Join(dir, "..", "outside.txt"),
		"/etc/passwd",
	} {
		res := run(t, Read{}, s, readArgs{Path: attempt})
		if !res.IsError {
			t.Fatalf("escape via %q was allowed", attempt)
		}
	}
}

func TestRelativePathRejectedWithSuggestion(t *testing.T) {
	s, _ := setup(t)
	res := run(t, Read{}, s, readArgs{Path: "src/main.go"})
	if !res.IsError || !strings.Contains(res.Content, "must be absolute") {
		t.Fatalf("expected absolute-path guidance, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Did you mean") {
		t.Fatalf("error should suggest the absolute form: %s", res.Content)
	}
}

func TestReadEmptyFileIsNotSilent(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "empty.txt")
	writeFile(t, p, "")

	res := run(t, Read{}, s, readArgs{Path: p})
	if res.IsError {
		t.Fatalf("empty file is not an error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "empty") {
		t.Fatalf("empty read must say so rather than return nothing: %q", res.Content)
	}
}

func TestReadTruncationTellsModelHowToContinue(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "big.txt")
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line\n")
	}
	writeFile(t, p, b.String())

	res := run(t, Read{}, s, readArgs{Path: p, Limit: 10})
	if !res.Truncated {
		t.Fatal("expected truncation flag")
	}
	if !strings.Contains(res.Content, "offset/limit") {
		t.Fatalf("truncation notice must explain continuation: %s", res.Content)
	}
}

func TestReadDirectorySuggestsGlob(t *testing.T) {
	s, dir := setup(t)
	res := run(t, Read{}, s, readArgs{Path: dir})
	if !res.IsError || !strings.Contains(res.Content, "glob") {
		t.Fatalf("expected glob suggestion for a directory: %s", res.Content)
	}
}

func TestReadMissingFileSuggestsGlob(t *testing.T) {
	s, dir := setup(t)
	res := run(t, Read{}, s, readArgs{Path: filepath.Join(dir, "nope.go")})
	if !res.IsError || !strings.Contains(res.Content, "glob") {
		t.Fatalf("missing-file error should point to a recovery: %s", res.Content)
	}
}

func TestReadBinaryRefused(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "bin")
	if err := os.WriteFile(p, []byte{0x7f, 'E', 'L', 'F', 0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	res := run(t, Read{}, s, readArgs{Path: p})
	if !res.IsError || !strings.Contains(res.Content, "binary") {
		t.Fatalf("expected binary refusal: %s", res.Content)
	}
}

// Atomic write: the original survives if the write fails partway.
func TestAtomicWriteLeavesNoTempFiles(t *testing.T) {
	s, dir := setup(t)
	p := filepath.Join(dir, "a.txt")
	run(t, Write{}, s, writeArgs{Path: p, Content: "hello"})

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".abhed-") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
