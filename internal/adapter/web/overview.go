// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"sort"
	"time"

	"github.com/thnhdatle/loupe/internal/app/queries"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

const maxChartSeries = 8

type overviewData struct {
	base
	Range  rangeView
	Total  queries.Stats
	Groups []queries.GroupStats
	Chart  chartData
	Banner *ingestBanner
}

type ingestBanner struct {
	Rejected int64
	Dropped  int64
	From, To time.Time
	Reasons  []string
}

type chartData struct {
	X        []int64      `json:"x"`
	Models   []string     `json:"models"`
	Cost     [][]*float64 `json:"cost"`
	Tokens   [][]*float64 `json:"tokens"`
	P95      [][]*float64 `json:"p95"`
	Interval string       `json:"interval"`
}

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	rv, err := resolveRange(r.URL.Query(), h.clk.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ov, err := h.q.Overview(r.Context(), query.OverviewRequest{
		From: rv.From, To: rv.To, GroupBy: query.GroupModel, Interval: rv.Interval, Kind: trace.KindLLM,
	})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	data := overviewData{
		base: h.base(r, "Overview", "overview"), Range: rv, Total: ov.Total, Groups: ov.Groups,
		Chart: buildChart(ov),
	}
	if st, err := h.q.IngestInRange(r.Context(), rv.From, rv.To); err != nil {
		h.log.Warn("ingest stats", "component", "web", "error", err.Error(), "request_id", httpmw.RequestIDFrom(r.Context()))
	} else {
		data.Banner = newBanner(st, rv)
	}
	h.renderPage(w, r, "overview.html", data)
}

func newBanner(st query.IngestStats, rv rangeView) *ingestBanner {
	b := &ingestBanner{}
	for _, m := range []struct {
		name string
		by   map[string]int64
		sum  *int64
	}{{"rejected", st.Rejected, &b.Rejected}, {"dropped", st.Dropped, &b.Dropped}} {
		reasons := make([]string, 0, len(m.by))
		for reason, n := range m.by {
			if n > 0 {
				*m.sum += n
				reasons = append(reasons, m.name+" "+reason+": "+formatInt(n))
			}
		}
		sort.Strings(reasons)
		b.Reasons = append(b.Reasons, reasons...)
	}
	if b.Rejected+b.Dropped == 0 {
		return nil
	}
	b.From, b.To = rv.From, rv.To
	return b
}

func buildChart(ov queries.Overview) chartData {
	cd := chartData{Interval: string(ov.Interval), X: []int64{}, Models: []string{}}
	idx := make(map[string]int)
	for _, g := range ov.Groups {
		if len(cd.Models) == maxChartSeries {
			break
		}
		idx[g.Key] = len(cd.Models)
		cd.Models = append(cd.Models, g.Key)
	}
	seen := make(map[int64]bool)
	for _, p := range ov.Series {
		if x := p.Bucket.Unix(); !seen[x] {
			seen[x] = true
			cd.X = append(cd.X, x)
		}
	}
	sort.Slice(cd.X, func(i, j int) bool { return cd.X[i] < cd.X[j] })
	pos := make(map[int64]int, len(cd.X))
	for i, x := range cd.X {
		pos[x] = i
	}
	cd.Cost, cd.Tokens, cd.P95 = nullGrid(len(cd.Models), len(cd.X)), nullGrid(len(cd.Models), len(cd.X)),
		nullGrid(len(cd.Models), len(cd.X))
	for _, p := range ov.Series {
		m, ok := idx[p.Key]
		if !ok {
			continue
		}
		i := pos[p.Bucket.Unix()]
		cd.Cost[m][i] = ptr(float64(p.Stats.CostNanos) / 1e9)
		cd.Tokens[m][i] = ptr(float64(totalTokens(p.Stats.Usage)))
		cd.P95[m][i] = ptr(float64(p.Stats.P95Ns) / 1e6)
	}
	return cd
}

func nullGrid(rows, cols int) [][]*float64 {
	g := make([][]*float64, rows)
	for i := range g {
		g[i] = make([]*float64, cols)
	}
	return g
}

func ptr(v float64) *float64 { return &v }

type modelsData struct {
	base
	Range  rangeView
	Groups []queries.GroupStats
}

func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	rv, err := resolveRange(r.URL.Query(), h.clk.Now())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	groups, err := h.q.ModelStats(r.Context(), rv.From, rv.To)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPage(w, r, "models.html", modelsData{base: h.base(r, "Models", "models"), Range: rv, Groups: groups})
}
