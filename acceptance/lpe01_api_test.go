//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// lpe01Part is one stored content part of a span.
type lpe01Part struct {
	Role      string `json:"role"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
	Source    string `json:"source"`
}

// lpe01Span is a span as returned by GET /v1/traces/{id}.
type lpe01Span struct {
	SpanID       string `json:"span_id"`
	ParentSpanID string `json:"parent_span_id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	StartNs      int64  `json:"start_ns"`
	Model        string `json:"model"`
	HasUsage     bool   `json:"has_usage"`
	Usage        struct {
		Input  int64 `json:"input"`
		Output int64 `json:"output"`
	} `json:"usage"`
	CostNanos       *int64      `json:"cost_nanos"`
	Content         []lpe01Part `json:"content"`
	ContentStripped bool        `json:"content_stripped"`
}

type lpe01Summary struct {
	TraceID   string   `json:"trace_id"`
	Service   string   `json:"service"`
	SpanCount int      `json:"span_count"`
	Models    []string `json:"models"`
	Usage     struct {
		Input  int64 `json:"input"`
		Output int64 `json:"output"`
	} `json:"usage"`
	CostNanos int64 `json:"cost_nanos"`
	HasError  bool  `json:"has_error"`
}

type lpe01Trace struct {
	Summary lpe01Summary `json:"summary"`
	Spans   []lpe01Span  `json:"spans"`
}

type lpe01Stats struct {
	Received  int64            `json:"received"`
	Stored    int64            `json:"stored"`
	Rejected  map[string]int64 `json:"rejected"`
	Dropped   map[string]int64 `json:"dropped"`
	Discarded map[string]int64 `json:"discarded"`
}

type lpe01Client struct{ base string }

func (c lpe01Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// traceIDs lists the trace ids of services starting with prefix.
func (c lpe01Client) traceIDs(ctx context.Context, prefix string) (map[string]string, error) {
	var page struct {
		Data []lpe01Summary `json:"data"`
	}
	if err := c.get(ctx, "/v1/traces", url.Values{"limit": {"200"}}, &page); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, s := range page.Data {
		if strings.HasPrefix(s.Service, prefix) {
			out[s.TraceID] = s.Service
		}
	}
	return out, nil
}

func (c lpe01Client) trace(ctx context.Context, id string) (lpe01Trace, error) {
	var tr lpe01Trace
	err := c.get(ctx, "/v1/traces/"+id, url.Values{"include_content": {"true"}}, &tr)
	return tr, err
}

func (c lpe01Client) stats(ctx context.Context) (lpe01Stats, error) {
	var st lpe01Stats
	err := c.get(ctx, "/v1/ingest/stats", nil, &st)
	return st, err
}

// lpe01Order returns the spans depth first with siblings ordered by start
// time then name, and each span's depth.
func lpe01Order(spans []lpe01Span) ([]lpe01Span, []int) {
	known := map[string]bool{}
	for _, s := range spans {
		known[s.SpanID] = true
	}
	kids := map[string][]lpe01Span{}
	for _, s := range spans {
		p := s.ParentSpanID
		if !known[p] {
			p = ""
		}
		kids[p] = append(kids[p], s)
	}
	for _, k := range kids {
		slices.SortFunc(k, func(a, b lpe01Span) int {
			if a.StartNs != b.StartNs {
				return int(a.StartNs - b.StartNs)
			}
			return strings.Compare(a.Name, b.Name)
		})
	}
	var out []lpe01Span
	var depths []int
	var rec func(parent string, depth int)
	rec = func(parent string, depth int) {
		for _, s := range kids[parent] {
			out = append(out, s)
			depths = append(depths, depth)
			rec(s.SpanID, depth+1)
		}
	}
	rec("", 0)
	return out, depths
}

// lpe01Price is a price in USD per million tokens as exact rationals.
type lpe01Price struct{ In, Out *big.Rat }

// lpe01LoadPrice reads the exact entry of model from pricing/default.yaml.
func lpe01LoadPrice(root, model string) (lpe01Price, error) {
	data, err := os.ReadFile(filepath.Join(root, "pricing", "default.yaml"))
	if err != nil {
		return lpe01Price{}, err
	}
	var f struct {
		Prices []struct {
			Match  string `yaml:"match"`
			Model  string `yaml:"model"`
			In     string `yaml:"input_per_mtok"`
			Out    string `yaml:"output_per_mtok"`
			Effect string `yaml:"effective_from"`
		} `yaml:"prices"`
	}
	if err := yaml.Unmarshal(data, &f); err != nil {
		return lpe01Price{}, err
	}
	var p lpe01Price
	n := 0
	for _, e := range f.Prices {
		if e.Match != "exact" || e.Model != model {
			continue
		}
		in, ok1 := new(big.Rat).SetString(e.In)
		out, ok2 := new(big.Rat).SetString(e.Out)
		if !ok1 || !ok2 {
			return lpe01Price{}, fmt.Errorf("price of %s is not decimal: %q %q", model, e.In, e.Out)
		}
		p = lpe01Price{In: in, Out: out}
		n++
	}
	if n != 1 {
		return lpe01Price{}, fmt.Errorf("pricing/default.yaml has %d exact entries for %s, want 1", n, model)
	}
	return p, nil
}

// Nanos is the cost in nano-USD: tokens x USD/Mtok x 1e9 / 1e6, exactly.
func (p lpe01Price) Nanos(in, out int64) (int64, error) {
	sum := new(big.Rat).Add(
		new(big.Rat).Mul(big.NewRat(in, 1), p.In),
		new(big.Rat).Mul(big.NewRat(out, 1), p.Out))
	sum.Mul(sum, big.NewRat(1000, 1))
	if !sum.IsInt() {
		return 0, fmt.Errorf("cost %s is not a whole number of nano-USD", sum.String())
	}
	return sum.Num().Int64(), nil
}
