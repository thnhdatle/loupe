//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/acceptance/internal/native"
)

const (
	lpe11Session  = "lpe11-session-a"
	lpe11Timeout  = 5 * time.Minute
	lpe11Traces   = 5
	lpe11NodeRoot = "tests/node"
)

var lpe11LoginRE = regexp.MustCompile(`dashboard login: (http\S+/login\?code=[A-Za-z0-9_-]+)`)

// lpe11Span is one fixture span; parent is the id of the parent span or 0.
type lpe11Span struct {
	name, op, model string
	id, parent      byte
	startMs, durMs  int
	content         bool
}

// lpe11Trace builds the nested agent trace:
//
//	planner (agent)
//	  chat gpt-4o (llm, carries gen_ai.input.messages)
//	  execute_tool search_web (tool)
//	  researcher (agent)
//	    chat claude-sonnet-4-5 (llm)
//	    verifier (agent)
//	      execute_tool fetch_page (tool)
//	  chat gpt-4o-mini (llm)
func lpe11Trace(tid []byte, session string, now time.Time) []*tracepb.Span {
	defs := []lpe11Span{
		{"invoke_agent planner", "invoke_agent", "", 1, 0, 0, 900, false},
		{"chat gpt-4o", "chat", "gpt-4o", 2, 1, 10, 150, true},
		{"execute_tool search_web", "execute_tool", "", 3, 1, 170, 100, false},
		{"invoke_agent researcher", "invoke_agent", "", 4, 1, 280, 450, false},
		{"chat claude-sonnet-4-5", "chat", "claude-sonnet-4-5", 5, 4, 290, 140, false},
		{"invoke_agent verifier", "invoke_agent", "", 6, 4, 440, 280, false},
		{"execute_tool fetch_page", "execute_tool", "", 7, 6, 450, 200, false},
		{"chat gpt-4o-mini", "chat", "gpt-4o-mini", 8, 1, 740, 150, false},
	}
	base := now.Add(-time.Minute)
	out := make([]*tracepb.Span, 0, len(defs))
	for _, d := range defs {
		sp := &tracepb.Span{
			TraceId: tid, SpanId: lpe11SpanID(tid, d.id), Name: d.name, Kind: tracepb.Span_SPAN_KIND_INTERNAL,
			StartTimeUnixNano: uint64(base.Add(time.Duration(d.startMs) * time.Millisecond).UnixNano()),
			EndTimeUnixNano:   uint64(base.Add(time.Duration(d.startMs+d.durMs) * time.Millisecond).UnixNano()),
			Attributes: []*commonpb.KeyValue{
				lpe10Str("gen_ai.operation.name", d.op),
				lpe10Str("session.id", session),
				lpe10Str("user.id", "lpe11-user"),
			},
		}
		if d.parent != 0 {
			sp.ParentSpanId = lpe11SpanID(tid, d.parent)
		}
		if d.model != "" {
			sp.Kind = tracepb.Span_SPAN_KIND_CLIENT
			sp.Attributes = append(sp.Attributes,
				lpe10Str("gen_ai.system", "openai"),
				lpe10Str("gen_ai.request.model", d.model),
				lpe10Int("gen_ai.usage.input_tokens", 120),
				lpe10Int("gen_ai.usage.output_tokens", 40))
		}
		if d.content {
			sp.Attributes = append(sp.Attributes, lpe10Str("gen_ai.input.messages",
				`[{"role":"user","parts":[{"type":"text","content":"LPE11 stripped"}]}]`))
		}
		out = append(out, sp)
	}
	return out
}

func lpe11SpanID(tid []byte, n byte) []byte {
	return []byte{0xaa, tid[15], 0, 0, 0, 0, 0, n}
}

func lpe11TraceID(n int) []byte {
	tid := make([]byte, 16)
	tid[0], tid[1], tid[15] = 0x1c, 0xe1, byte(n)
	return tid
}

// lpe11Simple is a one-span chat trace for the list pages.
func lpe11Simple(tid []byte, model string, ago time.Duration, now time.Time, session string) *tracepb.Span {
	start := now.Add(-ago)
	attrs := []*commonpb.KeyValue{
		lpe10Str("gen_ai.operation.name", "chat"),
		lpe10Str("gen_ai.system", "openai"),
		lpe10Str("gen_ai.request.model", model),
		lpe10Int("gen_ai.usage.input_tokens", 200),
		lpe10Int("gen_ai.usage.output_tokens", 50),
	}
	if session != "" {
		attrs = append(attrs, lpe10Str("session.id", session))
	}
	return &tracepb.Span{
		TraceId: tid, SpanId: lpe11SpanID(tid, 1), Name: "chat " + model, Kind: tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(start.UnixNano()), EndTimeUnixNano: uint64(start.Add(300 * time.Millisecond).UnixNano()),
		Attributes: attrs,
	}
}

func lpe11Send(ctx context.Context, p *native.Proc, mainTrace []byte) error {
	now := time.Now()
	spans := lpe11Trace(mainTrace, lpe11Session, now)
	spans = append(spans,
		lpe11Simple(lpe11TraceID(2), "gpt-4o", 5*time.Minute, now, lpe11Session),
		lpe11Simple(lpe11TraceID(3), "claude-sonnet-4-5", 8*time.Minute, now, "lpe11-session-b"),
		lpe11Simple(lpe11TraceID(4), "gpt-4o-mini", 12*time.Minute, now, "lpe11-session-b"),
		lpe11Simple(lpe11TraceID(5), "gpt-4o", 20*time.Minute, now, ""),
	)
	req := &coltrace.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   &resourcepb.Resource{Attributes: []*commonpb.KeyValue{lpe10Str("service.name", "lpe11")}},
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
	}}}
	return lpe10Post(ctx, p.HTTPURL()+"/v1/traces", req)
}

// lpe11WaitStored waits until the API lists every fixture trace.
func lpe11WaitStored(ctx context.Context, p *native.Proc, token string) error {
	var last string
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UIURL()+"/v1/traces?limit=50", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			var body struct {
				Data []struct {
					SpanCount int `json:"span_count"`
				} `json:"data"`
			}
			derr := json.NewDecoder(resp.Body).Decode(&body)
			_ = resp.Body.Close()
			last = fmt.Sprintf("status %d, %d traces, decode %v", resp.StatusCode, len(body.Data), derr)
			if resp.StatusCode == http.StatusOK && derr == nil && len(body.Data) >= lpe11Traces {
				for _, d := range body.Data {
					if d.SpanCount == 8 {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("traces not stored: %s: %w", last, ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// lpe11LoginURL waits for the logged login URL and points it at 127.0.0.1.
func lpe11LoginURL(p *native.Proc, port int) (string, error) {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(p.LogPath); err == nil {
			if m := lpe11LoginRE.FindSubmatch(data); m != nil {
				u, err := url.Parse(string(m[1]))
				if err != nil {
					return "", err
				}
				u.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
				return u.String(), nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return "", fmt.Errorf("no %q line in %s", "dashboard login: ...", p.LogPath)
}

// lpe11Report is the subset of Playwright's JSON report we read.
type lpe11Report struct {
	Suites []lpe11Suite `json:"suites"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type lpe11Suite struct {
	Title  string       `json:"title"`
	Suites []lpe11Suite `json:"suites"`
	Specs  []struct {
		Title string `json:"title"`
		OK    bool   `json:"ok"`
		Tests []struct {
			Status  string `json:"status"`
			Results []struct {
				Status string `json:"status"`
				Error  struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"results"`
		} `json:"tests"`
	} `json:"specs"`
}

// collect appends (title, ok, message) for every spec.
func (s lpe11Suite) collect(visit func(title string, ok bool, msg string)) {
	for _, sp := range s.Specs {
		msg := ""
		for _, t := range sp.Tests {
			for _, r := range t.Results {
				if r.Error.Message != "" {
					msg = r.Error.Message
				}
			}
		}
		visit(sp.Title, sp.OK, msg)
	}
	for _, c := range s.Suites {
		c.collect(visit)
	}
}

var lpe11ANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func lpe11OneLine(s string) string {
	s = lpe11ANSI.ReplaceAllString(s, "")
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.Join(lines[:min(len(lines), 4)], " | ")
}

// lpe11Playwright runs the spec and returns the report and console problems.
func lpe11Playwright(ctx context.Context, root, dir string, env []string) (lpe11Summary, error) {
	nodeDir := filepath.Join(root, lpe11NodeRoot)
	if _, err := os.Stat(filepath.Join(nodeDir, "node_modules", "@playwright", "test")); err != nil {
		return lpe11Summary{}, fmt.Errorf("playwright not installed in %s (run npm ci there): %w", nodeDir, err)
	}
	report := filepath.Join(dir, "report.json")
	consoleFile := filepath.Join(dir, "console.json")
	args := []string{"playwright", "test", "-c", "e2e/playwright.config.js"}
	cmd := exec.CommandContext(ctx, "npx", args...)
	cmd.Dir = nodeDir
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env,
		"LPE11_REPORT="+report,
		"LPE11_CONSOLE_ERRORS="+consoleFile,
		"LPE11_OUTPUT_DIR="+filepath.Join(dir, "pw-output"),
		"CI=",
	)
	out, runErr := cmd.CombinedOutput()
	data, err := os.ReadFile(report)
	if err != nil {
		return lpe11Summary{}, fmt.Errorf("no playwright report (run error %w): %w\n%s",
			runErr, err, lpe11OneLine(string(out)))
	}
	var rep lpe11Report
	if err := json.Unmarshal(data, &rep); err != nil {
		return lpe11Summary{}, fmt.Errorf("parse playwright report: %w", err)
	}
	var sum lpe11Summary
	for _, s := range rep.Suites {
		s.collect(func(title string, ok bool, msg string) {
			sum.total++
			if ok {
				sum.passed++
			} else {
				sum.failed = append(sum.failed, title+": "+lpe11OneLine(msg))
			}
		})
	}
	for _, e := range rep.Errors {
		sum.total++
		sum.failed = append(sum.failed, "playwright error: "+lpe11OneLine(e.Message))
	}
	if cd, err := os.ReadFile(consoleFile); err == nil {
		_ = json.Unmarshal(cd, &sum.problems)
	}
	return sum, nil
}

type lpe11Summary struct {
	total, passed int
	failed        []string
	problems      []string
}

//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE11_DashboardSmoke(t *testing.T) {
	run := harness.Start(t, "LPE-11")
	wantPct := lpe09Threshold(t, run, "checks_passed_pct")
	wantErrs := lpe09Threshold(t, run, "console_errors")
	measured := map[string]any{}

	if _, err := exec.LookPath("npx"); err != nil {
		run.Fail(measured, "npx not found: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		run.Fail(measured, "getwd: %v", err)
	}
	root, err := criteria.FindRoot(wd)
	if err != nil {
		run.Fail(measured, "find root: %v", err)
	}

	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "api.token")
	uiPort := native.FreePorts(t, 1)[0]
	p := native.Start(t, native.Opts{Dir: dir, Env: map[string]string{
		"LOUPE_UI_LISTEN":                net.JoinHostPort("0.0.0.0", strconv.Itoa(uiPort)),
		"LOUPE_UI_ALLOW_UNAUTHENTICATED": "false",
		"LOUPE_API_TOKEN_FILE":           tokenFile,
		"LOUPE_API_GENERATE_TOKEN":       "true",
		"LOUPE_CONTENT_CAPTURE":          "off",
	}})
	p.Ports.UI = uiPort
	tokenRaw, err := os.ReadFile(tokenFile)
	if err != nil {
		run.Fail(measured, "read token: %v", err)
	}
	token := strings.TrimSpace(string(tokenRaw))
	loginURL, err := lpe11LoginURL(p, uiPort)
	if err != nil {
		run.Fail(measured, "%v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lpe11Timeout)
	defer cancel()
	mainTrace := lpe11TraceID(1)
	if err = lpe11Send(ctx, p, mainTrace); err != nil {
		run.Fail(measured, "send: %v", err)
	}
	if err = lpe11WaitStored(ctx, p, token); err != nil {
		run.Fail(measured, "%v", err)
	}

	sum, err := lpe11Playwright(ctx, root, dir, []string{
		"LPE11_BASE_URL=" + p.UIURL(),
		"LPE11_LOGIN_URL=" + loginURL,
		"LPE11_TOKEN=" + token, // gitleaks:allow (variable, not a secret)
		"LPE11_TRACE_ID=" + hex.EncodeToString(mainTrace),
		"LPE11_SESSION_ID=" + lpe11Session,
	})
	if err != nil {
		run.Fail(measured, "%v", err)
	}
	pct := 0.0
	if sum.total > 0 {
		pct = roundPct(float64(sum.passed) / float64(sum.total) * 100)
	}
	measured["checks_total"] = sum.total
	measured["checks_passed"] = sum.passed
	measured["checks_passed_pct"] = pct
	measured["console_errors"] = len(sum.problems)
	measured["failed_checks"] = sum.failed
	measured["console_error_list"] = sum.problems

	details := fmt.Sprintf("%d/%d checks passed (%.2f%%), %d console errors; failed: %v; problems: %v",
		sum.passed, sum.total, pct, len(sum.problems), sum.failed, sum.problems)
	switch {
	case sum.total == 0:
		run.Fail(measured, "vacuous: no checks ran")
	case pct < wantPct || float64(len(sum.problems)) > wantErrs:
		run.Fail(measured, "%s", details)
	}
	run.Pass(measured, details)
}
