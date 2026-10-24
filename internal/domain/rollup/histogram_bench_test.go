// SPDX-License-Identifier: Apache-2.0

package rollup_test

import (
	"testing"

	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/rollup/rolluptest"
)

func lognormalValues(n int) []int64 {
	return rolluptest.Samples(rolluptest.Distributions()[0], 0, n)
}

func BenchmarkHistogram_Add(b *testing.B) {
	values := lognormalValues(4096)
	var h rollup.Histogram
	for _, v := range values {
		h.Add(v)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		h.Add(values[i%len(values)])
	}
}

func BenchmarkHistogram_Merge(b *testing.B) {
	src := histogramOf(lognormalValues(10_000)...)
	var dst rollup.Histogram
	dst.Merge(src)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		dst.Merge(src)
	}
}

func BenchmarkHistogram_MarshalBinary(b *testing.B) {
	h := histogramOf(lognormalValues(10_000)...)
	buf := make([]byte, 0, 4096)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		buf, _ = h.AppendBinary(buf[:0])
	}
}

func BenchmarkHistogram_UnmarshalBinary(b *testing.B) {
	blob, _ := histogramOf(lognormalValues(10_000)...).MarshalBinary()
	var h rollup.Histogram
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := h.UnmarshalBinary(blob); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHistogram_Quantile(b *testing.B) {
	h := histogramOf(lognormalValues(10_000)...)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = h.Quantile(0.99)
	}
}
