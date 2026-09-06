package ui

import (
	"bytes"
	"testing"
)

// Styling must follow where the bytes end up, not the type of the writer.
//
// Wrapping os.Stdout in anything at all silently turned colour off, because the
// check could only recognise an *os.File — so the banner went monochrome the
// moment the interactive path started routing output through a wrapper.
func TestStyleAsksAWriterThatKnows(t *testing.T) {
	t.Setenv("NO_COLOR", "")

	if s := NewStyle(claimsTerminal{true}); !s.enabled {
		t.Error("a writer reporting a terminal must get colour")
	}
	if s := NewStyle(claimsTerminal{false}); s.enabled {
		t.Error("a writer reporting no terminal must not get colour")
	}

	// A plain buffer is not a terminal and must stay monochrome, so piped
	// output and captured logs are not full of escape codes.
	if s := NewStyle(&bytes.Buffer{}); s.enabled {
		t.Error("a buffer is not a terminal")
	}
}

// NO_COLOR outranks everything, including a writer that says it is a terminal.
func TestNoColorWins(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if s := NewStyle(claimsTerminal{true}); s.enabled {
		t.Error("NO_COLOR must disable colour whatever the writer says")
	}
}

type claimsTerminal struct{ yes bool }

func (claimsTerminal) Write(p []byte) (int, error) { return len(p), nil }
func (c claimsTerminal) IsTerminal() bool          { return c.yes }
