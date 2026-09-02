package agent

import (
	"sort"
	"testing"
	"time"
)

func TestNewIDFormat(t *testing.T) {
	id := newID()
	if len(id) != 26 {
		t.Fatalf("want 26 chars, got %d: %q", len(id), id)
	}
	for i, c := range id {
		if !containsRune(enc, c) {
			t.Fatalf("char %d (%q) not in Crockford alphabet: %q", i, c, id)
		}
	}
}

func TestNewIDMonotonic(t *testing.T) {
	const n = 5000
	ids := make([]string, n)
	for i := range ids {
		ids[i] = newID()
	}
	if !sort.StringsAreSorted(ids) {
		for i := 1; i < n; i++ {
			if ids[i] <= ids[i-1] {
				t.Fatalf("not monotonic at %d: %q then %q", i, ids[i-1], ids[i])
			}
		}
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestNewIDSortsByTime(t *testing.T) {
	a := newID()
	time.Sleep(3 * time.Millisecond)
	b := newID()
	if a >= b {
		t.Fatalf("later id should sort after earlier: %q >= %q", a, b)
	}
}

func containsRune(s string, r rune) bool {
	for _, c := range s {
		if c == r {
			return true
		}
	}
	return false
}
