// Package ui renders the agent's work to a terminal.
//
// The design rules are in docs/architecture/09-ux.md. The ones that shape this
// code: show work as it happens so the user can interrupt early; one line per
// tool call, expanded only when it carries information; diffs before writes.
package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yuvrajsingh/titan/internal/agent"
)

// ANSI codes, disabled when not writing to a terminal or when NO_COLOR is set.
type Style struct{ enabled bool }

func NewStyle(w io.Writer) Style {
	if os.Getenv("NO_COLOR") != "" {
		return Style{false}
	}
	f, isFile := w.(*os.File)
	if !isFile {
		return Style{false}
	}
	info, err := f.Stat()
	if err != nil {
		return Style{false}
	}
	return Style{(info.Mode() & os.ModeCharDevice) != 0}
}

func (s Style) wrap(code, text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Dim(t string) string    { return s.wrap("2", t) }
func (s Style) Bold(t string) string   { return s.wrap("1", t) }
func (s Style) Red(t string) string    { return s.wrap("31", t) }
func (s Style) Green(t string) string  { return s.wrap("32", t) }
func (s Style) Yellow(t string) string { return s.wrap("33", t) }
func (s Style) Blue(t string) string   { return s.wrap("34", t) }
func (s Style) Cyan(t string) string   { return s.wrap("36", t) }

type Renderer struct {
	w     io.Writer
	s     Style
	quiet bool
}

func NewRenderer(w io.Writer, quiet bool) *Renderer {
	return &Renderer{w: w, s: NewStyle(w), quiet: quiet}
}

func (r *Renderer) Style() Style { return r.s }

// Event renders one event. Tool calls get a single line; failures expand.
func (r *Renderer) Event(ev agent.Event) {
	switch ev.Type {
	case agent.EvAgentMessage:
		var m agent.Message
		if json.Unmarshal(ev.Payload, &m) == nil && strings.TrimSpace(m.Text) != "" {
			fmt.Fprintf(r.w, "\n%s\n", m.Text)
		}

	case agent.EvActionRequested:
		if r.quiet {
			return
		}
		var a agent.ActionRequested
		if json.Unmarshal(ev.Payload, &a) != nil {
			return
		}
		fmt.Fprintf(r.w, "%s %s %s\n",
			r.s.Cyan("●"), r.s.Bold(a.Tool), r.s.Dim(summarizeArgs(a.Tool, a.Args)))

	case agent.EvObservation:
		var o agent.Observation
		if json.Unmarshal(ev.Payload, &o) != nil {
			return
		}
		// Errors always show; successful output stays collapsed unless it is
		// the kind of result the user needs to see.
		if o.IsError {
			for _, line := range firstLines(o.Content, 8) {
				fmt.Fprintf(r.w, "  %s %s\n", r.s.Red("│"), line)
			}
			return
		}
		if r.quiet {
			return
		}
		if o.ExitCode != nil && *o.ExitCode != 0 {
			for _, line := range firstLines(o.Content, 12) {
				fmt.Fprintf(r.w, "  %s %s\n", r.s.Yellow("│"), line)
			}
			return
		}
		if summary := observationSummary(o); summary != "" {
			fmt.Fprintf(r.w, "  %s %s\n", r.s.Dim("└"), r.s.Dim(summary))
		}

	case agent.EvActionDenied:
		var m map[string]string
		if json.Unmarshal(ev.Payload, &m) == nil {
			fmt.Fprintf(r.w, "  %s %s\n", r.s.Red("✕"), r.s.Dim(m["reason"]))
		}

	case agent.EvSessionEnded:
		var e agent.SessionEnded
		if json.Unmarshal(ev.Payload, &e) != nil || r.quiet {
			return
		}
		if e.Reason != agent.TermCompleted {
			fmt.Fprintf(r.w, "\n%s %s\n", r.s.Yellow("!"), r.s.Dim("ended: "+string(e.Reason)))
		}
	}
}

// summarizeArgs renders the one useful detail per tool, so the line stays
// scannable. Verbosity is the default failure mode of agent CLIs.
func summarizeArgs(tool string, raw json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	str := func(k string) string {
		if v, found := m[k]; found {
			if s, isStr := v.(string); isStr {
				return s
			}
		}
		return ""
	}
	switch tool {
	case "read", "write", "edit":
		return str("path")
	case "glob":
		return str("pattern")
	case "grep":
		if p := str("path"); p != "" {
			return fmt.Sprintf("%q in %s", str("pattern"), p)
		}
		return fmt.Sprintf("%q", str("pattern"))
	case "bash":
		if d := str("description"); d != "" {
			return d
		}
		return truncate(str("command"), 60)
	case "task":
		return str("description")
	}
	return ""
}

func observationSummary(o agent.Observation) string {
	switch o.Tool {
	case "glob", "grep":
		n := strings.Count(strings.TrimSpace(o.Content), "\n") + 1
		if strings.HasPrefix(o.Content, "[no files") || strings.HasPrefix(o.Content, "No matches") {
			return "no results"
		}
		return fmt.Sprintf("%d line(s)", n)
	case "read":
		return fmt.Sprintf("%d line(s)", strings.Count(o.Content, "\n"))
	case "edit", "write":
		return firstLine(o.Content)
	case "bash":
		if o.ExitCode != nil {
			return fmt.Sprintf("exit %d", *o.ExitCode)
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func firstLines(s string, n int) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = append(lines[:n], fmt.Sprintf("... %d more lines", len(lines)-n))
	}
	return lines
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
