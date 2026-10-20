//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/acceptance/internal/native"
)

const (
	lpe01Model   = "gpt-4o-mini"
	lpe01Reply   = "prairie bravo lumen prairie prairie cedar"
	lpe01Prompt  = "say hello to loupe"
	lpe01Answer  = "lumen juniper ember bravo meadow flint"
	lpe01Weather = "what is the weather in Paris"
	lpe01Result  = "sunny, 21C"
)

// lpe01ExportErr matches the export failure lines of the Python, Node and Go
// OpenTelemetry SDKs.
var lpe01ExportErr = regexp.MustCompile(`(?i)(failed to export|exception while exporting|error exporting|` +
	`while exporting|OTLPExporterError|export(er)? (error|failed)|failed to upload|failed to send|` +
	`connection refused|status (code )?[45][0-9][0-9]|traceback \(most recent)`)

// lpe01Want is one expected span; spans are listed depth first.
type lpe01Want struct {
	Depth int
	Name  string
	Kind  string
	// LLM spans carry the model, usage and cost; Out is the output tokens
	// (the whitespace token count of fakellm's answer).
	LLM bool
	Out int64
	// Content substrings that the stored content of the span must contain.
	Content []string
}

type lpe01Scenario struct {
	Name    string
	Dir     string
	Env     map[string]string
	Service string // service name prefix
	Want    []lpe01Want
	// Stdout substrings proving the deterministic fakellm answers.
	Stdout []string
}

func lpe01Scenarios() []lpe01Scenario {
	simple := func(root, llm string, content ...string) []lpe01Want {
		return []lpe01Want{
			{Depth: 0, Name: root, Kind: "other"},
			{Depth: 1, Name: llm, Kind: "llm", LLM: true, Out: 6, Content: content},
		}
	}
	chat := []string{lpe01Prompt, lpe01Reply}
	return []lpe01Scenario{
		{
			Name: "python openai-v2, log-event content form", Dir: "python-openai",
			Env:     map[string]string{"OPENAI_V2_CONTENT_FORM": "event"},
			Service: "example-python-openai-", Stdout: []string{lpe01Reply},
			Want: simple("python-openai-example", "chat "+lpe01Model, chat...),
		},
		{
			Name: "python openai-v2, span-attribute content form", Dir: "python-openai",
			Env:     map[string]string{"OPENAI_V2_CONTENT_FORM": "span"},
			Service: "example-python-openai-", Stdout: []string{lpe01Reply},
			Want: simple("python-openai-example", "chat "+lpe01Model, chat...),
		},
		{
			Name: "node openai", Dir: "node-openai", Service: "example-node-openai-",
			Stdout: []string{lpe01Reply},
			Want:   simple("node-openai-example", "chat "+lpe01Model, chat...),
		},
		{
			Name: "python openinference", Dir: "python-openinference", Service: "example-python-openinference-",
			Stdout: []string{lpe01Reply},
			Want:   simple("python-openinference-example", "ChatCompletion", chat...),
		},
		{
			Name: "go manual spans", Dir: "go-manual", Service: "example-go-manual-",
			Stdout: []string{lpe01Reply},
			Want:   simple("go-manual-example", "chat "+lpe01Model, chat...),
		},
		{
			Name: "agent with tool call", Dir: "agent", Service: "example-agent-",
			Stdout: []string{lpe01Answer},
			Want: []lpe01Want{
				{Depth: 0, Name: "invoke_agent weather-agent", Kind: "agent"},
				{
					Depth: 1, Name: "chat " + lpe01Model, Kind: "llm", LLM: true, Out: 7,
					Content: []string{lpe01Weather, "lookup_weather"},
				},
				{Depth: 1, Name: "execute_tool lookup_weather", Kind: "tool", Content: []string{lpe01Result}},
				{Depth: 1, Name: "chat " + lpe01Model, Kind: "llm", LLM: true, Out: 6, Content: []string{lpe01Result, lpe01Answer}},
			},
		},
	}
}

// lpe01Checks counts checks and keeps the failures.
type lpe01Checks struct {
	Total, Passed int
	Failures      []string
}

func (c *lpe01Checks) check(ok bool, format string, args ...any) {
	c.Total++
	if ok {
		c.Passed++
		return
	}
	c.Failures = append(c.Failures, fmt.Sprintf(format, args...))
}

func (c *lpe01Checks) pct() float64 {
	if c.Total == 0 {
		return 0
	}
	return 100 * float64(c.Passed) / float64(c.Total)
}

// lpe01Exec runs an example's run.sh and returns its combined output.
func lpe01Exec(ctx context.Context, root string, sc lpe01Scenario, env []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "examples/"+sc.Dir+"/run.sh")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), env...)
	for k, v := range sc.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// lpe01Await polls until a trace of the scenario that is not in known holds
// the expected spans and content, and two reads 500 ms apart agree.
func lpe01Await(ctx context.Context, c lpe01Client, sc lpe01Scenario, known map[string]string) (lpe01Trace, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var last string
	var lastErr error
	for ctx.Err() == nil {
		ids, err := c.traceIDs(ctx, sc.Service)
		lastErr = err
		for id := range ids {
			if _, old := known[id]; old || err != nil {
				continue
			}
			tr, err := c.trace(ctx, id)
			if err == nil {
				err = lpe01Complete(sc, tr)
			}
			if err != nil {
				lastErr = fmt.Errorf("trace %s: %w", id, err)
				continue
			}
			raw, _ := json.Marshal(tr)
			if string(raw) == last {
				return tr, nil
			}
			last = string(raw)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return lpe01Trace{}, fmt.Errorf("no complete trace for %s: %w", sc.Service, lastErr)
}

// lpe01Complete reports whether tr holds all spans and one content part per
// span that expects content.
func lpe01Complete(sc lpe01Scenario, tr lpe01Trace) error {
	wantParts, parts := 0, 0
	for _, w := range sc.Want {
		if len(w.Content) > 0 {
			wantParts++
		}
	}
	for _, s := range tr.Spans {
		parts += len(s.Content)
	}
	if len(tr.Spans) < len(sc.Want) || parts < wantParts {
		return fmt.Errorf("incomplete: %d spans, %d content parts", len(tr.Spans), parts)
	}
	return nil
}

// lpe01Verifier accumulates the expectations of one run.
type lpe01Verifier struct {
	*lpe01Checks
	prefix string
	reqs   []lpe01Request
	price  lpe01Price
	// Expected trace totals.
	in, out, cost int64
}

// lpe01Verify checks one run against the expectations.
func lpe01Verify(c *lpe01Checks, sc lpe01Scenario, tr lpe01Trace, reqs []lpe01Request, price lpe01Price) {
	v := &lpe01Verifier{lpe01Checks: c, prefix: sc.Name + ": ", reqs: reqs, price: price}
	spans, depths := lpe01Order(tr.Spans)
	v.check(len(spans) == len(sc.Want), "%sspan count %d, want %d", v.prefix, len(spans), len(sc.Want))
	llm := 0
	for i, w := range sc.Want {
		if i >= len(spans) {
			break
		}
		s := spans[i]
		v.check(depths[i] == w.Depth, "%sspan %d depth %d, want %d (%s)", v.prefix, i, depths[i], w.Depth, w.Name)
		v.check(s.Name == w.Name, "%sspan %d name %q, want %q", v.prefix, i, s.Name, w.Name)
		v.check(s.Kind == w.Kind, "%sspan %q kind %q, want %q", v.prefix, w.Name, s.Kind, w.Kind)
		if w.LLM {
			v.llm(w, s, llm)
			llm++
		} else {
			v.check(!s.HasUsage && s.CostNanos == nil, "%sspan %q has usage or cost", v.prefix, w.Name)
		}
		v.content(w, s)
	}
	v.check(llm == len(reqs), "%sfakellm requests %d, llm spans %d", v.prefix, len(reqs), llm)
	sum := tr.Summary
	v.check(sum.Usage.Input == v.in && sum.Usage.Output == v.out,
		"%strace usage in=%d out=%d, want in=%d out=%d", v.prefix, sum.Usage.Input, sum.Usage.Output, v.in, v.out)
	v.check(sum.CostNanos == v.cost, "%strace cost_nanos %d, want %d", v.prefix, sum.CostNanos, v.cost)
	v.check(len(sum.Models) == 1 && sum.Models[0] == lpe01Model,
		"%strace models %v, want [%s]", v.prefix, sum.Models, lpe01Model)
	v.check(!sum.HasError, "%strace has_error", v.prefix)
}

// llm checks the model, usage and cost of the n-th llm span against the
// fakellm request log and pricing/default.yaml.
func (v *lpe01Verifier) llm(w lpe01Want, s lpe01Span, n int) {
	v.check(s.Model == lpe01Model, "%sspan %q model %q, want %q", v.prefix, w.Name, s.Model, lpe01Model)
	if n >= len(v.reqs) {
		v.check(false, "%sspan %q has no matching fakellm request", v.prefix, w.Name)
		return
	}
	in := int64(v.reqs[n].Input)
	cost, err := v.price.Nanos(in, w.Out)
	v.check(err == nil, "%scost: %v", v.prefix, err)
	v.in += in
	v.out += w.Out
	v.cost += cost
	v.check(s.HasUsage && s.Usage.Input == in && s.Usage.Output == w.Out,
		"%sspan %q usage in=%d out=%d, want in=%d out=%d", v.prefix, w.Name, s.Usage.Input, s.Usage.Output, in, w.Out)
	v.check(s.CostNanos != nil && *s.CostNanos == cost,
		"%sspan %q cost_nanos %v, want %d", v.prefix, w.Name, s.CostNanos, cost)
}

// content checks the stored content of a span.
func (v *lpe01Verifier) content(w lpe01Want, s lpe01Span) {
	var body strings.Builder
	for _, part := range s.Content {
		body.WriteString(part.Body)
		body.WriteByte('\n')
		v.check(!part.Truncated, "%sspan %q content part truncated", v.prefix, w.Name)
	}
	for _, sub := range w.Content {
		v.check(strings.Contains(body.String(), sub),
			"%sspan %q content lacks %q (parts %d, body %q)", v.prefix, w.Name, sub, len(s.Content), body.String())
	}
	v.check(len(w.Content) == 0 || len(s.Content) > 0, "%sspan %q content missing", v.prefix, w.Name)
	v.roles(w, s)
	if w.Kind == "tool" {
		v.toolArgs(s)
	}
}

// roles checks the message roles of the log-event content form: the input
// messages start with a user message and end with a user or tool message, the
// output ends with an assistant message.
func (v *lpe01Verifier) roles(w lpe01Want, s lpe01Span) {
	for _, part := range s.Content {
		if part.Source != "log" || part.Role != "" {
			continue
		}
		var ev struct {
			In  []struct{ Role string } `json:"gen_ai.input.messages"`
			Out []struct{ Role string } `json:"gen_ai.output.messages"`
		}
		err := json.Unmarshal([]byte(part.Body), &ev)
		ok := err == nil && len(ev.In) > 0 && len(ev.Out) > 0
		v.check(ok, "%sspan %q log event lacks input or output messages: %v", v.prefix, w.Name, err)
		if !ok {
			continue
		}
		last := ev.In[len(ev.In)-1].Role
		v.check(ev.In[0].Role == "user" && (last == "user" || last == "tool"),
			"%sspan %q input roles %v", v.prefix, w.Name, ev.In)
		v.check(ev.Out[len(ev.Out)-1].Role == "assistant", "%sspan %q output roles %v", v.prefix, w.Name, ev.Out)
	}
}

// toolArgs checks that the stored tool call arguments equal the arguments
// fakellm's tool-call answer carried back in the next request.
func (v *lpe01Verifier) toolArgs(s lpe01Span) {
	var want map[string]any
	if len(v.reqs) < 2 || len(v.reqs[1].ToolArgs) == 0 {
		v.check(false, "%sfakellm request log has no tool call arguments", v.prefix)
		return
	}
	err := json.Unmarshal([]byte(v.reqs[1].ToolArgs[0]), &want)
	v.check(err == nil && len(want) > 0, "%sunreadable tool call arguments %q", v.prefix, v.reqs[1].ToolArgs[0])
	found := false
	for _, part := range s.Content {
		var got map[string]any
		if json.Unmarshal([]byte(part.Body), &got) == nil && reflect.DeepEqual(got, want) {
			found = true
		}
	}
	v.check(found, "%stool span content lacks arguments %v", v.prefix, want)
}

// lpe01Outcome is what the example runs measured.
type lpe01Outcome struct {
	checks       lpe01Checks
	exportErrors int
	runs         int
}

// lpe01RunAll runs every scenario and verifies its trace.
func lpe01RunAll(
	ctx context.Context, root string, fake *lpe01FakeLLM, loupe *native.Proc, price lpe01Price,
) (lpe01Outcome, error) {
	api := lpe01Client{base: loupe.UIURL()}
	env := []string{
		"LOUPE_OTLP_ENDPOINT=" + loupe.HTTPURL(),
		"LOUPE_API=" + loupe.UIURL(),
		"FAKELLM_URL=" + fake.OpenAIURL(),
		"LOUPE_CAPTURE=on",
		"EXAMPLE_NO_SUMMARY=1",
	}
	var res lpe01Outcome
	for _, sc := range lpe01Scenarios() {
		known, err := api.traceIDs(ctx, sc.Service)
		if err != nil {
			return res, fmt.Errorf("list traces: %w", err)
		}
		before, err := fake.Requests(ctx)
		if err != nil {
			return res, fmt.Errorf("fakellm requests: %w", err)
		}
		out, runErr := lpe01Exec(ctx, root, sc, env)
		res.runs++
		res.checks.check(runErr == nil, "%s: run.sh failed: %v\n%s", sc.Name, runErr, out)
		for _, line := range strings.Split(out, "\n") {
			if lpe01ExportErr.MatchString(line) {
				res.exportErrors++
				res.checks.check(false, "%s: SDK export error line: %s", sc.Name, line)
			}
		}
		for _, sub := range sc.Stdout {
			res.checks.check(strings.Contains(out, sub), "%s: stdout lacks %q: %s", sc.Name, sub, out)
		}
		after, err := fake.Requests(ctx)
		if err != nil {
			return res, fmt.Errorf("fakellm requests: %w", err)
		}
		tr, err := lpe01Await(ctx, api, sc, known)
		if err != nil {
			res.checks.check(false, "%s: %v", sc.Name, err)
			continue
		}
		lpe01Verify(&res.checks, sc, tr, after[len(before):], price)
	}
	return res, nil
}

// TestAcceptance_LPE01_SDKCompat is the acceptance test of criterion LPE-01.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE01_SDKCompat(t *testing.T) {
	run := harness.Start(t, "LPE-01")
	wd, err := os.Getwd()
	if err != nil {
		run.Fail(nil, "getwd: %v", err)
	}
	root, err := criteria.FindRoot(wd)
	if err != nil {
		run.Fail(nil, "%v", err)
	}
	price, err := lpe01LoadPrice(root, lpe01Model)
	if err != nil {
		run.Fail(nil, "%v", err)
	}
	fake := lpe01StartFakeLLM(t)
	loupe := native.Start(t, native.Opts{Env: map[string]string{"LOUPE_CONTENT_CAPTURE": "on"}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	res, err := lpe01RunAll(ctx, root, fake, loupe, price)
	if err != nil {
		run.Fail(nil, "%v", err)
	}
	checks := &res.checks
	st, err := lpe01Client{base: loupe.UIURL()}.stats(ctx)
	if err != nil {
		run.Fail(nil, "ingest stats: %v", err)
	}
	rejected, dropped := lpe01Sum(st.Rejected), lpe01Sum(st.Dropped)
	checks.check(rejected == 0, "ingest rejected %v, want none", st.Rejected)
	checks.check(dropped == 0, "ingest dropped %v, want none", st.Dropped)
	checks.check(st.Discarded["metrics"] > 0, "metrics requests not accepted: discarded %v", st.Discarded)
	measured := map[string]any{
		"checks_total":      checks.Total,
		"checks_passed":     checks.Passed,
		"checks_passed_pct": checks.pct(),
		"sdk_export_errors": res.exportErrors,
		"runs":              res.runs,
		"fakellm_image":     fake.Image,
		"discarded_metrics": st.Discarded["metrics"],
		"spans_stored":      st.Stored,
		"ingest_rejected":   rejected,
	}
	summary := fmt.Sprintf("%d/%d checks (%.1f%%), %d SDK export errors, %d runs, fakellm %s",
		checks.Passed, checks.Total, checks.pct(), res.exportErrors, res.runs, fake.Image)
	if len(checks.Failures) > 0 {
		summary += "\n" + strings.Join(checks.Failures, "\n")
	}
	minPct := lpe01Num(run.Thresholds["checks_passed_pct"])
	maxErrs := lpe01Num(run.Thresholds["sdk_export_errors"])
	if checks.pct() < minPct || float64(res.exportErrors) > maxErrs {
		run.Fail(measured, "%s", summary)
	}
	run.Pass(measured, summary)
}

func lpe01Sum(m map[string]int64) int64 {
	var n int64
	for _, v := range m {
		n += v
	}
	return n
}

func lpe01Num(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	}
	return 0
}
