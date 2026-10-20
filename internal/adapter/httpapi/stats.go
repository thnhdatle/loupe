// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"time"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

type statsView struct {
	Requests  int64       `json:"requests"`
	Errors    int64       `json:"errors"`
	Usage     trace.Usage `json:"usage"`
	CostNanos int64       `json:"cost_nanos"`
	Unpriced  int64       `json:"unpriced"`
	P50Ns     int64       `json:"p50_ns"`
	P95Ns     int64       `json:"p95_ns"`
	P99Ns     int64       `json:"p99_ns"`
}

type groupView struct {
	Key   string    `json:"key"`
	Other bool      `json:"other"`
	Users int       `json:"users"`
	Stats statsView `json:"stats"`
}

type pointView struct {
	Bucket string    `json:"bucket"`
	Key    string    `json:"key"`
	Other  bool      `json:"other"`
	Stats  statsView `json:"stats"`
}

type overviewView struct {
	From     string      `json:"from"`
	To       string      `json:"to"`
	GroupBy  string      `json:"group_by"`
	Interval string      `json:"interval"`
	Total    statsView   `json:"total"`
	Groups   []groupView `json:"groups"`
	Series   []pointView `json:"series"`
}

func newStatsView(s queries.Stats) statsView {
	return statsView{
		Requests: s.Requests, Errors: s.Errors, Usage: s.Usage, CostNanos: s.CostNanos,
		Unpriced: s.Unpriced, P50Ns: s.P50Ns, P95Ns: s.P95Ns, P99Ns: s.P99Ns,
	}
}

func newOverviewView(o queries.Overview) overviewView {
	v := overviewView{
		From: o.From.UTC().Format(time.RFC3339), To: o.To.UTC().Format(time.RFC3339),
		GroupBy: string(o.GroupBy), Interval: string(o.Interval), Total: newStatsView(o.Total),
		Groups: make([]groupView, 0, len(o.Groups)), Series: make([]pointView, 0, len(o.Series)),
	}
	for _, g := range o.Groups {
		v.Groups = append(v.Groups, groupView{Key: g.Key, Other: g.Other, Users: g.Users, Stats: newStatsView(g.Stats)})
	}
	for _, s := range o.Series {
		v.Series = append(v.Series, pointView{
			Bucket: s.Bucket.UTC().Format(time.RFC3339), Key: s.Key, Other: s.Other, Stats: newStatsView(s.Stats),
		})
	}
	return v
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	p, err := newParams(r.URL.Query(), "from", "to", "group_by", "interval", "kind")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	req := query.OverviewRequest{
		From: p.time("from"), To: p.time("to"),
		GroupBy:  query.GroupBy(p.str("group_by")),
		Interval: query.Interval(p.str("interval")),
		Kind:     trace.Kind(p.str("kind")),
	}
	if p.err != nil {
		h.fail(w, r, p.err)
		return
	}
	o, err := h.q.Overview(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpmw.WriteJSON(w, http.StatusOK, newOverviewView(o))
}

func (h *Handler) topSpans(w http.ResponseWriter, r *http.Request) {
	p, err := newParams(r.URL.Query(), "from", "to", "by", "limit")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	req := query.TopSpansRequest{From: p.time("from"), To: p.time("to"), By: query.TopBy(p.str("by")), Limit: p.limit()}
	if p.err != nil {
		h.fail(w, r, p.err)
		return
	}
	out, err := h.q.TopSpans(r.Context(), req)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if out == nil {
		out = []query.TopSpan{}
	}
	httpmw.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
}

func (h *Handler) ingestStats(w http.ResponseWriter, r *http.Request) {
	if _, err := newParams(r.URL.Query()); err != nil {
		h.fail(w, r, err)
		return
	}
	st, err := h.q.IngestStats(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	for _, m := range []*map[string]int64{&st.Rejected, &st.Dropped, &st.Discarded} {
		if *m == nil {
			*m = map[string]int64{}
		}
	}
	httpmw.WriteJSON(w, http.StatusOK, st)
}
