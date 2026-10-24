// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"strings"
	"unicode/utf8"
)

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
		_, w := utf8.DecodeRuneInString(name[starNx:])
		starNx += w
		px, nx = starPx+1, starNx
	}
	return true
}

func literalCount(pattern string) int {
	return utf8.RuneCountInString(pattern) - strings.Count(pattern, "*") - strings.Count(pattern, "?")
}
