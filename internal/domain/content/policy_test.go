// SPDX-License-Identifier: Apache-2.0

package content_test

import (
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/content"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func part(i int, role, body string) trace.ContentPart {
	return trace.ContentPart{
		Part: i, Role: role, Source: trace.SourceEvent, Key: "gen_ai." + role + ".message",
		Body: []byte(body), TimeNs: 5,
	}
}

func TestPolicy_Apply_NoParts(t *testing.T) {
	t.Parallel()
	for _, m := range []content.Mode{content.ModeOff, content.ModeOn, content.ModeMetadataOnly} {
		out, stripped := content.Apply(content.Policy{Mode: m}, nil)
		assert.Nil(t, out)
		assert.False(t, stripped)
	}
}

func TestPolicy_Apply_Off(t *testing.T) {
	t.Parallel()
	in := []trace.ContentPart{part(3, "system", "abc"), part(4, "user", "hello")}
	in[1].Bytes = 100
	in[0].TraceID = trace.ID{1}
	in[0].SpanID = trace.SpanID{2}
	for _, mode := range []content.Mode{content.ModeOff, "bogus"} {
		out, stripped := content.Apply(content.Policy{Mode: mode, MaxBytes: 1}, in)
		require.Len(t, out, 1)
		assert.True(t, stripped)
		want := trace.ContentPart{
			TraceID: in[0].TraceID, SpanID: in[0].SpanID, Source: trace.SourceEvent,
			Key: in[0].Key, Bytes: 103,
		}
		assert.Equal(t, want, out[0])
		assert.Nil(t, out[0].Body)
	}
	assert.Equal(t, 0, in[0].Bytes, "input untouched")
}

func TestPolicy_Apply_MetadataOnly(t *testing.T) {
	t.Parallel()
	in := []trace.ContentPart{part(0, "system", "abc"), part(1, "user", "héllo")}
	in[1].Bytes = 50
	out, stripped := content.Apply(content.Policy{Mode: content.ModeMetadataOnly}, in)
	assert.True(t, stripped)
	require.Len(t, out, 2)
	for i, o := range out {
		assert.Nil(t, o.Body)
		assert.Equal(t, in[i].Role, o.Role)
		assert.Equal(t, in[i].Key, o.Key)
		assert.Equal(t, in[i].Part, o.Part)
		assert.Equal(t, in[i].Source, o.Source)
	}
	assert.Equal(t, 3, out[0].Bytes)
	assert.Equal(t, 50, out[1].Bytes)
	assert.NotNil(t, in[0].Body)
}

func TestPolicy_Apply_On(t *testing.T) {
	t.Parallel()
	red := mustRedactor(t, []string{"email"}, nil)
	tests := []struct {
		name       string
		policy     content.Policy
		bodies     []string
		wantBodies []string
		wantTrunc  []bool
		wantBytes  []int
	}{
		{
			name: "no limit", policy: content.Policy{Mode: content.ModeOn},
			bodies: []string{"aaaa", "bbbb"}, wantBodies: []string{"aaaa", "bbbb"},
			wantTrunc: []bool{false, false}, wantBytes: []int{4, 4},
		},
		{
			name: "fits exactly", policy: content.Policy{Mode: content.ModeOn, MaxBytes: 8},
			bodies: []string{"aaaa", "bbbb"}, wantBodies: []string{"aaaa", "bbbb"},
			wantTrunc: []bool{false, false}, wantBytes: []int{4, 4},
		},
		{
			name: "cut in second then exhausted", policy: content.Policy{Mode: content.ModeOn, MaxBytes: 6},
			bodies: []string{"aaaa", "bbbb", "cccc"}, wantBodies: []string{"aaaa", "bb", ""},
			wantTrunc: []bool{false, true, true}, wantBytes: []int{4, 4, 4},
		},
		{
			name: "exhausted exactly then later part", policy: content.Policy{Mode: content.ModeOn, MaxBytes: 4},
			bodies: []string{"aaaa", "bbbb"}, wantBodies: []string{"aaaa", ""},
			wantTrunc: []bool{false, true}, wantBytes: []int{4, 4},
		},
		{
			name: "utf8 boundary", policy: content.Policy{Mode: content.ModeOn, MaxBytes: 4},
			bodies: []string{"aé€"}, wantBodies: []string{"aé"},
			wantTrunc: []bool{true}, wantBytes: []int{6},
		},
		{
			name: "utf8 cut to zero", policy: content.Policy{Mode: content.ModeOn, MaxBytes: 2},
			bodies: []string{"€x"}, wantBodies: []string{""},
			wantTrunc: []bool{true}, wantBytes: []int{4},
		},
		{
			name: "redact before budget keeps original bytes", policy: content.Policy{
				Mode: content.ModeOn, MaxBytes: 7, Redactor: red,
			},
			bodies: []string{"bob@example.com"}, wantBodies: []string{"[EMAIL]"},
			wantTrunc: []bool{false}, wantBytes: []int{15},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := make([]trace.ContentPart, len(tc.bodies))
			for i, b := range tc.bodies {
				in[i] = part(i, "user", b)
			}
			out, stripped := content.Apply(tc.policy, in)
			assert.False(t, stripped)
			require.Len(t, out, len(in))
			for i, o := range out {
				assert.Equal(t, tc.wantBodies[i], string(o.Body))
				assert.NotNil(t, o.Body)
				assert.Equal(t, tc.wantTrunc[i], o.Truncated)
				assert.Equal(t, tc.wantBytes[i], o.Bytes)
				assert.Equal(t, in[i].Role, o.Role)
				assert.Equal(t, in[i].Part, o.Part)
				assert.True(t, utf8.Valid(o.Body))
			}
			for i, b := range tc.bodies {
				assert.Equal(t, b, string(in[i].Body), "input not mutated")
			}
		})
	}
}

func TestPolicy_Apply_OnKeepsMarkerAndExplicitBytes(t *testing.T) {
	t.Parallel()
	marker := trace.ContentPart{Part: 0, Bytes: 9}
	explicit := part(1, "user", "abc")
	explicit.Bytes = 77
	explicit.Truncated = true
	out, _ := content.Apply(content.Policy{Mode: content.ModeOn, MaxBytes: 10}, []trace.ContentPart{marker, explicit})
	assert.Nil(t, out[0].Body)
	assert.Equal(t, 9, out[0].Bytes)
	assert.Equal(t, 77, out[1].Bytes)
	assert.True(t, out[1].Truncated)
}

func TestBudget_Take(t *testing.T) {
	t.Parallel()
	b := content.NewBudget(10, 4, false)
	got, cut := b.Take([]byte("abc"))
	assert.Equal(t, "abc", string(got))
	assert.False(t, cut)
	got, cut = b.Take([]byte("€€"))
	assert.Equal(t, "€", string(got))
	assert.True(t, cut)
	got, cut = b.Take([]byte(""))
	assert.Empty(t, got)
	assert.True(t, cut, "an exhausted budget marks every later part")

	full := content.NewBudget(10, 25, false)
	got, cut = full.Take([]byte("x"))
	assert.Empty(t, got)
	assert.True(t, cut)

	gap := content.NewBudget(10, 9, true)
	got, cut = gap.Take([]byte("x"))
	assert.Empty(t, got, "a truncated span stays exhausted")
	assert.True(t, cut)

	ex := content.NewBudget(10, 0, false)
	ex.Exhaust()
	_, cut = ex.Take([]byte("x"))
	assert.True(t, cut)

	unlimited := content.NewBudget(0, 1<<30, true)
	got, cut = unlimited.Take([]byte("abc"))
	assert.Equal(t, "abc", string(got))
	assert.False(t, cut)
}
