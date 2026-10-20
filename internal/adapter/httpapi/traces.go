// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

func (h *Handler) listTraces(w http.ResponseWriter, r *http.Request) {
	p, err := newParams(r.URL.Query(), "from", "to", "session_id", "user_id", "min_cost", "has_error",
		"service", "model", "provider", "kind", "status", "min_duration_ms", "name",
		"sort", "order", "limit", "cursor")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	req := queries.TraceListRequest{
		Filter: query.TraceFilter{
			From: p.time("from"), To: p.time("to"),
			SessionID: p.str("session_id"), UserID: p.str("user_id"),
			MinCostNanos: p.minCost(), HasError: p.hasError(), Span: p.spanFilter(),
		},
		Sort: query.Sort(p.str("sort")), Order: query.Order(p.str("order")), Page: p.page(),
	}
	if p.err != nil {
		h.fail(w, r, p.err)
		return
	}
	page, err := h.q.ListTraces(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	normalizeTraces(page.Data)
	if page.Data == nil {
		page.Data = []query.TraceSummary{}
	}
	httpmw.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) getTrace(w http.ResponseWriter, r *http.Request) {
	p, err := newParams(r.URL.Query(), "include_content")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	include, _ := p.boolean("include_content")
	if p.err != nil {
		h.fail(w, r, p.err)
		return
	}
	d, err := h.q.GetTrace(r.Context(), r.PathValue("trace_id"), include)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	normalizeTraces([]query.TraceSummary{d.Summary})
	if d.Spans == nil {
		d.Spans = []query.SpanView{}
	}
	httpmw.WriteJSON(w, http.StatusOK, d)
}

// normalizeTraces replaces nil slices by empty ones so they encode as [].
func normalizeTraces(rows []query.TraceSummary) {
	for i := range rows {
		if rows[i].Models == nil {
			rows[i].Models = []string{}
		}
	}
}
