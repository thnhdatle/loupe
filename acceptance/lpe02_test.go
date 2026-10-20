//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/bench/load"
)

// lpe02Seed is the payload seed; run ids keep span ids unique across runs.
const lpe02Seed = 2

// lpe02Once makes the scenario run once per process whatever b.N is: one
// LPE-02 execution is 3 gated 10-minute runs plus the reported 5,000 spans/s run.
var lpe02Once sync.Once

// BenchmarkAcceptance_LPE02_Ingest is the acceptance benchmark of criterion LPE-02.
//
// Each run starts the native binary on a fresh database (deleted afterwards),
// sends the benchmark payload (bench/gen) over OTLP/HTTP protobuf at a constant open-model
// arrival rate of min_spans_per_s for `minutes`, waits for the queue to drain
// and stops the server gracefully. Gates per run: 0 rejected, 0 dropped, lost
// = sent − stored = 0 (stored counted in the database file and cross-checked
// with /v1/ingest/stats), and also 0 transport errors, 0 other errors and 0
// requests skipped because the generator's in-flight cap was reached, so that
// the arrival rate really was min_spans_per_s. The 5,000 spans/s run is
// recorded under reported_* and never gates.
func BenchmarkAcceptance_LPE02_Ingest(b *testing.B) {
	lpe02Once.Do(func() { lpe02Scenario(b) })
}

func lpe02Scenario(b *testing.B) {
	b.Helper()
	run := harness.Start(b, "LPE-02")
	rate := perfNum(run, "min_spans_per_s")
	minutes := perfNum(run, "minutes")
	runs := int(perfNum(run, "runs"))
	lim := lpe02Limits{
		rejected: int64(perfNum(run, "rejected")),
		dropped:  int64(perfNum(run, "dropped")),
		lost:     int64(perfNum(run, "lost")),
	}
	dur := time.Duration(minutes * float64(time.Minute))
	measured := map[string]any{"runs": runs, "minutes": minutes, "target_spans_per_s": rate}

	var (
		fails   []string
		minRate = -1.0
		results []any
	)
	for i := 1; i <= runs; i++ {
		name := fmt.Sprintf("run%d", i)
		res, err := perfRun(b, run.ID, perfConfig{
			Name: name, Rate: rate, Duration: dur, Mode: load.ModeReject, RunID: uint32(i), Seed: lpe02Seed,
		})
		sum := perfSummary(res)
		lost := res.Load.SentSpans - res.DBSpans
		sum["lost"] = lost
		measured[name] = sum
		results = append(results, map[string]any{"name": name, "summary": sum})
		if err != nil {
			fails = append(fails, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		fails = append(fails, lim.check(name, res, rate)...)
		if minRate < 0 || res.sentRate() < minRate {
			minRate = res.sentRate()
		}
	}
	measured["min_sent_spans_per_s"] = minRate

	// Reported, not gated: one run at reported_only_spans_per_s.
	if v, ok := run.Thresholds["reported_only_spans_per_s"]; ok && v != nil {
		r5 := perfNum(run, "reported_only_spans_per_s")
		name := fmt.Sprintf("reported_%.0f", r5)
		res, err := perfRun(b, run.ID, perfConfig{
			Name: name, Rate: r5, Duration: dur, Mode: load.ModeReject, RunID: uint32(runs + 1), Seed: lpe02Seed,
		})
		sum := perfSummary(res)
		sum["lost"] = res.Load.SentSpans - res.DBSpans
		if err != nil {
			sum["error"] = err.Error()
		}
		sum["would_pass"] = err == nil && len(lim.check(name, res, r5)) == 0
		measured[name] = sum
		results = append(results, map[string]any{"name": name, "summary": sum, "reported_only": true})
	}
	if err := perfAppendResults(run.ID, results...); err != nil {
		b.Logf("append %s: %v", perfResultsFile, err)
	}
	b.ReportMetric(minRate, "min_sent_spans/s")

	if len(fails) > 0 {
		run.Fail(measured, "%s", strings.Join(fails, "; "))
		return
	}
	run.Pass(measured, fmt.Sprintf("%d runs of %.0f min at %.0f spans/s: 0 rejected, 0 dropped, 0 lost",
		runs, minutes, rate))
}

// lpe02Limits are the per-run thresholds.
type lpe02Limits struct{ rejected, dropped, lost int64 }

// check returns the gate failures of one run at rate.
func (lim lpe02Limits) check(name string, r perfResult, rate float64) []string {
	maxRejected, maxDropped, maxLost := lim.rejected, lim.dropped, lim.lost
	var f []string
	add := func(format string, args ...any) { f = append(f, name+": "+fmt.Sprintf(format, args...)) }
	l := r.Load
	if l.RejectedSpans > maxRejected {
		add("rejected %d > %d", l.RejectedSpans, maxRejected)
	}
	if l.DroppedSpans > maxDropped {
		add("dropped %d > %d", l.DroppedSpans, maxDropped)
	}
	if lost := l.SentSpans - r.DBSpans; lost > maxLost || lost < 0 {
		add("lost = sent %d − stored %d = %d (max %d)", l.SentSpans, r.DBSpans, lost, maxLost)
	}
	if r.Stats.Stored != r.DBSpans {
		add("/v1/ingest/stats stored %d != spans in DB %d", r.Stats.Stored, r.DBSpans)
	}
	if n := r.Stats.rejected() + r.Stats.dropped(); n != 0 {
		add("/v1/ingest/stats reports %d rejected/dropped", n)
	}
	if l.TransportErrors > 0 || l.OtherErrorSpans > 0 || l.Unfinished > 0 {
		add("transport errors %d, other errors %v, unfinished %d", l.TransportErrors, l.OtherErrors, l.Unfinished)
	}
	if l.SkippedBacklog > 0 {
		add("%d requests skipped by the generator's in-flight cap", l.SkippedBacklog)
	}
	if got := r.sentRate(); got < rate {
		add("sent %.1f spans/s < %.0f", got, rate)
	}
	if r.StopError != "" {
		add("server stop: %s", r.StopError)
	}
	return f
}
