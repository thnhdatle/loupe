// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"net/url"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
)

type sessionsData struct {
	base
	Range    rangeView
	UserID   string
	Rows     []query.SessionSummary
	NextHref string
}

func (h *Handler) sessionList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rv, err := resolveRange(q, h.clk.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	page, err := h.q.ListSessions(r.Context(), queries.SessionListRequest{
		Filter: query.SessionFilter{From: rv.From, To: rv.To, UserID: q.Get("user_id")},
		Page:   query.Page{Limit: pageLimit, Cursor: q.Get("cursor")},
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPage(w, r, "sessions.html", sessionsData{
		base: h.base(r, "Sessions", "sessions"), Range: rv, UserID: q.Get("user_id"), Rows: page.Data,
		NextHref: sessionNext("/sessions", q, page.NextCursor),
	})
}

func sessionNext(path string, q url.Values, cursor string) string {
	if cursor == "" {
		return ""
	}
	n := url.Values{}
	for _, k := range []string{"range", "from", "to", "user_id"} {
		if v := q.Get(k); v != "" {
			n.Set(k, v)
		}
	}
	n.Set("cursor", cursor)
	return path + "?" + n.Encode()
}

type timelineRow struct {
	query.TraceSummary
	X, W int
}

type sessionDetailData struct {
	base
	Summary  query.SessionSummary
	Rows     []timelineRow
	NextHref string
}

func (h *Handler) sessionDetail(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	d, err := h.q.GetSession(r.Context(), r.PathValue("id"), query.Page{Limit: pageLimit, Cursor: q.Get("cursor")})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	lo, hi := d.Summary.FirstNs, d.Summary.LastNs
	for i := range d.Data {
		lo, hi = min(lo, d.Data[i].StartNs), max(hi, d.Data[i].EndNs)
	}
	total := float64(max(hi-lo, 1))
	rows := make([]timelineRow, 0, len(d.Data))
	for _, t := range d.Data {
		wd := max(int(float64(t.DurationNs)/total*waterScale), minBarWidth)
		x := min(int(float64(t.StartNs-lo)/total*waterScale), waterScale-wd)
		rows = append(rows, timelineRow{TraceSummary: t, X: max(x, 0), W: wd})
	}
	h.renderPage(w, r, "session.html", sessionDetailData{
		base: h.base(r, "Session "+d.Summary.ID, "sessions"), Summary: d.Summary, Rows: rows,
		NextHref: sessionNextDetail(r.URL.Path, d.NextCursor),
	})
}

func sessionNextDetail(path, cursor string) string {
	if cursor == "" {
		return ""
	}
	return path + "?" + url.Values{"cursor": {cursor}}.Encode()
}
