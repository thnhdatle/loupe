// SPDX-License-Identifier: Apache-2.0

package semconv_test

import (
	"testing"

	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/semconv"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func genKeyPool() []string {
	rules := semconv.ContentKeyRules()
	pool := make([]string, 0, 10+len(rules))
	pool = append(pool,
		"gen_ai.usage.input_tokens", "gen_ai.request.model", "user.id", "session.id", "http.method",
		"custom.key", "llm.token_count.total", "gen_ai.tool.name", "service.version", "x",
	)
	for _, r := range rules {
		pool = append(pool, r.Sample)
	}
	return pool
}

func genValue() *rapid.Generator[trace.Value] {
	return rapid.OneOf(
		rapid.Map(rapid.String(), trace.StringValue),
		rapid.Map(rapid.Int64(), trace.IntValue),
		rapid.Map(rapid.Bool(), trace.BoolValue),
		rapid.Map(rapid.Float64(), trace.DoubleValue),
	)
}

func genAttrs(pool []string) *rapid.Generator[[]trace.Attr] {
	attr := rapid.Custom(func(t *rapid.T) trace.Attr {
		return trace.Attr{Key: rapid.SampledFrom(pool).Draw(t, "key"), Value: genValue().Draw(t, "value")}
	})
	return rapid.SliceOfN(attr, 0, 24)
}

func TestTranslate_AttributesProperty_NonContentKeptContentAbsent(t *testing.T) {
	t.Parallel()
	pool := genKeyPool()
	rapid.Check(t, func(t *rapid.T) {
		attrs := genAttrs(pool).Draw(t, "attrs")
		maxBytes := rapid.IntRange(0, 20).Draw(t, "max")
		s := semconv.Translate(rawSpan(attrs...), semconv.Options{MaxAttrBytes: maxBytes})
		checkKeySplit(t, attrs, &s)
	})
}

func checkKeySplit(t *rapid.T, attrs []trace.Attr, s *trace.Span) {
	t.Helper()
	m := attrsJSON(t, s.Attributes)
	hasContent := false
	for _, a := range attrs {
		_, inAttrs := m[a.Key]
		if semconv.IsContentKey(a.Key) {
			hasContent = true
			if inAttrs {
				t.Fatalf("content key %q in attributes", a.Key)
			}
		} else if !inAttrs {
			t.Fatalf("non-content key %q lost", a.Key)
		}
	}
	for k := range m {
		if semconv.IsContentKey(k) {
			t.Fatalf("content key %q in attributes", k)
		}
	}
	if hasContent && len(s.Content) == 0 {
		t.Fatal("content keys produced no parts")
	}
	for i, p := range s.Content {
		if p.Part != i {
			t.Fatalf("part %d numbered %d", i, p.Part)
		}
	}
}

func TestTranslate_AttributesProperty_OrderIndependent(t *testing.T) {
	t.Parallel()
	pool := genKeyPool()
	rapid.Check(t, func(t *rapid.T) {
		attrs := genAttrs(pool).Draw(t, "attrs")
		seen := map[string]bool{}
		uniq := attrs[:0:0]
		for _, a := range attrs {
			if !seen[a.Key] {
				seen[a.Key] = true
				uniq = append(uniq, a)
			}
		}
		perm := rapid.Permutation(uniq).Draw(t, "perm")
		a := semconv.Translate(rawSpan(uniq...), semconv.Options{})
		b := semconv.Translate(rawSpan(perm...), semconv.Options{})
		if string(a.Attributes) != string(b.Attributes) {
			t.Fatalf("attributes differ:\n%s\n%s", a.Attributes, b.Attributes)
		}
		if a.Usage != b.Usage || a.Kind != b.Kind || len(a.Content) != len(b.Content) {
			t.Fatal("typed fields depend on attribute order")
		}
	})
}
