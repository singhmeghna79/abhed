package ui

import (
	"bufio"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// LineReader reads a line of input with the editing a terminal user expects.
//
// The CLI read with bufio.Reader, which delivers whatever the terminal sends
// and nothing more: arrow keys arrived as escape sequences and appeared as
// "^[[D", there was no history, and a typo could only be fixed by backspacing
// to it. That is not a preference — a prompt where Left does not move the
// cursor reads as broken.
//
// When stdin is not a terminal — a pipe, a CI job, a here-doc — this falls
// straight through to line reads, because raw mode on a pipe would corrupt the
// input and there is nobody typing to benefit from it.
type LineReader struct {
	term    *term.Terminal
	fd      int
	state   *term.State
	fallbck *bufio.Reader
	raw     bool
}

// NewLineReader prepares stdin for editing where that is possible.
func NewLineReader(prompt string) *LineReader {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return &LineReader{fallbck: bufio.NewReader(os.Stdin)}
	}
	state, err := term.MakeRaw(fd)
	if err != nil {
		return &LineReader{fallbck: bufio.NewReader(os.Stdin)}
	}
	t := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stdout}, prompt)
	return &LineReader{term: t, fd: fd, state: state, raw: true}
}

// ReadLine returns the next line. In raw mode it supports Left and Right to
// move, Up and Down for history, Home, End, Ctrl-A, Ctrl-E, Ctrl-U, Ctrl-K and
// Ctrl-W, and Ctrl-C and Ctrl-D as interrupt and end of input.
func (l *LineReader) ReadLine() (string, error) {
	if l.raw {
		line, err := l.term.ReadLine()
		return line, err
	}
	line, err := l.fallbck.ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// Raw reports whether editing is active, so a caller can print its own prompt
// when it is not.
func (l *LineReader) Raw() bool { return l.raw }

// SetPrompt changes the prompt shown before the cursor.
func (l *LineReader) SetPrompt(p string) {
	if l.raw {
		l.term.SetPrompt(p)
	}
}

// Write prints through the terminal so output does not collide with a line
// being edited. Outside raw mode it goes to stdout unchanged.
func (l *LineReader) Write(p []byte) (int, error) {
	if l.raw {
		return l.term.Write(p)
	}
	return os.Stdout.Write(p)
}

// Close restores the terminal. Leaving it in raw mode makes the user's shell
// unusable afterwards, which is a worse failure than anything this package
// does, so callers must defer it.
func (l *LineReader) Close() {
	if l.raw && l.state != nil {
		_ = term.Restore(l.fd, l.state)
		l.raw = false
	}
}
