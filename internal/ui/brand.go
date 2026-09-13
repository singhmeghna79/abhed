// Package ui renders Titan's terminal surface.
package ui

import (
	"fmt"
	"strings"
)

// Titan's mark is an open frame around a single point.
//
// The frame is the harness; the point is the model it carries. The research
// this project rests on found the scaffold around the model, not the model, to
// be the dominant variable, and the mark says exactly that — the frame is the
// product. It is drawn open on the right, at the model's eye line, because a
// harness is something a model is placed into rather than a sealed box.
//
// Rendered in three sizes because a mark has to survive both places it lives:
// a single terminal cell and a 128px browser header.

// MarkLarge is the startup banner: the frame with the point inside it, the
// same construction as brand/titan-mark.svg. Eight lines, because Banner lays
// the run facts beside it and both columns have to end together.
const MarkLarge = `▛▀▀▀▀▀▀▀▀▀▜ 
▌         ▐ 
▌   ▄██▄   ▐
▌  ██████   
▌  ██████   
▌   ▀██▀   ▐
▌         ▐ 
▙▄▄▄▄▄▄▄▄▄▟ `

// MarkSmall is the two-line form for a compact header.
const MarkSmall = `▛▀●▀▜
▙▄▄▄▟`

// Glyph is the single-character form for prompts and log lines: a point inside
// a ring, which is the one form of the mark that survives one terminal cell.
const Glyph = "◎"

// Banner renders the startup identity block.
//
// Deliberately restrained: an engineer sees this on every invocation, and a
// banner that entertains on the first run irritates on the hundredth. It earns
// its space by carrying the four facts that change between runs — model,
// workspace, sandbox tier, storage — not by being decorative.
func Banner(s Style, version, model, workspace, sandbox, storage string) string {
	var b strings.Builder

	col := strings.Split(MarkLarge, "\n")
	// Facts sit beside the mark rather than beneath it, so the block stays
	// seven lines instead of twelve.
	rows := []string{
		s.Bold("TITAN") + "  " + s.Dim(version),
		s.Dim("deep agent harness · on-prem"),
		"",
		s.Dim("model    ") + model,
		s.Dim("work     ") + workspace,
		s.Dim("sandbox  ") + sandbox,
		s.Dim("storage  ") + storage,
	}

	// Pad by RUNE count: the box-drawing characters are multi-byte, so %-11s
	// would align on bytes and stagger the right-hand column.
	width := 0
	for _, line := range col {
		if n := len([]rune(line)); n > width {
			width = n
		}
	}
	for i, line := range col {
		right := ""
		if i < len(rows) {
			right = rows[i]
		}
		pad := strings.Repeat(" ", width-len([]rune(line)))
		fmt.Fprintf(&b, "  %s%s   %s\n", s.Cyan(line), pad, right)
	}
	return b.String()
}

// Prompt is the input marker: the glyph, not a bare chevron.
func Prompt(s Style) string {
	return s.Cyan(Glyph) + " "
}

// Rule draws a labelled separator, used between turns so a long session stays
// scannable.
func Rule(s Style, label string, width int) string {
	if width <= 0 {
		width = 72
	}
	if label == "" {
		return s.Dim(strings.Repeat("─", width))
	}
	head := "── " + label + " "
	if n := width - len([]rune(head)); n > 0 {
		head += strings.Repeat("─", n)
	}
	return s.Dim(head)
}
