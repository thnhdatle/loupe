// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"math"
	"math/bits"
	"slices"
)

// Histogram layout. Regular buckets split every power-of-two octave
// of nanoseconds into SubBuckets linear sub-buckets, from MinValueNs to
// MaxValueNs inclusive. Index 0 is the underflow bucket [0, MinValueNs) (negative
// durations count there too) and index NumBuckets-1 the overflow bucket
// (MaxValueNs, +inf).
const (
	// MinValueNs is the lowest value of the regular buckets: 1 µs.
	MinValueNs int64 = 1_000
	// MaxValueNs is the highest value of the regular buckets: 24 h.
	MaxValueNs int64 = 86_400_000_000_000
	// SubBucketBits is log2 of SubBuckets.
	SubBucketBits = 6
	// SubBuckets is the number of linear sub-buckets per octave.
	SubBuckets = 1 << SubBucketBits
	// NumBuckets counts every bucket: underflow, 2322 regular buckets, overflow.
	NumBuckets = lastRegular + 2

	// minOctave is the octave holding MinValueNs (2^9 ≤ 1000 < 2^10).
	minOctave = 9
	// firstSub is the sub-bucket of MinValueNs in minOctave: (1000 >> 3) & 63;
	// its lower bound is exactly (64+61) << 3 = 1000.
	firstSub = 61
	// lastRegular is the index of the bucket holding MaxValueNs (octave 46, sub-bucket 14).
	lastRegular = 2322

	underflowIndex = 0
	overflowIndex  = lastRegular + 1
)

// bucket is one non-empty bucket of a sparse histogram.
type bucket struct {
	idx uint16
	n   uint64
}

// Histogram is a sparse log-linear latency histogram in nanoseconds. The zero
// value is an empty histogram ready to use. A Histogram must not be copied by
// value once used: copies share storage; use Clone.
type Histogram struct {
	buckets []bucket // sorted by idx, every n > 0
	total   uint64
}

// bucketIndex returns the bucket of a value in nanoseconds.
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

// bucketBounds returns [lo, hi) of a bucket; the overflow bucket returns
// [MaxValueNs+1, MaxValueNs+1).
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

// Add records one value in nanoseconds.
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

// Count returns the number of recorded values.
func (h *Histogram) Count() uint64 { return h.total }

// IsEmpty reports whether no value was recorded.
func (h *Histogram) IsEmpty() bool { return h.total == 0 }

// Reset empties the histogram and keeps its storage.
func (h *Histogram) Reset() {
	h.buckets = h.buckets[:0]
	h.total = 0
}

// Clone returns a deep copy.
func (h *Histogram) Clone() Histogram {
	if len(h.buckets) == 0 {
		return Histogram{}
	}
	return Histogram{buckets: slices.Clone(h.buckets), total: h.total}
}

// Merge adds every bucket of o to h. Merge is associative and commutative; it
// never retains o's storage, and h.Merge(h) doubles h.
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
	// Merge from the back so h's entries move at most once and o is only read.
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

// countMissing counts the buckets of o absent from h; both are sorted.
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

// addInPlace adds o's counts to h, which already holds every index of o.
func addInPlace(h, o []bucket) {
	i := 0
	for _, b := range o {
		for h[i].idx != b.idx {
			i++
		}
		h[i].n += b.n
	}
}

// NearestRank returns the nearest-rank order statistic ⌈q·n⌉ clamped to [1, n]
// (1-based); 0 when n is 0. It is the reference rank of every quantile here.
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

// Quantile estimates the q-quantile in nanoseconds. It finds the bucket holding
// the nearest-rank value r = ⌈q·n⌉ and interpolates linearly by rank inside the
// bucket bounds [lo, hi): with c values before the bucket and k in it, the
// estimate is lo + (hi−lo)·(r−c−½)/k, i.e. the centre of the value's slot when
// the k values are spread evenly. The underflow bucket interpolates over
// [0, MinValueNs); the overflow bucket reports MaxValueNs.
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
	// Unreachable while total equals the sum of the buckets.
	return MaxValueNs, nil
}

// estimate interpolates the r-th of k values (1-based) inside bucket idx.
func estimate(idx int, r, k uint64) int64 {
	if idx >= overflowIndex {
		return MaxValueNs
	}
	lo, hi := bucketBounds(idx)
	frac := (float64(r) - 0.5) / float64(k)
	return lo + int64(math.Floor(frac*float64(hi-lo)))
}
