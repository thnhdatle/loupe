// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

type filterForm struct {
	From, To, Service, Model, Provider, Kind, Status, Name string
	MinCost, MinDurationMs, SessionID, UserID              string
	HasError                                               bool
	Sort, Order                                            string
}

var filterKeys = []string{
	"from", "to", "service", "model", "provider", "kind", "status", "name",
	"min_cost", "min_duration_ms", "session_id", "user_id", "has_error", "sort", "order",
}

func newFilterForm(q url.Values) filterForm {
	return filterForm{
		From: q.Get("from"), To: q.Get("to"), Service: q.Get("service"), Model: q.Get("model"),
		Provider: q.Get("provider"), Kind: q.Get("kind"), Status: q.Get("status"), Name: q.Get("name"),
		MinCost: q.Get("min_cost"), MinDurationMs: q.Get("min_duration_ms"),
		SessionID: q.Get("session_id"), UserID: q.Get("user_id"),
		HasError: isTrue(q.Get("has_error")), Sort: q.Get("sort"), Order: q.Get("order"),
	}
}

func isTrue(s string) bool { return s == "1" || s == trueValue || s == "on" }

func (f filterForm) request(cursor string) (queries.TraceListRequest, error) {
	req := queries.TraceListRequest{
		Filter: query.TraceFilter{
			SessionID: f.SessionID, UserID: f.UserID,
			Span: query.SpanFilter{
				Service: f.Service, Model: f.Model, Provider: f.Provider, NamePrefix: f.Name,
				Kind: trace.Kind(f.Kind), Status: trace.Status(f.Status),
			},
		},
		Sort: query.Sort(f.Sort), Order: query.Order(f.Order),
		Page: query.Page{Limit: pageLimit, Cursor: cursor},
	}
	var err error
	if req.Filter.From, err = parseOptionalTime(f.From); err != nil {
		return req, fmt.Errorf("%w: from: %w", query.ErrInvalidRange, err)
	}
	if req.Filter.To, err = parseOptionalTime(f.To); err != nil {
		return req, fmt.Errorf("%w: to: %w", query.ErrInvalidRange, err)
	}
	if f.MinCost != "" {
		n, err := query.ParseUSD(f.MinCost)
		if err != nil {
			return req, fmt.Errorf("min_cost: %w", err)
		}
		req.Filter.MinCostNanos = &n
	}
	if f.MinDurationMs != "" {
		ms, err := strconv.ParseInt(f.MinDurationMs, 10, 64)
		if err != nil || ms < 0 {
			return req, fmt.Errorf("%w: min_duration_ms must be a non-negative integer", query.ErrInvalidFilter)
		}
		req.Filter.Span.MinDurationNs = ms * int64(time.Millisecond)
	}
	if f.HasError {
		t := true
		req.Filter.HasError = &t
	}
	return req, nil
}

type tracesData struct {
	base
	Form     filterForm
	Rows     []query.TraceSummary
	NextHref string
}

func nextHref(path string, q url.Values, cursor string) string {
	if cursor == "" {
		return ""
	}
	n := url.Values{}
	for _, k := range filterKeys {
		if v := q.Get(k); v != "" {
			n.Set(k, v)
		}
	}
	n.Set("cursor", cursor)
	return path + "?" + n.Encode()
}

func (h *Handler) traces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	form := newFilterForm(q)
	req, err := form.request(q.Get("cursor"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.q.ListTraces(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := tracesData{
		base: h.base(r, "Traces", "traces"), Form: form, Rows: page.Data,
		NextHref: nextHref("/traces", q, page.NextCursor),
	}
	if isHTMX(r) {
		h.render(w, r, http.StatusOK, view{"traces.html", "traces_results"}, data)
		return
	}
	h.renderPage(w, r, "traces.html", data)
}
