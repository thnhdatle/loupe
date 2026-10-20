//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/internal/domain/cost"
	"github.com/thnhdatle/loupe/internal/domain/query"
	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// TestAcceptance_LPE05_Cost is the acceptance test of criterion LPE-05: every
// case of the hand-checked fixture goes through the production
// path — the fixture's price sections are loaded by the pricing file parser,
// each case is sent as gen_ai-attributed raw spans through app/ingest (semconv
// translation, model choice, cost.Calculator) and app/writer into a SQLite
// store on a temporary database (usage dedupe and cost transfer at settle),
// and is read back through app/queries. A span case matches when the stored
// cost_nanos, the usage_clamped flag and the price version the calculator
// selects for the stored model equal the expected ones; a trace case matches
// when the trace's cost (null when no counted span is priced), unpriced span
// count, input and output tokens and calls (counted llm/embedding spans) do.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE05_Cost(t *testing.T) {
	run := harness.Start(t, "LPE-05")
	wantPct := lpe09Threshold(t, run, "exact_match_pct")
	casesMin := int(lpe09Threshold(t, run, "cases_min"))

	wd, err := os.Getwd()
	if err != nil {
		run.Fail(nil, "getwd: %v", err)
	}
	root, err := criteria.FindRoot(wd)
	if err != nil {
		run.Fail(nil, "%v", err)
	}
	f, err := loadLPE05Fixture(root)
	if err != nil {
		run.Fail(nil, "%v", err)
	}
	pricing, err := loadLPE05Pricing(f)
	if err != nil {
		run.Fail(nil, "%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := startLPE05Pipeline(ctx, t.TempDir(), pricing.calc)
	if err != nil {
		run.Fail(nil, "%v", err)
	}
	defer func() {
		if serr := p.stop(); serr != nil {
			t.Errorf("stop pipeline: %v", serr)
		}
	}()

	measured, mismatches, err := lpe05Run(ctx, p, pricing, f.Cases)
	if err != nil {
		run.Fail(measured, "%v", err)
	}
	total, _ := measured["cases_total"].(int)
	pct, _ := measured["exact_match_pct"].(float64)
	summary := fmt.Sprintf("%v/%d cases exact in nano-USD (%v span, %v trace; need >= %d cases, >= %v%%)",
		measured["matched"], total, measured["span_cases"], measured["trace_cases"], casesMin, wantPct)
	if len(mismatches) > 0 {
		summary += "; mismatches: " + strings.Join(mismatches, "; ")
	}
	if total < casesMin || pct < wantPct {
		run.Fail(measured, "%s", summary)
	}
	run.Pass(measured, summary)
}

// lpe05Run ingests and checks every case and returns the measured values and
// one line per mismatching case. An error means the pipeline itself failed.
func lpe05Run(
	ctx context.Context, p *lpe05Pipeline, pricing *lpe05Pricing, cases []lpe05Case,
) (map[string]any, []string, error) {
	kinds := map[string]int{}
	var mismatches []string
	matched := 0
	for i := range cases {
		c := &cases[i]
		kinds[c.Kind]++
		ok, got, err := lpe05CheckCase(ctx, p, pricing, c, lpe05TraceID(i+1))
		if err != nil {
			return map[string]any{"cases_total": len(cases)}, nil, fmt.Errorf("case %s: %w", c.ID, err)
		}
		if ok {
			matched++
			continue
		}
		mismatches = append(mismatches, fmt.Sprintf("%s: expected %s, got %s", c.ID, lpe05Want(c), got))
	}
	pct := 0.0
	if len(cases) > 0 {
		pct = roundPct(100 * float64(matched) / float64(len(cases)))
	}
	if mismatches == nil {
		mismatches = []string{}
	}
	return map[string]any{
		"cases_total":     len(cases),
		"span_cases":      kinds["span"],
		"trace_cases":     kinds["trace"],
		"matched":         matched,
		"mismatched":      len(cases) - matched,
		"exact_match_pct": pct,
		"mismatches":      mismatches,
	}, mismatches, nil
}

// lpe05CheckCase ingests one case and compares the read-back values; got describes
// them in the form of lpe05Want.
func lpe05CheckCase(
	ctx context.Context, p *lpe05Pipeline, pricing *lpe05Pricing, c *lpe05Case, id trace.ID,
) (ok bool, got string, err error) {
	var raws []trace.RawSpan
	switch c.Kind {
	case "span":
		raws, err = p.spanCaseRaws(c, id)
	case "trace":
		raws, err = p.traceCaseRaws(c, id)
	default:
		err = fmt.Errorf("unknown kind %q", c.Kind)
	}
	if err != nil {
		return false, "", err
	}
	if err = p.ingest(ctx, raws); err != nil {
		return false, "", err
	}
	d, err := p.trace(ctx, id)
	if err != nil {
		return false, "", err
	}
	if len(d.Spans) != len(raws) {
		return false, "", fmt.Errorf("stored %d of %d spans", len(d.Spans), len(raws))
	}
	if c.Kind == "span" {
		return lpe05CheckSpan(pricing, c, &d.Spans[0])
	}
	if err = lpe05CheckKinds(c, d.Spans); err != nil {
		return false, "", err
	}
	return lpe05CheckTrace(c, &d)
}

// lpe05CheckSpan compares a stored span; the price version is the one the
// production calculator selects for the stored (translated) provider and model.
func lpe05CheckSpan(
	pricing *lpe05Pricing, c *lpe05Case, v *query.SpanView,
) (ok bool, got string, err error) {
	res, cerr := pricing.calc.Cost(cost.Input{
		Provider: v.Provider, ResponseModel: v.Model, Start: time.Unix(0, v.StartNs).UTC(), Usage: v.Usage,
	})
	price := ""
	if cerr == nil {
		price = pricing.id(res)
	}
	want := c.Expected
	ok = lpe05EqualNanos(want.CostNanos, v.CostNanos) && v.UsageClamped == want.UsageClamped &&
		price == lpe05Deref(want.Price)
	return ok, fmt.Sprintf("cost_nanos=%s price=%s usage_clamped=%t",
		lpe05Nanos(v.CostNanos), lpe05Or(price, "null"), v.UsageClamped), nil
}

// lpe05CheckTrace compares a trace's totals. The trace row stores a cost sum,
// so "null" is read as: sum 0 and no counted span priced.
func lpe05CheckTrace(c *lpe05Case, d *query.TraceDetail) (ok bool, got string, err error) {
	calls, priced := 0, false
	for i := range d.Spans {
		s := &d.Spans[i]
		if s.UsageDuplicate || !s.Kind.HasUsageDedupe() {
			continue
		}
		calls++
		priced = priced || s.CostNanos != nil
	}
	sum := d.Summary
	var costNanos *int64
	if sum.CostNanos != 0 || priced {
		n := sum.CostNanos
		costNanos = &n
	}
	want := c.Expected
	ok = lpe05EqualNanos(want.CostNanos, costNanos) && sum.UnpricedSpans == want.UnpricedSpans &&
		sum.Usage.Input == want.InputTokens && sum.Usage.Output == want.OutputTokens && calls == want.Calls
	return ok, fmt.Sprintf("cost_nanos=%s unpriced_spans=%d input_tokens=%d output_tokens=%d calls=%d",
		lpe05Nanos(costNanos), sum.UnpricedSpans, sum.Usage.Input, sum.Usage.Output, calls), nil
}

// lpe05CheckKinds verifies that semconv resolved every span to its fixture kind.
func lpe05CheckKinds(c *lpe05Case, views []query.SpanView) error {
	want := make(map[string]trace.Kind, len(c.Spans))
	for _, s := range c.Spans {
		want[s.ID] = s.Kind
	}
	for i := range views {
		if k := want[views[i].SpanID]; views[i].Kind != k {
			return fmt.Errorf("span %s: stored kind %q, fixture kind %q", views[i].SpanID, views[i].Kind, k)
		}
	}
	return nil
}

func lpe05Want(c *lpe05Case) string {
	w := c.Expected
	if c.Kind == "span" {
		return fmt.Sprintf("cost_nanos=%s price=%s usage_clamped=%t",
			lpe05Nanos(w.CostNanos), lpe05Or(lpe05Deref(w.Price), "null"), w.UsageClamped)
	}
	return fmt.Sprintf("cost_nanos=%s unpriced_spans=%d input_tokens=%d output_tokens=%d calls=%d",
		lpe05Nanos(w.CostNanos), w.UnpricedSpans, w.InputTokens, w.OutputTokens, w.Calls)
}

func lpe05EqualNanos(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func lpe05Nanos(p *int64) string {
	if p == nil {
		return "null"
	}
	return strconv.FormatInt(*p, 10)
}

func lpe05Deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func lpe05Or(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
