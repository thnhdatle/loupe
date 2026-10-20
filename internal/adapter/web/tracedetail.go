// SPDX-License-Identifier: Apache-2.0

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"

	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// ContentStrippedMessage is shown when a span's content arrived but was not stored.
const ContentStrippedMessage = "content not stored: set content.capture=on"

const (
	waterScale    = 1000 // bar coordinates are integers on a 0..1000 scale
	minBarWidth   = 3
	maxDepthClass = 12 // CSS has indent classes depth-0 .. depth-12
	maxAttrChars  = 4000
)

// waterRow is one span of the waterfall.
type waterRow struct {
	SpanID   string
	Name     string
	Kind     trace.Kind
	Status   trace.Status
	Service  string
	Model    string
	Duration int64
	Depth    int // capped at maxDepthClass
	X, W     int // bar position on the 0..1000 scale
	Selected bool
	Href     string
	Error    bool
}

type labelValue struct {
	Label string
	Value string
}

// partView is a content part prepared for display.
type partView struct {
	Heading   string
	Role      string
	Body      string
	HasBody   bool
	Bytes     int
	Truncated bool
}

// spanPanel is the side panel of the selected span.
type spanPanel struct {
	Span      query.SpanView
	Offset    int64
	Tokens    []labelValue
	HasTokens bool
	Unpriced  bool
	Attrs     []labelValue
	Parts     []partView
	Stripped  bool
	Message   string
}

// traceDetailData is the data of the trace detail page and of its fragment.
type traceDetailData struct {
	base
	Summary query.TraceSummary
	Rows    []waterRow
	Panel   spanPanel
}

func (h *Handler) traceDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	d, err := h.q.GetTrace(r.Context(), id, false)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if hasStoredContent(d) {
		if full, err := h.q.GetTrace(r.Context(), id, true); err == nil {
			d = full
		} else {
			h.log.Warn("trace content", "component", "web", "trace_id", id, "error", err.Error())
		}
	}
	order := orderSpans(d.Spans)
	if len(order) == 0 {
		h.fail(w, r, fmt.Errorf("trace %s has no spans: %w", id, trace.ErrNotFound))
		return
	}
	sel := order[0]
	for _, i := range order {
		if d.Spans[i].SpanID == r.URL.Query().Get("span") {
			sel = i
		}
	}
	data := traceDetailData{
		base: h.base(r, "Trace "+shortID(id), "traces"), Summary: d.Summary,
		Rows: waterfall(d, order, sel), Panel: newPanel(d, sel),
	}
	if isHTMX(r) {
		h.render(w, r, http.StatusOK, view{"trace.html", "trace_body"}, data)
		return
	}
	h.renderPage(w, r, "trace.html", data)
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// hasStoredContent reports whether any span has content bodies worth fetching.
func hasStoredContent(d query.TraceDetail) bool {
	for i := range d.Spans {
		if len(d.Spans[i].Content) > 0 && !d.Spans[i].ContentStripped {
			return true
		}
	}
	return false
}

// orderSpans returns span indexes in tree order (parents before children,
// siblings by start). Spans whose parent is missing are roots.
func orderSpans(spans []query.SpanView) []int {
	byID := make(map[string]int, len(spans))
	for i := range spans {
		byID[spans[i].SpanID] = i
	}
	children := make(map[int][]int)
	var roots []int
	for i := range spans {
		p, ok := byID[spans[i].ParentSpanID]
		if spans[i].ParentSpanID == "" || !ok || p == i {
			roots = append(roots, i)
			continue
		}
		children[p] = append(children[p], i)
	}
	byStart := func(s []int) {
		sort.SliceStable(s, func(a, b int) bool { return spans[s[a]].StartNs < spans[s[b]].StartNs })
	}
	byStart(roots)
	out := make([]int, 0, len(spans))
	seen := make([]bool, len(spans))
	var walk func(i int)
	walk = func(i int) {
		if seen[i] {
			return
		}
		seen[i] = true
		out = append(out, i)
		byStart(children[i])
		for _, c := range children[i] {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	for i := range spans { // a parent cycle leaves spans unreachable from any root
		walk(i)
	}
	return out
}

func depthOf(spans []query.SpanView, byID map[string]int, i int) int {
	depth := 0
	for seen := 0; seen <= len(spans); seen++ {
		p, ok := byID[spans[i].ParentSpanID]
		if !ok || spans[i].ParentSpanID == "" || p == i {
			break
		}
		i = p
		depth++
	}
	return depth
}

func waterfall(d query.TraceDetail, order []int, sel int) []waterRow {
	spans := d.Spans
	byID := make(map[string]int, len(spans))
	lo, hi := spans[order[0]].StartNs, spans[order[0]].EndNs
	for i := range spans {
		byID[spans[i].SpanID] = i
		lo, hi = min(lo, spans[i].StartNs), max(hi, spans[i].EndNs)
	}
	total := float64(max(hi-lo, 1))
	rows := make([]waterRow, 0, len(order))
	for _, i := range order {
		s := &spans[i]
		wd := max(int(float64(s.DurationNs)/total*waterScale), minBarWidth)
		x := min(int(float64(s.StartNs-lo)/total*waterScale), waterScale-wd)
		rows = append(rows, waterRow{
			SpanID: s.SpanID, Name: s.Name, Kind: s.Kind, Status: s.Status, Service: s.Service, Model: s.Model,
			Duration: s.DurationNs, Depth: min(depthOf(spans, byID, i), maxDepthClass), X: max(x, 0), W: wd,
			Selected: i == sel, Href: "/traces/" + d.Summary.TraceID + "?span=" + s.SpanID,
			Error: s.Status == trace.StatusError,
		})
	}
	return rows
}

func newPanel(d query.TraceDetail, sel int) spanPanel {
	s := d.Spans[sel]
	lo := s.StartNs
	for i := range d.Spans {
		lo = min(lo, d.Spans[i].StartNs)
	}
	p := spanPanel{Span: s, Offset: s.StartNs - lo, Attrs: attrRows(s.Attributes)}
	u := s.Usage
	p.HasTokens = s.HasUsage || !u.IsZero()
	p.Tokens = []labelValue{
		{"Input", formatInt(u.Input)},
		{"Output", formatInt(u.Output)},
		{"Cache read", formatInt(u.CacheRead)},
		{"Cache write", formatInt(u.CacheWrite)},
		{"Reasoning", formatInt(u.Reasoning)},
	}
	p.Unpriced = s.CostNanos == nil && totalTokens(u) > 0
	p.Stripped, p.Message = s.ContentStripped, ContentStrippedMessage
	for _, c := range s.Content {
		pv := partView{Heading: c.Role, Role: c.Role, Bytes: c.Bytes, Truncated: c.Truncated}
		if pv.Heading == "" {
			pv.Heading = "content"
		}
		if c.Body != nil {
			pv.Body, pv.HasBody = *c.Body, true
		}
		p.Parts = append(p.Parts, pv)
	}
	return p
}

// attrRows flattens the attributes JSON object into sorted rows.
func attrRows(raw json.RawMessage) []labelValue {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	rows := make([]labelValue, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, labelValue{k, attrText(m[k])})
	}
	return rows
}

func attrText(v any) string {
	s, ok := v.(string)
	if !ok {
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		s = string(b)
	}
	if r := []rune(s); len(r) > maxAttrChars {
		return string(r[:maxAttrChars]) + "…"
	}
	return s
}
