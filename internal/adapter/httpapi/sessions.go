// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	p, err := newParams(r.URL.Query(), "from", "to", "user_id", "limit", "cursor")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	req := queries.SessionListRequest{
		Filter: query.SessionFilter{From: p.time("from"), To: p.time("to"), UserID: p.str("user_id")},
		Page:   p.page(),
	}
	if p.err != nil {
		h.fail(w, r, p.err)
		return
	}
	page, err := h.q.ListSessions(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if page.Data == nil {
		page.Data = []query.SessionSummary{}
	}
	httpmw.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	p, err := newParams(r.URL.Query(), "limit", "cursor")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d, err := h.q.GetSession(r.Context(), r.PathValue("id"), p.page())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	normalizeTraces(d.Data)
	if d.Data == nil {
		d.Data = []query.TraceSummary{}
	}
	httpmw.WriteJSON(w, http.StatusOK, d)
}
