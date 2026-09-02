package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUndoRestoresModifiedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	os.WriteFile(p, []byte("original\n"), 0o644)

	u := NewUndoLog()
	u.BeginTurn()
	before, _ := os.ReadFile(p)
	u.Record(p, before, true)
	os.WriteFile(p, []byte("modified\n"), 0o644)

	restored, err := u.Undo()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "original\n" {
		t.Fatalf("not restored: %q", got)
	}
	if len(restored) != 1 || !strings.Contains(restored[0], "a.go") {
		t.Fatalf("unexpected report: %v", restored)
	}
}

// Undoing a creation means deleting the file, not writing an empty one.
func TestUndoRemovesCreatedFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "new.go")

	u := NewUndoLog()
	u.BeginTurn()
	u.Record(p, nil, false) // did not exist
	os.WriteFile(p, []byte("created\n"), 0o644)

	restored, err := u.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("created file should have been removed, not emptied")
	}
	if !strings.Contains(restored[0], "removed") {
		t.Fatalf("report should say removed: %v", restored)
	}
}

// A turn that edits several files must undo as one unit.
func TestUndoRevertsWholeTurn(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	u := NewUndoLog()
	u.BeginTurn()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("original\n"), 0o644)
		before, _ := os.ReadFile(p)
		u.Record(p, before, true)
		os.WriteFile(p, []byte("modified\n"), 0o644)
		paths = append(paths, p)
	}

	restored, err := u.Undo()
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 3 {
		t.Fatalf("expected 3 files reverted, got %d: %v", len(restored), restored)
	}
	for _, p := range paths {
		got, _ := os.ReadFile(p)
		if string(got) != "original\n" {
			t.Fatalf("%s not restored: %q", p, got)
		}
	}
}

// Undo goes back one turn at a time, not all the way to the start.
func TestUndoIsPerTurn(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	os.WriteFile(p, []byte("v1\n"), 0o644)

	u := NewUndoLog()

	u.BeginTurn()
	u.Record(p, []byte("v1\n"), true)
	os.WriteFile(p, []byte("v2\n"), 0o644)

	u.BeginTurn()
	u.Record(p, []byte("v2\n"), true)
	os.WriteFile(p, []byte("v3\n"), 0o644)

	if u.Pending() != 2 {
		t.Fatalf("expected 2 undoable turns, got %d", u.Pending())
	}

	u.Undo()
	if got, _ := os.ReadFile(p); string(got) != "v2\n" {
		t.Fatalf("first undo should reach v2, got %q", got)
	}
	u.Undo()
	if got, _ := os.ReadFile(p); string(got) != "v1\n" {
		t.Fatalf("second undo should reach v1, got %q", got)
	}
	if _, err := u.Undo(); err == nil {
		t.Fatal("a third undo should report nothing to undo")
	}
}

// Several edits to one file within a turn must revert to the PRE-turn state.
func TestUndoUsesEarliestCheckpointWithinTurn(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	os.WriteFile(p, []byte("original\n"), 0o644)

	u := NewUndoLog()
	u.BeginTurn()
	u.Record(p, []byte("original\n"), true)
	os.WriteFile(p, []byte("intermediate\n"), 0o644)
	u.Record(p, []byte("intermediate\n"), true)
	os.WriteFile(p, []byte("final\n"), 0o644)

	u.Undo()
	got, _ := os.ReadFile(p)
	if string(got) != "original\n" {
		t.Fatalf("should revert to pre-turn state, got %q", got)
	}
}

func TestUndoOnEmptyLog(t *testing.T) {
	u := NewUndoLog()
	if _, err := u.Undo(); err == nil {
		t.Fatal("expected an error with nothing to undo")
	}
}

func TestNilUndoLogIsSafe(t *testing.T) {
	var u *UndoLog
	u.Record("/x", nil, false) // must not panic
	if u.Pending() != 0 {
		t.Fatal("nil log should report zero pending")
	}
	if _, err := u.Undo(); err == nil {
		t.Fatal("nil log should error rather than pretend to work")
	}
}

func TestChangedListsFiles(t *testing.T) {
	u := NewUndoLog()
	u.BeginTurn()
	u.Record("/w/b.go", []byte("x"), true)
	u.Record("/w/a.go", []byte("y"), true)
	u.Record("/w/b.go", []byte("z"), true) // duplicate path

	changed := u.Changed()
	if len(changed) != 2 {
		t.Fatalf("expected 2 unique files, got %v", changed)
	}
	if changed[0] != "/w/a.go" {
		t.Fatalf("expected sorted output, got %v", changed)
	}
}

func TestPersistHookCalled(t *testing.T) {
	u := NewUndoLog()
	var persisted int
	u.Persist = func(cp Checkpoint) error { persisted++; return nil }

	u.BeginTurn()
	u.Record("/w/a.go", []byte("x"), true)
	u.Record("/w/b.go", []byte("y"), true)

	if persisted != 2 {
		t.Fatalf("persist hook called %d times, want 2", persisted)
	}
}

// A persistence failure must not prevent the edit from being recorded.
func TestPersistFailureIsNotFatal(t *testing.T) {
	u := NewUndoLog()
	u.Persist = func(cp Checkpoint) error { return os.ErrPermission }
	u.BeginTurn()
	u.Record("/w/a.go", []byte("x"), true)
	if u.Pending() != 1 {
		t.Fatal("checkpoint should still be recorded in memory")
	}
}
