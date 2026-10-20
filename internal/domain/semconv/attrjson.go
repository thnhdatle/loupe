// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"encoding/base64"
	"hash/fnv"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// encoder serialises OTLP values to canonical JSON: keys sorted, first
// occurrence of a duplicate key wins, OTLP types mapped to JSON types.
type encoder struct {
	redact    func(string) string
	maxBytes  int
	truncated bool
	buf       []byte
}

// sortedAttrs returns attrs ordered by cmp on the key, keeping the first
// occurrence of a duplicate key and only keys for which keep (if non-nil) is true.
func sortedAttrs(attrs []trace.Attr, keep func(string) bool, cmp func(a, b string) int) []trace.Attr {
	out := make([]trace.Attr, 0, len(attrs))
	for _, a := range attrs {
		if keep == nil || keep(a.Key) {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b trace.Attr) int { return cmp(a.Key, b.Key) })
	return slices.CompactFunc(out, func(a, b trace.Attr) bool { return a.Key == b.Key })
}

func (e *encoder) object(attrs []trace.Attr, keep func(string) bool) {
	e.buf = append(e.buf, '{')
	for i, a := range sortedAttrs(attrs, keep, strings.Compare) {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.buf = appendQuoted(e.buf, a.Key)
		e.buf = append(e.buf, ':')
		e.value(a.Value)
	}
	e.buf = append(e.buf, '}')
}

func (e *encoder) value(v trace.Value) {
	switch v.Type {
	case trace.ValueString:
		e.buf = appendQuoted(e.buf, e.limit(v.Str, true))
	case trace.ValueBool:
		e.buf = strconv.AppendBool(e.buf, v.Bool)
	case trace.ValueInt:
		e.buf = strconv.AppendInt(e.buf, v.Int, 10)
	case trace.ValueDouble:
		e.double(v.Double)
	case trace.ValueBytes:
		e.buf = appendQuoted(e.buf, base64.StdEncoding.EncodeToString(v.Bytes))
	case trace.ValueArray:
		e.array(v.Array)
	case trace.ValueMap:
		e.object(v.Map, nil)
	case trace.ValueEmpty:
		e.buf = append(e.buf, "null"...)
	default:
		e.buf = append(e.buf, "null"...)
	}
}

func (e *encoder) double(f float64) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		e.buf = append(e.buf, "null"...)
		return
	}
	e.buf = strconv.AppendFloat(e.buf, f, 'g', -1, 64)
}

func (e *encoder) array(vs []trace.Value) {
	e.buf = append(e.buf, '[')
	for i, v := range vs {
		if i > 0 {
			e.buf = append(e.buf, ',')
		}
		e.value(v)
	}
	e.buf = append(e.buf, ']')
}

// limit applies the redact hook (when redactIt) and then truncates s to the
// maximum attribute size at a UTF-8 boundary, recording a truncation.
func (e *encoder) limit(s string, redactIt bool) string {
	if redactIt && e.redact != nil {
		s = e.redact(s)
	}
	if e.maxBytes <= 0 || len(s) <= e.maxBytes {
		return s
	}
	n := e.maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	e.truncated = true
	return s[:n]
}

// appendQuoted appends s as a JSON string; invalid UTF-8 becomes U+FFFD.
func appendQuoted(buf []byte, s string) []byte {
	const hexDigits = "0123456789abcdef"
	buf = append(buf, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			buf = append(buf, `�`...)
		case r == '"' || r == '\\':
			buf = append(buf, '\\', byte(r))
		case r == '\n':
			buf = append(buf, '\\', 'n')
		case r == '\r':
			buf = append(buf, '\\', 'r')
		case r == '\t':
			buf = append(buf, '\\', 't')
		case r < 0x20:
			buf = append(buf, '\\', 'u', '0', '0', hexDigits[r>>4], hexDigits[r&0xf])
		default:
			buf = append(buf, s[i:i+size]...)
		}
		i += size
	}
	return append(buf, '"')
}

// valueText renders a content value: strings as-is, everything else as JSON.
// It never redacts or truncates.
func valueText(v trace.Value) []byte {
	if v.Type == trace.ValueString {
		return []byte(v.Str)
	}
	e := encoder{}
	e.value(v)
	return e.buf
}

// objectText renders attrs as a canonical JSON object, without redaction or
// truncation; no attributes give an empty (non-nil) text.
func objectText(attrs []trace.Attr) []byte {
	if len(attrs) == 0 {
		return []byte{}
	}
	e := encoder{}
	e.object(attrs, nil)
	return e.buf
}

// canonicalResourceJSON serialises the resource attributes as sorted JSON.
func canonicalResourceJSON(res *trace.RawResource) []byte {
	e := encoder{}
	var attrs []trace.Attr
	if res != nil {
		attrs = res.Attributes
	}
	e.object(attrs, nil)
	return e.buf
}

// ResourceID is the stable id of a resource: FNV-1a 64 over its canonical
// attributes JSON. A nil resource has id 0.
func ResourceID(res *trace.RawResource) uint64 {
	if res == nil {
		return 0
	}
	h := fnv.New64a()
	_, _ = h.Write(canonicalResourceJSON(res))
	return h.Sum64()
}

// CanonicalResource returns the stored form of a raw resource.
func CanonicalResource(res *trace.RawResource) trace.Resource {
	return trace.Resource{ID: ResourceID(res), Attributes: canonicalResourceJSON(res)}
}
