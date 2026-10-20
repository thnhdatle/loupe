// SPDX-License-Identifier: Apache-2.0

package query_test

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

func genSort() *rapid.Generator[query.Sort] {
	return rapid.SampledFrom([]query.Sort{query.SortStart, query.SortCost, query.SortDuration})
}

func genOrder() *rapid.Generator[query.Order] {
	return rapid.SampledFrom([]query.Order{query.OrderDesc, query.OrderAsc})
}

func TestCursor_RoundTripProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		c := query.Cursor{SortKey: rapid.Int64().Draw(t, "key"), ID: rapid.String().Draw(t, "id")}
		s, o := genSort().Draw(t, "sort"), genOrder().Draw(t, "order")
		got, err := query.DecodeCursor(query.EncodeCursor(c, s, o), s, o)
		if err != nil || got != c {
			t.Fatalf("round trip: got %+v, %v; want %+v", got, err, c)
		}
	})
}

func TestCursor_OtherSortRejectedProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		c := query.Cursor{SortKey: rapid.Int64().Draw(t, "key"), ID: rapid.String().Draw(t, "id")}
		s, o := genSort().Draw(t, "sort"), genOrder().Draw(t, "order")
		s2, o2 := genSort().Draw(t, "sort2"), genOrder().Draw(t, "order2")
		if s == s2 && o == o2 {
			t.Skip()
		}
		_, err := query.DecodeCursor(query.EncodeCursor(c, s, o), s2, o2)
		if err == nil {
			t.Fatal("cursor accepted for another sort/order")
		}
	})
}

func TestCursor_DecodeNeverPanicsProperty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		_, _ = query.DecodeCursor(rapid.String().Draw(t, "s"), query.SortStart, query.OrderDesc)
	})
}

func TestDecodeCursor_Invalid(t *testing.T) {
	t.Parallel()
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	tests := []struct{ name, in string }{
		{"empty", ""},
		{"not base64", "!!!"},
		{"padded base64", "e30="},
		{"not json", enc("hello")},
		{"wrong version", enc(`{"v":2,"s":"start","o":"desc","k":1,"i":"x"}`)},
		{"missing version", enc(`{"s":"start","o":"desc"}`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := query.DecodeCursor(tc.in, query.SortStart, query.OrderDesc)
			require.ErrorIs(t, err, query.ErrInvalidCursor)
		})
	}
}

func TestCursor_TraceID(t *testing.T) {
	t.Parallel()
	id := trace.ID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}
	got, err := query.TraceCursor(7, id).TraceID()
	require.NoError(t, err)
	assert.Equal(t, id, got)

	_, err = query.Cursor{SortKey: 1, ID: "zz"}.TraceID()
	require.ErrorIs(t, err, query.ErrInvalidCursor)
}

func FuzzDecodeCursor(f *testing.F) {
	id := trace.ID{1}
	f.Add(query.EncodeCursor(query.TraceCursor(42, id), query.SortStart, query.OrderDesc))
	f.Add(query.EncodeCursor(query.Cursor{SortKey: -1, ID: "s1"}, query.SortCost, query.OrderAsc))
	f.Add("")
	f.Add("e30")
	f.Add("!!!")
	f.Fuzz(func(t *testing.T, s string) {
		c, err := query.DecodeCursor(s, query.SortStart, query.OrderDesc)
		if err != nil {
			return
		}
		got, err := query.DecodeCursor(query.EncodeCursor(c, query.SortStart, query.OrderDesc),
			query.SortStart, query.OrderDesc)
		if err != nil || got != c {
			t.Fatalf("re-encode mismatch: %+v vs %+v (%v)", got, c, err)
		}
	})
}
