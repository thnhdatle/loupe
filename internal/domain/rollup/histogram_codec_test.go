// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
)

func TestHistogram_MarshalBinary_Layout(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		h    *rollup.Histogram
		want []byte
	}{
		{name: "empty", h: histogramOf(), want: []byte{1, 0}},
		{name: "edges", h: histogramOf(0, 5, 1000, rollup.MaxValueNs+1), want: []byte{
			1, 3,
			0, 2,
			0, 1,
			0x91, 0x12, 1,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := tc.h.MarshalBinary()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			appended, err := tc.h.AppendBinary([]byte{0xff})
			require.NoError(t, err)
			assert.Equal(t, append([]byte{0xff}, tc.want...), appended)
		})
	}
}

func TestHistogram_UnmarshalBinary_RoundTrip(t *testing.T) {
	t.Parallel()
	h := histogramOf(-1, 0, 1000, 1e6, 1e6, 3e9, rollup.MaxValueNs, rollup.MaxValueNs+1)
	b := encoded(t, h)
	got := histogramOf(5e9)
	require.NoError(t, got.UnmarshalBinary(b))
	assert.Equal(t, h.Count(), got.Count())
	assert.Equal(t, b, encoded(t, got))
}

func TestHistogram_UnmarshalBinary_Corrupt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		blob []byte
		want error
	}{
		{name: "nil", blob: nil, want: rollup.ErrCorruptHistogram},
		{name: "unknown version", blob: []byte{2, 0}, want: rollup.ErrUnsupportedVersion},
		{name: "version zero", blob: []byte{0}, want: rollup.ErrUnsupportedVersion},
		{name: "missing bucket count", blob: []byte{1}, want: rollup.ErrCorruptHistogram},
		{name: "too many buckets", blob: []byte{1, 0x95, 0x12}, want: rollup.ErrCorruptHistogram},
		{name: "truncated gap", blob: []byte{1, 1}, want: rollup.ErrCorruptHistogram},
		{name: "truncated count", blob: []byte{1, 1, 0}, want: rollup.ErrCorruptHistogram},
		{name: "unterminated varint", blob: []byte{1, 1, 0, 0x80}, want: rollup.ErrCorruptHistogram},
		{
			name: "overlong varint", blob: []byte{1, 1, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f},
			want: rollup.ErrCorruptHistogram,
		},
		{name: "zero count", blob: []byte{1, 1, 0, 0}, want: rollup.ErrCorruptHistogram},
		{name: "index past overflow", blob: []byte{1, 1, 0x94, 0x12, 1}, want: rollup.ErrCorruptHistogram},
		{name: "second index past overflow", blob: []byte{1, 2, 0x92, 0x12, 1, 1, 1}, want: rollup.ErrCorruptHistogram},
		{name: "total overflows", blob: []byte{
			1, 2, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01, 0, 1,
		}, want: rollup.ErrCorruptHistogram},
		{name: "trailing bytes", blob: []byte{1, 0, 0}, want: rollup.ErrCorruptHistogram},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := histogramOf(1e6)
			err := h.UnmarshalBinary(tc.blob)
			require.ErrorIs(t, err, tc.want)
			assert.True(t, h.IsEmpty(), "a failed decode leaves the histogram empty")
		})
	}
}

func TestHistogram_UnmarshalBinary_LastIndexAccepted(t *testing.T) {
	t.Parallel()
	var h rollup.Histogram
	require.NoError(t, h.UnmarshalBinary([]byte{1, 1, 0x93, 0x12, 7}))
	assert.Equal(t, uint64(7), h.Count())
	got, err := h.Quantile(0.5)
	require.NoError(t, err)
	assert.Equal(t, rollup.MaxValueNs, got)
}

func FuzzHistogramDecode(f *testing.F) {
	f.Add([]byte{1, 0})
	f.Add([]byte{1, 3, 0, 2, 0, 1, 0x91, 0x12, 1})
	f.Add([]byte{2, 0})
	f.Add([]byte{1, 1, 0x93, 0x12, 7})
	f.Fuzz(func(t *testing.T, blob []byte) {
		var h rollup.Histogram
		if err := h.UnmarshalBinary(blob); err != nil {
			require.True(t, h.IsEmpty())
			return
		}
		canon := encoded(t, &h)
		var again rollup.Histogram
		require.NoError(t, again.UnmarshalBinary(canon))
		require.Equal(t, canon, encoded(t, &again))
		require.Equal(t, h.Count(), again.Count())
		if !h.IsEmpty() {
			v, err := h.Quantile(0.5)
			require.NoError(t, err)
			require.GreaterOrEqual(t, v, int64(0))
			require.LessOrEqual(t, v, rollup.MaxValueNs)
		}
	})
}
