// SPDX-License-Identifier: Apache-2.0

package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/thnhdatle/loupe/internal/app/analyze"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

const maxSpanRefs = 8

type spanRef struct {
	Number string
	Name   string
	Href   string
}

type pointView struct {
	Text string
	Refs []spanRef
	More int
}

type analysisData struct {
	Error    string
	Notice   string
	Causes   []pointView
	Fixes    []pointView
	Findings []pointView
	Omitted  int
}

func (h *Handler) analyzeTrace(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		h.renderError(w, r, http.StatusForbidden, "Cross-origin request rejected.")
		return
	}
	a := h.opts.Analyzer
	if a == nil {
		h.renderError(w, r, http.StatusNotFound, "Analyze is off: set analyze.base_url.")
		return
	}
	id := r.PathValue("id")
	httpmw.ExtendWriteDeadline(w, a.Timeout())
	rep, err := a.Analyze(r.Context(), id)
	if err != nil {
		h.analysisFailed(w, r, err)
		return
	}
	data, err := h.traceData(r, id)
	if err != nil {
		h.analysisFailed(w, r, err)
		return
	}
	data.Analysis = newAnalysis(rep, data.Rows)
	if isHTMX(r) {
		h.render(w, r, http.StatusOK, view{tracePage, "analysis"}, data.Analysis)
		return
	}
	h.renderPage(w, r, tracePage, data)
}

// analysisFailed answers htmx with 200 so the message replaces the analysis area instead of being dropped.
func (h *Handler) analysisFailed(w http.ResponseWriter, r *http.Request, err error) {
	status, msg := http.StatusInternalServerError, "Analysis failed; see the server log."
	switch {
	case errors.Is(err, analyze.ErrBusy):
		status, msg = http.StatusConflict, "An analysis of this trace is already running."
	case isNotFound(err):
		status, msg = http.StatusNotFound, "Not found."
	default:
		h.log.Error("analyze failed", "component", "web", "operation", r.URL.Path, "error", err.Error(),
			"request_id", httpmw.RequestIDFrom(r.Context()))
	}
	if isHTMX(r) {
		h.render(w, r, http.StatusOK, view{tracePage, "analysis"}, &analysisData{Error: msg})
		return
	}
	h.renderError(w, r, status, msg)
}

func newAnalysis(rep analyze.Report, rows []waterRow) *analysisData {
	refs := make(map[string]spanRef, len(rows))
	for _, r := range rows {
		refs[r.SpanID] = spanRef{Number: strconv.Itoa(r.Number), Name: r.Name, Href: r.Href}
	}
	points := func(ps []analyze.Point) []pointView {
		out := make([]pointView, 0, len(ps))
		for _, p := range ps {
			out = append(out, newPoint(p.Text, p.SpanIDs, refs))
		}
		return out
	}
	d := &analysisData{
		Notice: rep.Notice, Causes: points(rep.Causes), Fixes: points(rep.Fixes), Omitted: rep.SpansOmitted,
	}
	for _, f := range rep.Findings {
		d.Findings = append(d.Findings, newPoint(f.Text, f.SpanIDs, refs))
	}
	return d
}

func newPoint(text string, ids []string, refs map[string]spanRef) pointView {
	p := pointView{Text: text}
	for _, id := range ids {
		ref, ok := refs[id]
		switch {
		case !ok:
		case len(p.Refs) == maxSpanRefs:
			p.More++
		default:
			p.Refs = append(p.Refs, ref)
		}
	}
	return p
}
