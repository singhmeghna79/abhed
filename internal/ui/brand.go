// Package ui renders Titan's terminal surface.
package ui

import (
	"fmt"
	"strings"
)

// Titan's mark is a column set inside a hexagon.
//
// Both halves carry meaning. The hexagon is the shared vocabulary of
// infrastructure marks — Kubernetes, Docker, Terraform, Vault — and reads as
// "a component in a system", which is what Titan is. Inside it, a T built as a
// column: capital, fluted shaft, base. Titan is a harness, the structure that
// carries weight so the model can work, and the research this project rests on
// found the harness, not the model, to be the dominant variable.
//
// Rendered in three sizes because a mark has to survive both places it lives:
// a single terminal cell and a 128px browser header. The flutes are drawn only
// in the large form; below about 32px they fill in and muddy the shape.

// MarkLarge is the startup banner: the hexagon with the column-T inside it,
// the same construction as brand/titan-mark.svg. Half-block characters give
// the diagonal edges a slope that full blocks cannot.
const MarkLarge = `    ▄▄████▄▄    
  ▟███▀▀▀▀███▙  
 ██▘ ▄▄▄▄▄▄ ▝██ 
▐█▘   ▀██▀   ▜█▌
▐█▖    ██    ▟█▌
 ██▖  ▄██▄  ▗██ 
  ▜███▄▄▄▄███▛  
    ▀▀████▀▀    `

// MarkSmall is the two-line form for a compact header.
const MarkSmall = `▗▄██▄▖
▝█▀█▀█▘`

// Glyph is the single-character form for prompts and log lines: a hexagon,
// which is the one shape of the mark that survives a single terminal cell.
const Glyph = "⬢"

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
