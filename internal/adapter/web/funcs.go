// SPDX-License-Identifier: Apache-2.0

package web

import (
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

var sharedTemplates = map[string]bool{"layout.html": true, "partials.html": true}

func parsePages(fsys fs.FS) (map[string]*template.Template, error) {
	files, err := fs.Glob(fsys, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("web: list templates: %w", err)
	}
	pages := make(map[string]*template.Template)
	for _, f := range files {
		name := path.Base(f)
		if sharedTemplates[name] {
			continue
		}
		t, err := template.New(name).Funcs(funcMap()).ParseFS(fsys, "templates/layout.html", "templates/partials.html", f)
		if err != nil {
			return nil, fmt.Errorf("web: parse %s: %w", name, err)
		}
		pages[name] = t
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: no page templates", ErrInvalidOptions)
	}
	return pages, nil
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"money":    formatMoney,
		"moneyPtr": formatMoneyPtr,
		"dur":      formatDuration,
		"num":      formatInt,
		"pct":      formatPercent,
		"ts":       formatTime,
		"tokens":   totalTokens,
		"badge":    kindBadge,
		"join":     func(s []string) string { return strings.Join(s, ", ") },
		"list":     func(s ...string) []string { return s },
		"int64":    func(n int) int64 { return int64(n) },
		"bar":      func(x, w int, class string) barData { return barData{X: x, W: w, Class: class} },
	}
}

func formatMoney(nanos int64) string {
	usd := float64(nanos) / 1e9
	switch {
	case nanos == 0:
		return "$0.00"
	case usd >= 1:
		return fmt.Sprintf("$%.2f", usd)
	case usd >= 0.01:
		return fmt.Sprintf("$%.4f", usd)
	default:
		return fmt.Sprintf("$%.6f", usd)
	}
}

func formatMoneyPtr(nanos *int64) string {
	if nanos == nil {
		return "unpriced"
	}
	return formatMoney(*nanos)
}

func formatDuration(ns int64) string {
	switch {
	case ns < 1_000:
		return strconv.FormatInt(ns, 10) + " ns"
	case ns < 1_000_000:
		return fmt.Sprintf("%.0f µs", float64(ns)/1e3)
	case ns < 10_000_000:
		return fmt.Sprintf("%.1f ms", float64(ns)/1e6)
	case ns < 1_000_000_000:
		return fmt.Sprintf("%.0f ms", float64(ns)/1e6)
	case ns < 60_000_000_000:
		return fmt.Sprintf("%.2f s", float64(ns)/1e9)
	default:
		d := time.Duration(ns).Round(time.Second)
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

func formatInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func formatPercent(part, total int64) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.1f%%", float64(part)*100/float64(total))
}

func formatTime(ns int64) string {
	return time.Unix(0, ns).UTC().Format("2006-01-02 15:04:05")
}

func totalTokens(u trace.Usage) int64 { return u.Input + u.Output }

var kindBadges = map[trace.Kind]string{
	trace.KindLLM: "LLM", trace.KindEmbedding: "EMB", trace.KindTool: "TOOL", trace.KindRetrieval: "RET",
	trace.KindRerank: "RNK", trace.KindAgent: "AGT", trace.KindChain: "CHN", trace.KindGuardrail: "GRD",
	trace.KindOther: "OTH",
}

func kindBadge(k trace.Kind) string {
	if b, ok := kindBadges[k]; ok {
		return b
	}
	return "OTH"
}

type barData struct {
	X, W  int
	Class string
}
