// SPDX-License-Identifier: Apache-2.0

package sqlite

import (
	"fmt"

	"github.com/klauspost/compress/zstd"
)

const maxContentBytes = 64 << 20

type codec struct {
	enc *zstd.Encoder
	dec *zstd.Decoder
}

func newCodec(readers int) (*codec, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedDefault))
	if err != nil {
		return nil, fmt.Errorf("sqlite: zstd encoder: %w", err)
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(readers),
		zstd.WithDecoderMaxMemory(maxContentBytes))
	if err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("sqlite: zstd decoder: %w", err)
	}
	return &codec{enc: enc, dec: dec}, nil
}

func (c *codec) compress(body []byte) []byte {
	if body == nil {
		return nil
	}
	return c.enc.EncodeAll(body, make([]byte, 0, len(body)/2+16))
}

func (c *codec) decompress(blob []byte) ([]byte, error) {
	if blob == nil {
		return nil, nil
	}
	out, err := c.dec.DecodeAll(blob, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlite: decompress content: %w", err)
	}
	if out == nil {
		out = []byte{}
	}
	return out, nil
}

func (c *codec) size(blob []byte) (int, error) {
	var h zstd.Header
	if err := h.Decode(blob); err == nil && h.HasFCS && h.FrameContentSize <= maxContentBytes {
		return int(h.FrameContentSize), nil
	}
	out, err := c.decompress(blob)
	return len(out), err
}

func (c *codec) close() {
	_ = c.enc.Close()
	c.dec.Close()
}
