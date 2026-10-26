package main

import "strings"

// New social glyphs use 12 cells doubled onto the existing 24-cell grid.
func doubleSocialGlyph(rows []string) []string {
	var out []string
	for _, row := range rows {
		var doubled strings.Builder
		for _, cell := range row {
			doubled.WriteRune(cell)
			doubled.WriteRune(cell)
		}
		out = append(out, doubled.String(), doubled.String())
	}
	return out
}
