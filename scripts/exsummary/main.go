// SPDX-License-Identifier: Apache-2.0

package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

const pollTimeout = 10 * time.Second

type usage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
}

type summary struct {
	TraceID   string   `json:"trace_id"`
	SpanCount int      `json:"span_count"`
	Models    []string `json:"models"`
	Usage     usage    `json:"usage"`
	CostNanos int64    `json:"cost_nanos"`
	HasError  bool     `json:"has_error"`
}

type content struct {
	Role string `json:"role"`
}

type span struct {
	SpanID       string    `json:"span_id"`
	ParentSpanID string    `json:"parent_span_id"`
	Name         string    `json:"name"`
	Kind         string    `json:"kind"`
	StartNs      int64     `json:"start_ns"`
	Model        string    `json:"model"`
	HasUsage     bool      `json:"has_usage"`
	Usage        usage     `json:"usage"`
	Duplicate    bool      `json:"usage_duplicate"`
	CostNanos    *int64    `json:"cost_nanos"`
	Content      []content `json:"content"`
}

type detail struct {
	Summary summary `json:"summary"`
	Spans   []span  `json:"spans"`
}

type client struct {
	base, token string
	http        *http.Client
}

func (c *client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

var errNotReady = errors.New("trace not complete yet")

type wanted struct {
	traces, minSpans int
	content          bool
}

func fetchAll(ctx context.Context, c *client, service string, w wanted) (int, []detail, error) {
	var page struct {
		Data []summary `json:"data"`
	}
	if err := c.get(ctx, "/v1/traces", url.Values{"service": {service}}, &page); err != nil {
		return 0, nil, err
	}
	if len(page.Data) < w.traces {
		return 0, nil, errNotReady
	}
	slices.Reverse(page.Data)
	if w.traces == 1 {
		page.Data = page.Data[len(page.Data)-1:]
	}
	var ds []detail
	for _, sm := range page.Data {
		var d detail
		err := c.get(ctx, "/v1/traces/"+sm.TraceID, url.Values{"include_content": {"true"}}, &d)
		if err != nil {
			return 0, nil, err
		}
		if len(d.Spans) < w.minSpans {
			return 0, nil, errNotReady
		}
		ds = append(ds, d)
	}
	if w.content && !slices.ContainsFunc(ds[0].Spans, func(s span) bool { return len(s.Content) > 0 }) {
		return 0, nil, errNotReady
	}
	return len(page.Data), ds, nil
}

type node struct {
	span  span
	depth int
}

func walk(spans []span) []node {
	ids := make(map[string]bool, len(spans))
	for _, s := range spans {
		ids[s.SpanID] = true
	}
	children := map[string][]span{}
	for _, s := range spans {
		p := s.ParentSpanID
		if !ids[p] {
			p = ""
		}
		children[p] = append(children[p], s)
	}
	for _, c := range children {
		slices.SortFunc(c, func(a, b span) int {
			return cmp.Or(cmp.Compare(a.StartNs, b.StartNs), strings.Compare(a.Name, b.Name))
		})
	}
	var out []node
	var rec func(parent string, depth int)
	rec = func(parent string, depth int) {
		for _, s := range children[parent] {
			out = append(out, node{s, depth})
			rec(s.SpanID, depth+1)
		}
	}
	rec("", 0)
	return out
}

func render(traces int, d detail, withContent bool) string {
	var b strings.Builder
	s := d.Summary
	fmt.Fprintf(&b, "traces: %d\n", traces)
	fmt.Fprintf(&b, "spans: %d\n", s.SpanCount)
	fmt.Fprintf(&b, "models: %s\n", strings.Join(s.Models, ","))
	fmt.Fprintf(&b, "tokens: input=%d output=%d\n", s.Usage.Input, s.Usage.Output)
	fmt.Fprintf(&b, "cost_nanos: %d\n", s.CostNanos)
	fmt.Fprintf(&b, "errors: %t\n", s.HasError)
	for _, n := range walk(d.Spans) {
		fmt.Fprintf(&b, "%s%s [%s]", strings.Repeat("  ", n.depth), n.span.Name, n.span.Kind)
		if n.span.Model != "" {
			fmt.Fprintf(&b, " model=%s", n.span.Model)
		}
		if n.span.HasUsage {
			fmt.Fprintf(&b, " in=%d out=%d", n.span.Usage.Input, n.span.Usage.Output)
		}
		if n.span.Duplicate {
			b.WriteString(" duplicate")
		}
		if n.span.CostNanos != nil {
			fmt.Fprintf(&b, " cost_nanos=%d", *n.span.CostNanos)
		}
		if withContent {
			fmt.Fprintf(&b, " content=%d", len(n.span.Content))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func renderAll(traces int, ds []detail, withContent bool) string {
	if len(ds) == 1 {
		return render(traces, ds[0], withContent)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "traces: %d\n", traces)
	for i, d := range ds {
		fmt.Fprintf(&b, "== trace %d\n", i+1)
		_, rest, _ := strings.Cut(render(traces, d, withContent), "\n")
		b.WriteString(rest)
	}
	return b.String()
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "exsummary:", err)
		fmt.Println("FAIL")
		os.Exit(1)
	}
}

func run() error {
	minSpans := flag.Int("min-spans", 1, "wait until the trace has at least this many spans")
	traceCount := flag.Int("traces", 1, "wait for and print this many traces of the service, oldest first")
	wantContent := flag.Bool("content", false, "when LOUPE_CAPTURE=on, wait for and count content parts")
	flag.Parse()
	if flag.NArg() != 1 {
		return errors.New("usage: exsummary [-min-spans N] [-traces N] [-content] SERVICE")
	}
	service := flag.Arg(0)
	withContent := *wantContent && os.Getenv("LOUPE_CAPTURE") == "on"
	c := &client{
		base:  strings.TrimRight(envOr("LOUPE_API", "http://127.0.0.1:3000"), "/"),
		token: os.Getenv("LOUPE_API_TOKEN"),
		http:  &http.Client{Timeout: 5 * time.Second},
	}
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), pollTimeout)
	defer cancel()
	var last string
	var lastErr error
	for {
		traces, ds, err := fetchAll(ctx, c, service, wanted{*traceCount, *minSpans, withContent})
		if err == nil {
			cur := renderAll(traces, ds, withContent)
			if cur == last {
				fmt.Print(cur)
				fmt.Fprintf(os.Stderr, "exsummary: trace visible after %.1f s\n", time.Since(start).Seconds())
				return nil
			}
			last = cur
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("no stable trace for service %q within %s: %w", service, pollTimeout, lastErr)
		case <-time.After(500 * time.Millisecond):
		}
	}
}
