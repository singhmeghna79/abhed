package ui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/zybuu-ai/abhed/internal/policy"
)

// Approver prompts the user to approve a tool call.
//
// Never "the agent wants to edit auth.go — allow?". The prompt shows exactly
// what changes, because an approval the user cannot evaluate is theatre
// (docs §09).
type Approver struct {
	In      io.Reader
	Out     io.Writer
	Style   Style
	Session *AllowList
}

// AllowList holds scopes the user approved with "always" during this session.
type AllowList struct {
	scopes map[string]bool
}

func NewAllowList() *AllowList { return &AllowList{scopes: map[string]bool{}} }

func (a *AllowList) Add(scope string) { a.scopes[scope] = true }

func (a *AllowList) Has(scope string) bool { return a.scopes[scope] }

func NewApprover(out io.Writer) *Approver {
	return &Approver{In: os.Stdin, Out: out, Style: NewStyle(out), Session: NewAllowList()}
}

func (a *Approver) Approve(ctx context.Context, tool string, args json.RawMessage, res policy.Result) (bool, error) {
	if res.Scope != "" && a.Session.Has(res.Scope) {
		return true, nil
	}

	s := a.Style
	fmt.Fprintf(a.Out, "\n%s %s %s\n", s.Yellow("●"), s.Bold(tool), s.Dim(summarizeArgs(tool, args)))
	if res.Reason != "" {
		fmt.Fprintf(a.Out, "  %s\n", s.Dim(res.Reason))
	}

	if preview := a.preview(tool, args); preview != "" {
		fmt.Fprintln(a.Out, preview)
	}

	options := "[a]ccept  [r]eject"
	if res.Scope != "" {
		options += fmt.Sprintf("  [A]lways allow %s", s.Dim(res.Scope))
	}
	fmt.Fprintf(a.Out, "  %s ", options)

	reader := bufio.NewReader(a.In)
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if err != nil {
			// EOF (piped input, no TTY): refuse rather than silently proceeding.
			fmt.Fprintln(a.Out)
			return false, nil //nolint:nilerr // end of input means refuse, which is an answer, not an error
		}
		switch strings.TrimSpace(line) {
		case "a", "y", "":
			return true, nil
		case "r", "n":
			return false, nil
		case "A":
			if res.Scope != "" {
				a.Session.Add(res.Scope)
				return true, nil
			}
			fmt.Fprintf(a.Out, "  no scope available; [a]ccept or [r]eject: ")
		default:
			fmt.Fprintf(a.Out, "  %s ", options)
		}
	}
}

// preview renders what the action will actually do.
func (a *Approver) preview(tool string, raw json.RawMessage) string {
	s := a.Style
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	str := func(k string) string {
		if v, found := m[k]; found {
			if v, isStr := v.(string); isStr {
				return v
			}
		}
		return ""
	}

	switch tool {
	case "edit":
		old, updated := str("old_string"), str("new_string")
		var b strings.Builder
		for _, line := range strings.Split(strings.TrimRight(old, "\n"), "\n") {
			fmt.Fprintf(&b, "  %s\n", s.Red("- "+line))
		}
		for _, line := range strings.Split(strings.TrimRight(updated, "\n"), "\n") {
			fmt.Fprintf(&b, "  %s\n", s.Green("+ "+line))
		}
		return strings.TrimRight(b.String(), "\n")

	case "write":
		content := str("content")
		lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
		var b strings.Builder
		shown := lines
		if len(lines) > 15 {
			shown = lines[:15]
		}
		for _, line := range shown {
			fmt.Fprintf(&b, "  %s\n", s.Green("+ "+line))
		}
		if len(lines) > 15 {
			fmt.Fprintf(&b, "  %s\n", s.Dim(fmt.Sprintf("... %d more lines", len(lines)-15)))
		}
		return strings.TrimRight(b.String(), "\n")

	case "bash":
		return fmt.Sprintf("  %s", s.Dim("$ "+str("command")))
	}
	return ""
}
