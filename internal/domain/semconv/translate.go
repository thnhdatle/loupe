// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type Options struct {
	MaxAttrBytes int
	RedactValue  func(string) string
}

// Translate converts a raw OTLP span into a domain span.
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

func usageOf(ix attrIndex) (trace.Usage, bool) {
	in, out := ix.intField(FieldInputTokens), ix.intField(FieldOutputTokens)
	cr, cw, rs := ix.intField(FieldCacheRead), ix.intField(FieldCacheWrite), ix.intField(FieldReasoning)
	u := trace.Usage{Input: in.N, Output: out.N, CacheRead: cr.N, CacheWrite: cw.N, Reasoning: rs.N}
	u = applyInclusion(u, FamilyInclusive(FamilyOf(in.Key)), FamilyInclusive(FamilyOf(out.Key)))
	has := in.Present || out.Present || cr.Present || cw.Present || rs.Present
	return u, has
}

func applyInclusion(u trace.Usage, inclusiveIn, inclusiveOut bool) trace.Usage {
	if !inclusiveIn {
		u.Input += u.CacheRead + u.CacheWrite
	}
	if !inclusiveOut {
		u.Output += u.Reasoning
	}
	return u
}

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
