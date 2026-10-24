// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/thnhdatle/loupe/internal/app/ingest"
)

var (
	errBodyTooLarge = errors.New("otlp: request body too large")
	errInflight     = errors.New("otlp: in-flight byte limit reached")
	errBadBody      = errors.New("otlp: malformed request body")
)

type guardReader struct {
	r    io.Reader
	lim  *ingest.InflightLimiter
	max  int64
	n    int64
	held int64
}

func (g *guardReader) Read(p []byte) (int, error) {
	n, err := g.r.Read(p)
	if n > 0 {
		g.n += int64(n)
		if g.n > g.max {
			return 0, errBodyTooLarge
		}
		if !g.lim.TryAcquire(int64(n)) {
			return 0, errInflight
		}
		g.held += int64(n)
	}
	return n, err //nolint:wrapcheck // io.EOF and transport errors must stay comparable
}

func (g *guardReader) release() {
	g.lim.Release(g.held)
	g.held = 0
}

func (s *Server) readBody(body io.Reader, gzipped bool) (data []byte, release func(), err error) {
	g := &guardReader{lim: s.lim, max: s.cfg.MaxRequestBytes}
	release = g.release
	g.r = body
	if gzipped {
		zr, zerr := gzip.NewReader(body)
		if zerr != nil {
			return nil, release, fmt.Errorf("%w: gzip header: %w", errBadBody, zerr)
		}
		defer zr.Close() //nolint:errcheck // reading side, nothing to flush
		g.r = zr
	}
	data, err = io.ReadAll(g)
	switch {
	case err == nil:
		return data, release, nil
	case errors.Is(err, errBodyTooLarge), errors.Is(err, errInflight):
		return nil, release, fmt.Errorf("read body: %w", err)
	default:
		return nil, release, fmt.Errorf("%w: %w", errBadBody, err)
	}
}

func unmarshalProto(data []byte, m proto.Message) error {
	if err := proto.Unmarshal(data, m); err != nil {
		return fmt.Errorf("%w: protobuf: %w", errBadBody, err)
	}
	return nil
}

func unmarshalJSON(data []byte, m proto.Message) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("%w: json: %w", errBadBody, err)
	}
	if err := rewriteHexIDs(doc); err != nil {
		return err
	}
	fixed, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%w: json: %w", errBadBody, err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(fixed, m); err != nil {
		return fmt.Errorf("%w: json: %w", errBadBody, err)
	}
	return nil
}

func rewriteHexIDs(v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if err := rewriteHexEntry(x, k, e); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := rewriteHexIDs(e); err != nil {
				return err
			}
		}
	}
	return nil
}

func rewriteHexEntry(m map[string]any, k string, e any) error {
	if s, ok := e.(string); ok && isIDKey(k) {
		b, err := hex.DecodeString(s)
		if err != nil {
			return fmt.Errorf("%w: %s is not hex: %w", errBadBody, k, err)
		}
		m[k] = base64.StdEncoding.EncodeToString(b)
		return nil
	}
	return rewriteHexIDs(e)
}

func isIDKey(k string) bool {
	switch k {
	case "traceId", "spanId", "parentSpanId", "trace_id", "span_id", "parent_span_id":
		return true
	}
	return false
}
