// Package ui renders Titan's terminal surface.
package ui

import (
	"fmt"
	"strings"
)

// Titan's mark is a Doric column reduced to its three load-bearing parts: a
// capital, a fluted shaft, a base.
//
// The subject chose it. Titan is a harness — the structure that carries weight
// so the model can work — and the research this project rests on found that the
// harness, not the model, is the dominant variable. A column says "this holds
// something up" in a way an abstract glyph does not, and it survives the two
// places a mark actually has to live here: a 16px terminal cell and a 32px
// browser header.
//
// Rendered in three sizes so the same identity reads at any scale.

// MarkLarge is the startup banner: full column, drawn with box characters.
const MarkLarge = ` ▄▄▄▄▄▄▄▄▄ 
 █████████ 
   ║║║║║   
   ║║║║║   
   ║║║║║   
 ▄▄▄▄▄▄▄▄▄ 
▀▀▀▀▀▀▀▀▀▀▀`

// MarkSmall is the two-line form for a compact header.
const MarkSmall = `▄▀▀▀▄
▐│││▌`

// Glyph is the single-character form for prompts and log lines. A column in
// one cell: the capital sits on the shaft.
const Glyph = "⌸"

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
		s.Dim("on-prem coding agent"),
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
