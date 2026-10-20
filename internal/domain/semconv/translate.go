// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Options configures Translate.
type Options struct {
	// MaxAttrBytes truncates each non-content string value to this many bytes at
	// a UTF-8 boundary and sets trace.FlagAttrTruncated; zero or less is unlimited.
	MaxAttrBytes int
	// RedactValue, when non-nil, is applied to every non-content string value
	// before truncation. Content is never passed through it here.
	RedactValue func(string) string
}

// Translate maps a raw span to a domain span: typed fields from the alias table,
// the kind, status and error type, the canonical non-content attributes JSON
// (typed-field keys are kept in it too) and the content parts of every
// denylisted attribute and gen_ai.* span event.
//
// Span events that are not gen_ai.* are not stored; only the type of an
// exception event feeds ErrorType. Content never reaches Attributes and is
// never redacted or truncated here. Cost and ContentStripped are left for the
// application layer.
func Translate(raw *trace.RawSpan, opts Options) trace.Span {
	if raw == nil {
		return trace.Span{Kind: trace.KindOther, Status: trace.StatusUnset, Attributes: []byte("{}")}
	}
	ix := newAttrIndex(raw.Attributes)
	enc := encoder{redact: opts.RedactValue, maxBytes: opts.MaxAttrBytes}
	s := trace.Span{
		TraceID:      raw.TraceID,
		SpanID:       raw.SpanID,
		ParentSpanID: raw.ParentSpanID,
		Name:         raw.Name,
		StartNs:      raw.StartNs,
		EndNs:        raw.EndNs,
		Status:       trace.StatusFromCode(raw.StatusCode),
		ResourceID:   ResourceID(raw.Resource),
	}
	fillFields(&s, ix)
	s.ErrorType = errorType(raw, ix)
	s.Service = serviceName(raw.Resource)
	s.Model = enc.limit(s.Model, false)
	s.Provider = enc.limit(s.Provider, false)
	s.SessionID = enc.limit(s.SessionID, false)
	s.UserID = enc.limit(s.UserID, false)
	s.ErrorType = enc.limit(s.ErrorType, false)
	s.Service = enc.limit(s.Service, false)
	enc.object(raw.Attributes, func(k string) bool { return !IsContentKey(k) })
	s.Attributes = enc.buf
	if enc.truncated {
		s.Flags |= trace.FlagAttrTruncated
	}
	s.Content = spanContent(raw)
	return s
}

// fillFields sets model, provider, usage, session, user and kind.
func fillFields(s *trace.Span, ix attrIndex) {
	s.Model = ix.strField(FieldResponseModel)
	if s.Model == "" {
		s.Model = ix.strField(FieldRequestModel)
	}
	s.Provider = ix.strField(FieldProvider)
	s.SessionID = ix.strField(FieldSession)
	s.UserID = ix.strField(FieldUser)
	s.Usage, s.HasUsage = usageOf(ix)
	s.Kind = ResolveKind(ix.str(keysOf(FieldOperation)[0]), ix.strField(FieldOIKind), s.Model != "", s.HasUsage)
}

// usageOf resolves the usage counts and normalises them to inclusive semantics.
func usageOf(ix attrIndex) (trace.Usage, bool) {
	in, out := ix.intField(FieldInputTokens), ix.intField(FieldOutputTokens)
	cr, cw, rs := ix.intField(FieldCacheRead), ix.intField(FieldCacheWrite), ix.intField(FieldReasoning)
	u := trace.Usage{Input: in.N, Output: out.N, CacheRead: cr.N, CacheWrite: cw.N, Reasoning: rs.N}
	u = applyInclusion(u, FamilyInclusive(FamilyOf(in.Key)), FamilyInclusive(FamilyOf(out.Key)))
	has := in.Present || out.Present || cr.Present || cw.Present || rs.Present
	return u, has
}

// applyInclusion adds cache tokens to input and reasoning tokens to output for
// a family whose counts exclude them.
func applyInclusion(u trace.Usage, inclusiveIn, inclusiveOut bool) trace.Usage {
	if !inclusiveIn {
		u.Input += u.CacheRead + u.CacheWrite
	}
	if !inclusiveOut {
		u.Output += u.Reasoning
	}
	return u
}

// errorType returns the error.type attribute or, for an error span, the type of
// its first exception event.
func errorType(raw *trace.RawSpan, ix attrIndex) string {
	if t := ix.str("error.type"); t != "" {
		return t
	}
	if trace.StatusFromCode(raw.StatusCode) != trace.StatusError {
		return ""
	}
	for _, ev := range raw.Events {
		if ev.Name != "exception" {
			continue
		}
		if t := newAttrIndex(ev.Attributes).str("exception.type"); t != "" {
			return t
		}
	}
	return ""
}

func serviceName(res *trace.RawResource) string {
	if res == nil {
		return ""
	}
	return newAttrIndex(res.Attributes).str("service.name")
}
