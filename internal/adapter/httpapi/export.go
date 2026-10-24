// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/platform/clock"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

const (
	ExportFormatJSONL  = "jsonl"
	exportContentType  = "application/x-ndjson"
	exportWriteTimeout = 30 * time.Second
)

func (h *Handler) exportParams(r *http.Request) (query.ExportRequest, error) {
	p, err := newParams(r.URL.Query(), "from", "to", "format")
	if err != nil {
		return query.ExportRequest{}, err
	}
	if f := p.str("format"); f != "" && f != ExportFormatJSONL {
		p.fail("format must be %q", ExportFormatJSONL)
	}
	from, to := p.time("from"), p.time("to")
	if p.err != nil {
		return query.ExportRequest{}, p.err
	}
	var fp, tp *time.Time
	if !from.IsZero() {
		fp = &from
	}
	if !to.IsZero() {
		tp = &to
	}
	start, end, err := query.ResolveRange(fp, tp, h.clk.Now())
	if err != nil {
		return query.ExportRequest{}, fmt.Errorf("export range: %w", err)
	}
	return query.ExportRequest{From: start, To: end}, nil
}

type exportStream struct {
	w       http.ResponseWriter
	rc      *http.ResponseController
	clk     clock.Clock
	started bool
}

func (e *exportStream) start() {
	if e.started {
		return
	}
	e.started = true
	e.w.Header().Set("Content-Type", exportContentType)
	e.w.Header().Set("X-Content-Type-Options", "nosniff")
	e.w.WriteHeader(http.StatusOK)
}

func EncodeExportLines(w io.Writer, batch []query.TraceDetail) error {
	enc := json.NewEncoder(w)
	for i := range batch {
		if batch[i].Summary.Models == nil {
			batch[i].Summary.Models = []string{}
		}
		if batch[i].Spans == nil {
			batch[i].Spans = []query.SpanView{}
		}
		if err := enc.Encode(batch[i]); err != nil {
			return fmt.Errorf("encode trace: %w", err)
		}
	}
	return nil
}

func (e *exportStream) write(batch []query.TraceDetail) error {
	var buf bytes.Buffer
	if err := EncodeExportLines(&buf, batch); err != nil {
		return err
	}
	e.start()
	_ = e.rc.SetWriteDeadline(e.clk.Now().Add(exportWriteTimeout))
	if _, err := e.w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write export: %w", err)
	}
	if err := e.rc.Flush(); err != nil {
		return fmt.Errorf("flush export: %w", err)
	}
	return nil
}

func (h *Handler) exportTraces(w http.ResponseWriter, r *http.Request) {
	if h.opts.Exporter == nil {
		httpmw.WriteError(w, r, http.StatusNotFound, CodeNotFound, "export is not available")
		return
	}
	req, err := h.exportParams(r)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	st := &exportStream{w: w, rc: http.NewResponseController(w), clk: h.clk}
	err = h.opts.Exporter.ExportTraces(r.Context(), req, st.write)
	switch {
	case err == nil:
		st.start()
	case !st.started:
		h.fail(w, r, err)
	case h.opts.Logger != nil:
		h.opts.Logger.ErrorContext(r.Context(), "export aborted", "request_id", httpmw.RequestIDFrom(r.Context()),
			"path", r.URL.Path, "error", err)
	}
}
