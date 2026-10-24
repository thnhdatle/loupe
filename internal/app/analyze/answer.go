// SPDX-License-Identifier: Apache-2.0

package analyze

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	maxPoints     = 5
	maxPointRunes = 600
)

var errNoAnswer = errors.New("analyze: answer has no causes or fixes")

// Point is one cause or fix with the spans it refers to.
type Point struct {
	Text    string   `json:"text"`
	SpanIDs []string `json:"span_ids"`
}

type answer struct {
	Causes []Point `json:"causes"`
	Fixes  []Point `json:"fixes"`
}

// parseAnswer decodes the model's JSON answer and drops span IDs that are not in known.
func parseAnswer(raw string, known map[string]bool) (answer, error) {
	var a answer
	if err := json.Unmarshal([]byte(jsonObject(raw)), &a); err != nil {
		return answer{}, fmt.Errorf("analyze: decode answer: %w", err)
	}
	a.Causes, a.Fixes = clean(a.Causes, known), clean(a.Fixes, known)
	if len(a.Causes)+len(a.Fixes) == 0 {
		return answer{}, errNoAnswer
	}
	return a, nil
}

// jsonObject cuts the outermost object out of answers wrapped in code fences, prose or reasoning tags.
func jsonObject(raw string) string {
	if _, after, ok := strings.Cut(raw, "</think>"); ok {
		raw = after
	}
	start, end := strings.Index(raw, "{"), strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return raw
	}
	return raw[start : end+1]
}

func clean(points []Point, known map[string]bool) []Point {
	out := make([]Point, 0, min(len(points), maxPoints))
	for _, p := range points {
		text := strings.TrimSpace(p.Text)
		if text == "" {
			continue
		}
		if r := []rune(text); len(r) > maxPointRunes {
			text = string(r[:maxPointRunes]) + "…"
		}
		ids := []string{}
		seen := map[string]bool{}
		for _, id := range p.SpanIDs {
			id = strings.ToLower(strings.TrimSpace(id))
			if known[id] && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		out = append(out, Point{Text: text, SpanIDs: ids})
		if len(out) == maxPoints {
			break
		}
	}
	return out
}
