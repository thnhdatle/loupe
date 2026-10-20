// SPDX-License-Identifier: Apache-2.0

package rollup

import (
	"encoding/binary"
	"fmt"
)

// EncodingVersion is the first byte of every histogram blob.
//
// Version 1 layout: version byte, uvarint number of non-empty buckets, then per
// bucket in ascending index order a uvarint gap (index − previous index − 1,
// the previous index of the first bucket being −1) and a uvarint count > 0.
const EncodingVersion byte = 1

// AppendBinary appends the encoded histogram to b (encoding.BinaryAppender).
func (h *Histogram) AppendBinary(b []byte) ([]byte, error) {
	b = append(b, EncodingVersion)
	b = binary.AppendUvarint(b, uint64(len(h.buckets)))
	prev := -1
	for _, bk := range h.buckets {
		b = binary.AppendUvarint(b, uint64(int(bk.idx)-prev-1)) //nolint:gosec // G115: indices ascend.
		b = binary.AppendUvarint(b, bk.n)
		prev = int(bk.idx)
	}
	return b, nil
}

// MarshalBinary encodes the histogram (encoding.BinaryMarshaler). It never fails.
func (h *Histogram) MarshalBinary() ([]byte, error) {
	return h.AppendBinary(make([]byte, 0, 2+4*len(h.buckets)))
}

// UnmarshalBinary replaces h with the decoded blob, reusing h's storage. It
// returns ErrUnsupportedVersion or ErrCorruptHistogram (wrapped) for a bad
// blob; h is then empty.
func (h *Histogram) UnmarshalBinary(data []byte) error {
	if err := h.decode(data); err != nil {
		h.Reset()
		return err
	}
	return nil
}

func (h *Histogram) decode(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("%w: empty blob", ErrCorruptHistogram)
	}
	if data[0] != EncodingVersion {
		return fmt.Errorf("%w: version %d", ErrUnsupportedVersion, data[0])
	}
	r := reader{buf: data[1:]}
	n, err := r.uvarint("bucket count")
	if err != nil {
		return err
	}
	if n > NumBuckets {
		return fmt.Errorf("%w: %d buckets, at most %d", ErrCorruptHistogram, n, NumBuckets)
	}
	h.buckets = h.buckets[:0]
	h.total = 0
	prev := -1
	for range n {
		gap, err := r.uvarint("bucket gap")
		if err != nil {
			return err
		}
		if gap >= uint64(NumBuckets-1-prev) {
			return fmt.Errorf("%w: bucket index beyond %d", ErrCorruptHistogram, NumBuckets-1)
		}
		cnt, err := r.uvarint("bucket count value")
		if err != nil {
			return err
		}
		if cnt == 0 || h.total+cnt < h.total {
			return fmt.Errorf("%w: zero or overflowing bucket count", ErrCorruptHistogram)
		}
		// gap < NumBuckets − 1 − prev was checked, so prev stays below NumBuckets.
		prev += int(gap) + 1                                             //nolint:gosec // G115: bounded above.
		h.buckets = append(h.buckets, bucket{idx: uint16(prev), n: cnt}) //nolint:gosec // G115: bounded above.
		h.total += cnt
	}
	if len(r.buf) != 0 {
		return fmt.Errorf("%w: %d trailing bytes", ErrCorruptHistogram, len(r.buf))
	}
	return nil
}

// reader consumes uvarints from a byte slice.
type reader struct {
	buf []byte
}

func (r *reader) uvarint(what string) (uint64, error) {
	v, n := binary.Uvarint(r.buf)
	if n <= 0 {
		return 0, fmt.Errorf("%w: truncated or overlong %s", ErrCorruptHistogram, what)
	}
	r.buf = r.buf[n:]
	return v, nil
}
