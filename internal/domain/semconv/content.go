// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"strconv"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// messagePrefixes are the indexed message families: the key is
// <prefix><N>.<rest>, and rest is a role key when it is in roleSuffixes.
var (
	messagePrefixes = []string{"llm.input_messages.", "llm.output_messages.", "gen_ai.prompt.", "gen_ai.completion."}
	roleSuffixes    = []string{"message.role", keyRole}
)

// messageGroup splits an indexed message key into its group (prefix and index)
// and reports whether the key is the role of that message.
func messageGroup(key string) (group string, isRole, ok bool) {
	for _, p := range messagePrefixes {
		rest, found := strings.CutPrefix(key, p)
		if !found {
			continue
		}
		idx, tail, found := strings.Cut(rest, ".")
		if !found || idx == "" || strings.Trim(idx, "0123456789") != "" {
			return "", false, false
		}
		for _, suffix := range roleSuffixes {
			if tail == suffix {
				return p + idx, true, true
			}
		}
		return p + idx, false, true
	}
	return "", false, false
}

// compareKeys orders keys segment by segment, comparing all-digit segments as
// numbers so that message 2 sorts before message 10.
func compareKeys(a, b string) int {
	for a != "" && b != "" {
		sa, ra, _ := strings.Cut(a, ".")
		sb, rb, _ := strings.Cut(b, ".")
		if c := compareSegments(sa, sb); c != 0 {
			return c
		}
		a, b = ra, rb
	}
	return strings.Compare(a, b)
}

func compareSegments(a, b string) int {
	na, errA := strconv.ParseUint(a, 10, 64)
	nb, errB := strconv.ParseUint(b, 10, 64)
	if errA == nil && errB == nil && na != nb {
		if na < nb {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

// attrContentParts turns every denylisted attribute into content parts, one
// per key, in natural key order. The role key of an indexed message is not a
// part of its own: its value is the Role of the message's other parts (a
// message that has only a role keeps the role key as its part).
func attrContentParts(attrs []trace.Attr) []trace.ContentPart {
	content := sortedAttrs(attrs, IsContentKey, compareKeys)
	roles := make(map[string]string)
	hasBody := make(map[string]bool)
	for _, a := range content {
		group, isRole, ok := messageGroup(a.Key)
		switch {
		case !ok:
		case isRole:
			if s, isStr := scalarString(a.Value); isStr {
				roles[group] = s
			}
		default:
			hasBody[group] = true
		}
	}
	parts := make([]trace.ContentPart, 0, len(content))
	for _, a := range content {
		group, isRole, ok := messageGroup(a.Key)
		if ok && isRole && hasBody[group] {
			continue
		}
		rule, _ := ruleFor(a.Key)
		role := rule.Role
		if r := roles[group]; ok && r != "" {
			role = r
		}
		body := valueText(a.Value)
		parts = append(parts, trace.ContentPart{
			Source: trace.SourceAttribute, Key: a.Key, Role: role, Bytes: len(body), Body: body,
		})
	}
	return parts
}

// number assigns Part 0..n-1 and the span identity to parts.
func number(parts []trace.ContentPart, tid trace.ID, sid trace.SpanID) []trace.ContentPart {
	for i := range parts {
		parts[i].Part = i
		parts[i].TraceID = tid
		parts[i].SpanID = sid
	}
	return parts
}

// spanContent collects the content parts of a raw span: denylisted attributes
// first (natural key order), then gen_ai.* span events in event order.
func spanContent(raw *trace.RawSpan) []trace.ContentPart {
	parts := attrContentParts(raw.Attributes)
	for _, ev := range raw.Events {
		if !strings.HasPrefix(ev.Name, genAIPrefix) {
			continue
		}
		base := trace.ContentPart{Source: trace.SourceEvent, Key: ev.Name, Role: eventRole(ev.Name), TimeNs: ev.TimeNs}
		parts = append(parts, attrsParts(base, ev.Attributes)...)
	}
	if len(parts) == 0 {
		return nil
	}
	return number(parts, raw.TraceID, raw.SpanID)
}
