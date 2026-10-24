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
	var limited []string
	var limitedText string
	var keys []failureKey
	ids := map[failureKey][]string{}
	for i := range spans {
		s := &spans[i]
		text := errorText(s)
		switch {
		case rateLimited(s, text):
			if limited == nil {
				limitedText = text
			}
			limited = append(limited, s.SpanID)
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
	if len(limited) > 0 {
		out = append(out, Finding{Kind: KindRateLimit, Text: rateLimitText(len(limited), limitedText), SpanIDs: limited})
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
