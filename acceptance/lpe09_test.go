//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/internal/domain/rollup"
	"github.com/thnhdatle/loupe/internal/domain/rollup/rolluptest"
)

// lpe09Hours is the number of hourly rollup rows each distribution is spread over.
const lpe09Hours = 24

// TestAcceptance_LPE09_Percentiles is the acceptance test of criterion LPE-09:
// per distribution (fixed seeds) the samples are spread round-robin
// over 24 hourly histograms, each stored as a blob and decoded again, merged,
// and p50/p95/p99 compared with the nearest-rank exact quantile.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE09_Percentiles(t *testing.T) {
	run := harness.Start(t, "LPE-09")
	samples := int(lpe09Threshold(t, run, "samples_per_distribution"))
	wantDists := int(lpe09Threshold(t, run, "distributions"))
	maxErrPct := lpe09Threshold(t, run, "relative_error_pct_max")

	dists := rolluptest.Distributions()
	measured := map[string]any{"samples_per_distribution": samples, "distributions": len(dists)}
	worst, worstAt := 0.0, ""
	details := make([]string, 0, len(dists)*len(rolluptest.Quantiles))
	for i, d := range dists {
		errs, err := lpe09Distribution(d, i, samples)
		if err != nil {
			run.Fail(measured, "%s: %v", d.Name, err)
		}
		for j, q := range rolluptest.Quantiles {
			name := fmt.Sprintf("%s_p%d_error_pct", d.Name, int(math.Round(q*100)))
			measured[name] = roundPct(errs[j])
			details = append(details, fmt.Sprintf("%s=%.4f%%", name, errs[j]))
			if errs[j] > worst {
				worst, worstAt = errs[j], name
			}
		}
	}
	measured["relative_error_pct_max"] = roundPct(worst)
	summary := fmt.Sprintf("seed %d; worst %s=%.4f%% (limit %v%%); %s",
		rolluptest.Seed, worstAt, worst, maxErrPct, strings.Join(details, ", "))
	if len(dists) < wantDists || worst > maxErrPct {
		run.Fail(measured, "%s", summary)
	}
	run.Pass(measured, summary)
}

// lpe09Distribution returns the relative error in percent at each of rolluptest.Quantiles.
func lpe09Distribution(d rolluptest.Distribution, i, samples int) ([]float64, error) {
	values := rolluptest.Samples(d, i, samples)
	var hours [lpe09Hours]rollup.Histogram
	for j, v := range values {
		hours[j%lpe09Hours].Add(v)
	}
	var merged rollup.Histogram
	for h := range hours {
		blob, err := hours[h].MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("encode hour %d: %w", h, err)
		}
		var row rollup.Histogram
		if err := row.UnmarshalBinary(blob); err != nil {
			return nil, fmt.Errorf("decode hour %d: %w", h, err)
		}
		merged.Merge(&row)
	}
	if merged.Count() != uint64(samples) {
		return nil, fmt.Errorf("merged count %d, want %d", merged.Count(), samples)
	}
	sorted := rolluptest.Sorted(values)
	errs := make([]float64, 0, len(rolluptest.Quantiles))
	for _, q := range rolluptest.Quantiles {
		got, err := merged.Quantile(q)
		if err != nil {
			return nil, fmt.Errorf("quantile %v: %w", q, err)
		}
		errs = append(errs, 100*rolluptest.RelativeError(got, rolluptest.ExactQuantile(sorted, q)))
	}
	return errs, nil
}

func lpe09Threshold(t *testing.T, run *harness.Run, key string) float64 {
	t.Helper()
	switch v := run.Thresholds[key].(type) {
	case int:
		return float64(v)
	case float64:
		return v
	default:
		run.Fail(nil, "threshold %s: unexpected value %v (%T)", key, v, v)
		return 0
	}
}

func roundPct(v float64) float64 { return math.Round(v*1e4) / 1e4 }
