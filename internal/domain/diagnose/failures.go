// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"fmt"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var rateLimitPhrases = []string{
	"rate limit", "ratelimit", "rate_limit", "too many requests", "toomanyrequests", "resource_exhausted",
}

type failureKey struct {
	kind Kind
	name string
	text string
}

func failures(spans []query.SpanView) []Finding {
	var limited []int
	var keys []failureKey
	ids := map[failureKey][]string{}
	for i := range spans {
		s := &spans[i]
		text := errorText(s)
		switch {
		case rateLimited(s, text):
			limited = append(limited, i)
		case s.Status == trace.StatusError:
			k := failureKey{kind: KindError, name: s.Name, text: text}
			if s.Kind == trace.KindTool {
				k.kind = KindToolError
			}
			if _, ok := ids[k]; !ok {
				keys = append(keys, k)
			}
			ids[k] = append(ids[k], s.SpanID)
		}
	}
	var out []Finding
	if limited = innermost(spans, limited); len(limited) > 0 {
		limitedIDs := make([]string, len(limited))
		for j, i := range limited {
			limitedIDs[j] = spans[i].SpanID
		}
		text := rateLimitText(len(limited), errorText(&spans[limited[0]]))
		out = append(out, Finding{Kind: KindRateLimit, Text: text, SpanIDs: limitedIDs})
	}
	for _, kind := range []Kind{KindToolError, KindError} {
		for _, k := range keys {
			if k.kind == kind {
				out = append(out, Finding{Kind: kind, Text: k.describe(len(ids[k])), SpanIDs: ids[k]})
			}
		}
	}
	return out
}

// innermost drops spans whose rate limit only repeats the one of a descendant.
func innermost(spans []query.SpanView, limited []int) []int {
	parent := make(map[string]string, len(spans))
	for i := range spans {
		parent[spans[i].SpanID] = spans[i].ParentSpanID
	}
	covered := map[string]bool{}
	for _, i := range limited {
		for p := parent[spans[i].SpanID]; p != "" && !covered[p]; p = parent[p] {
			covered[p] = true
		}
	}
	var out []int
	for _, i := range limited {
		if !covered[spans[i].SpanID] {
			out = append(out, i)
		}
	}
	return out
}

func rateLimitText(n int, text string) string {
	msg := "1 call was rate limited"
	if n > 1 {
		msg = fmt.Sprintf("%d calls were rate limited", n)
	}
	if text != "" {
		msg += ": " + text
	}
	return msg
}

func (k failureKey) describe(n int) string {
	subject := quote(k.name)
	if k.kind == KindToolError {
		subject = "tool " + subject
	}
	msg := subject + " failed"
	if n > 1 {
		msg += fmt.Sprintf(" %d times", n)
	}
	if k.text == "" {
		return msg + " without an error message"
	}
	return msg + ": " + k.text
}

func rateLimited(s *query.SpanView, text string) bool {
	if statusCode(attributes(s)) == 429 {
		return true
	}
	if s.Status != trace.StatusError {
		return false
	}
	lower := strings.ToLower(text)
	for _, p := range rateLimitPhrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
