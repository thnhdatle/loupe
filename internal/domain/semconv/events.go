// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"slices"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const (
	genAIPrefix           = "gen_ai."
	OperationDetailsEvent = "gen_ai.client.inference.operation.details"
)

func eventRole(name string) string {
	switch name {
	case "gen_ai.system.message":
		return roleSystem
	case "gen_ai.user.message":
		return roleUser
	case "gen_ai.assistant.message", "gen_ai.choice":
		return roleAssistant
	case "gen_ai.tool.message":
		return roleTool
	default:
		return ""
	}
}

func EventName(rec *trace.RawLogRecord) string {
	if rec.EventName != "" {
		return rec.EventName
	}
	ix := newAttrIndex(rec.Attributes)
	if n := ix.str("event.name"); n != "" {
		return n
	}
	return ix.str("otel.event.name")
}

func attrsParts(base trace.ContentPart, attrs []trace.Attr) []trace.ContentPart {
	var content *trace.Value
	var rest []trace.Attr
	for _, a := range sortedAttrs(attrs, nil, strings.Compare) {
		switch {
		case a.Key == keyRole && scalarOK(a.Value):
			if base.Role == "" {
				base.Role, _ = scalarString(a.Value)
			}
		case a.Key == "content":
			content = &a.Value
		case isEventMetadata(a.Key):
		default:
			rest = append(rest, a)
		}
	}
	var parts []trace.ContentPart
	if content != nil {
		parts = append(parts, withBody(base, valueText(*content)))
	}
	if len(rest) > 0 || content == nil {
		parts = append(parts, withBody(base, objectText(rest)))
	}
	return parts
}

func isEventMetadata(key string) bool {
	return key == "event.name" || key == "otel.event.name" || slices.Contains(keysOf(FieldProvider), key)
}

func scalarOK(v trace.Value) bool {
	_, ok := scalarString(v)
	return ok
}

func withBody(base trace.ContentPart, body []byte) trace.ContentPart {
	base.Body = body
	base.Bytes = len(body)
	return base
}

// TranslateLog converts an OTLP log record into content parts and reports whether to keep it.
func TranslateLog(rec *trace.RawLogRecord) (parts []trace.ContentPart, reason string, keep bool) {
	name := EventName(rec)
	if !strings.HasPrefix(name, genAIPrefix) {
		return nil, trace.ReasonNotGenAI, false
	}
	if rec.TraceID.IsZero() || rec.SpanID.IsZero() {
		return nil, trace.ReasonNoSpanContext, false
	}
	base := trace.ContentPart{Source: trace.SourceLog, Key: name, Role: eventRole(name), TimeNs: rec.TimeNs}
	roleAttr := newAttrIndex(rec.Attributes).str(keyRole)
	if base.Role == "" {
		base.Role = roleAttr
	}
	parts = append(parts, bodyParts(base, rec.Body)...)
	attrs := make([]trace.Attr, 0, len(rec.Attributes))
	for _, a := range rec.Attributes {
		if !isEventMetadata(a.Key) && (a.Key != keyRole || roleAttr == "") {
			attrs = append(attrs, a)
		}
	}
	if len(attrs) > 0 {
		parts = append(parts, attrsParts(base, attrs)...)
	}
	if len(parts) == 0 {
		parts = append(parts, withBody(base, []byte{}))
	}
	return number(parts, rec.TraceID, rec.SpanID), "", true
}

func bodyParts(base trace.ContentPart, body trace.Value) []trace.ContentPart {
	switch {
	case body.Type == trace.ValueEmpty:
		return nil
	case body.Type == trace.ValueMap && base.Key == OperationDetailsEvent:
		fields := sortedAttrs(body.Map, nil, strings.Compare)
		parts := make([]trace.ContentPart, 0, len(fields))
		for _, f := range fields {
			p := base
			p.Key = f.Key
			if rule, ok := ruleFor(f.Key); ok {
				p.Role = rule.Role
			}
			parts = append(parts, withBody(p, valueText(f.Value)))
		}
		return parts
	case body.Type == trace.ValueMap:
		return attrsParts(base, body.Map)
	default:
		return []trace.ContentPart{withBody(base, valueText(body))}
	}
}
