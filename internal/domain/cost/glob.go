// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"strings"
	"unicode/utf8"
)

// MatchGlobPattern reports whether name matches the price-entry glob pattern.
//
// Pattern syntax: '*' matches any sequence of characters, including the empty
// sequence and '/'; '?' matches exactly one character (one UTF-8 rune); every
// other character, including '[', ']' and '\', matches itself. There are no
// character classes and no escapes. Matching is case-sensitive and anchored at
// both ends. Unlike path.Match, '*' crosses '/', because model ids such as
// "accounts/fireworks/models/llama-v3" contain slashes.
func MatchGlobPattern(pattern, name string) bool {
	px, nx := 0, 0
	starPx, starNx := -1, 0
	for px < len(pattern) || nx < len(name) {
		if px < len(pattern) {
			switch c := pattern[px]; {
			case c == '*':
				starPx, starNx = px, nx
				px++
				continue
			case c == '?' && nx < len(name):
				_, w := utf8.DecodeRuneInString(name[nx:])
				px, nx = px+1, nx+w
				continue
			case c != '?' && nx < len(name) && name[nx] == c:
				px, nx = px+1, nx+1
				continue
			}
		}
		if starPx < 0 || starNx >= len(name) {
			return false
		}
		// Backtrack: let the last '*' swallow one more rune.
		_, w := utf8.DecodeRuneInString(name[starNx:])
		starNx += w
		px, nx = starPx+1, starNx
	}
	return true
}

// literalCount is the specificity of a glob pattern: the number of runes that
// are not wildcards. An exact entry is more specific than any glob.
func literalCount(pattern string) int {
	return utf8.RuneCountInString(pattern) - strings.Count(pattern, "*") - strings.Count(pattern, "?")
}
