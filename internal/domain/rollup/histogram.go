// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"math"
	"math/bits"
	"slices"
)

const (
	MinValueNs    int64 = 1_000
	MaxValueNs    int64 = 86_400_000_000_000
	SubBucketBits       = 6
	SubBuckets          = 1 << SubBucketBits
	NumBuckets          = lastRegular + 2

	minOctave   = 9
	firstSub    = 61
	lastRegular = 2322

	underflowIndex = 0
	overflowIndex  = lastRegular + 1
)

type bucket struct {
	idx uint16
	n   uint64
}

// Histogram is a sparse log-linear latency histogram in nanoseconds.
type Histogram struct {
	buckets []bucket
	total   uint64
}

func bucketIndex(v int64) int {
	switch {
	case v < MinValueNs:
		return underflowIndex
	case v > MaxValueNs:
		return overflowIndex
	}
	u := uint64(v)
	octave := bits.Len64(u) - 1
	sub := int((u >> (octave - SubBucketBits)) & (SubBuckets - 1))
	return 1 + (octave-minOctave)*SubBuckets + sub - firstSub
}

func bucketBounds(idx int) (lo, hi int64) {
	switch {
	case idx <= underflowIndex:
		return 0, MinValueNs
	case idx >= overflowIndex:
		return MaxValueNs + 1, MaxValueNs + 1
	}
	j := idx - 1 + firstSub
	octave := minOctave + j/SubBuckets
	sub := int64(j % SubBuckets)
	shift := octave - SubBucketBits
	lo = (SubBuckets + sub) << shift
	hi = (SubBuckets + sub + 1) << shift
	return lo, min(hi, MaxValueNs+1)
}

func (h *Histogram) Add(v int64) {
	idx := uint16(bucketIndex(v)) //nolint:gosec // G115: bucketIndex is in [0, NumBuckets).
	h.total++
	if n := len(h.buckets); n > 0 && h.buckets[n-1].idx < idx {
		h.buckets = append(h.buckets, bucket{idx: idx, n: 1})
		return
	}
	i, found := slices.BinarySearchFunc(h.buckets, idx, func(b bucket, t uint16) int {
		return int(b.idx) - int(t)
	})
	if found {
		h.buckets[i].n++
		return
	}
	h.buckets = slices.Insert(h.buckets, i, bucket{idx: idx, n: 1})
}

func (h *Histogram) Count() uint64 { return h.total }

func (h *Histogram) IsEmpty() bool { return h.total == 0 }

func (h *Histogram) Reset() {
	h.buckets = h.buckets[:0]
	h.total = 0
}

func (h *Histogram) Clone() Histogram {
	if len(h.buckets) == 0 {
		return Histogram{}
	}
	return Histogram{buckets: slices.Clone(h.buckets), total: h.total}
}

func (h *Histogram) Merge(o *Histogram) {
	if o == nil || len(o.buckets) == 0 {
		return
	}
	total := o.total
	missing := countMissing(h.buckets, o.buckets)
	if missing == 0 {
		addInPlace(h.buckets, o.buckets)
		h.total += total
		return
	}
	n := len(h.buckets)
	h.buckets = slices.Grow(h.buckets, missing)[:n+missing]
	i, j, k := n-1, len(o.buckets)-1, n+missing-1
	for j >= 0 {
		switch {
		case i >= 0 && h.buckets[i].idx > o.buckets[j].idx:
			h.buckets[k] = h.buckets[i]
			i--
		case i >= 0 && h.buckets[i].idx == o.buckets[j].idx:
			h.buckets[k] = bucket{idx: o.buckets[j].idx, n: h.buckets[i].n + o.buckets[j].n}
			i--
			j--
		default:
			h.buckets[k] = o.buckets[j]
			j--
		}
		k--
	}
	h.total += total
}

func countMissing(h, o []bucket) int {
	missing, i := 0, 0
	for _, b := range o {
		for i < len(h) && h[i].idx < b.idx {
			i++
		}
		if i == len(h) || h[i].idx != b.idx {
			missing++
		}
	}
	return missing
}

func addInPlace(h, o []bucket) {
	i := 0
	for _, b := range o {
		for h[i].idx != b.idx {
			i++
		}
		h[i].n += b.n
	}
}

func NearestRank(q float64, n uint64) uint64 {
	if n == 0 {
		return 0
	}
	r := math.Ceil(q * float64(n))
	switch {
	case r < 1:
		return 1
	case r >= float64(n):
		return n
	}
	return uint64(r)
}

func (h *Histogram) Quantile(q float64) (int64, error) {
	if !(q >= 0 && q <= 1) {
		return 0, ErrInvalidQuantile
	}
	if h.total == 0 {
		return 0, ErrEmptyHistogram
	}
	rank := NearestRank(q, h.total)
	var before uint64
	for _, b := range h.buckets {
		if before+b.n >= rank {
			return estimate(int(b.idx), rank-before, b.n), nil
		}
		before += b.n
	}
	return MaxValueNs, nil
}

func estimate(idx int, r, k uint64) int64 {
	if idx >= overflowIndex {
		return MaxValueNs
	}
	lo, hi := bucketBounds(idx)
	frac := (float64(r) - 0.5) / float64(k)
	return lo + int64(math.Floor(frac*float64(hi-lo)))
}
