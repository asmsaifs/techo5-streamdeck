package render

import (
	"strings"
	"unicode/utf8"
)

// wrapLabel breaks text into at most maxLines lines no wider than width, as measure counts it. It
// breaks at spaces, inside a word only when the word alone is too wide, and ends the last line
// with an ellipsis when the text does not all fit. whole is false when it had to cut anything.
func wrapLabel(text string, width float64, maxLines int, measure func(string) float64) (lines []string, whole bool) {
	words := strings.Fields(text)
	var cur string
	for len(words) > 0 {
		next := words[0]
		if cur != "" {
			next = cur + " " + words[0]
		}
		if measure(next) <= width {
			cur = next
			words = words[1:]
			continue
		}
		if cur == "" {
			// A word wider than a line: take as many of its runes as fit.
			n := fitRunes(words[0], width, measure)
			cur, words[0] = words[0][:n], words[0][n:]
			if words[0] == "" {
				words = words[1:]
			}
		}
		lines = append(lines, cur)
		cur = ""
		if len(lines) == maxLines {
			break
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	if len(words) == 0 {
		return lines, true
	}
	last := len(lines) - 1
	lines[last] = ellipsize(lines[last], width, measure)
	return lines, false
}

// fitRunes is how many bytes of w, a whole number of runes and at least one, fit in width.
func fitRunes(w string, width float64, measure func(string) float64) int {
	n := 0
	for i, r := range w {
		end := i + utf8.RuneLen(r)
		if n > 0 && measure(w[:end]) > width {
			break
		}
		n = end
	}
	return n
}

// ellipsize shortens s until s plus "…" fits.
func ellipsize(s string, width float64, measure func(string) float64) string {
	const dots = "…"
	for s != "" && measure(s+dots) > width {
		_, n := utf8.DecodeLastRuneInString(s)
		s = strings.TrimRight(s[:len(s)-n], " ")
	}
	return s + dots
}
