//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/harness"
	"github.com/thnhdatle/loupe/acceptance/internal/native"
)

const (
	lpe06Runs      = 5
	lpe06ImageTag  = "thnhdatle/loupe:dev"
	lpe06PollEvery = 5 * time.Millisecond
)

// lpe06StartToReady starts a fresh process on an empty data dir and returns the
// time from just before exec to the first 200 of the admin /readyz.
func lpe06StartToReady(t *testing.T) time.Duration {
	t.Helper()
	p := native.Start(t, native.Opts{NoWait: true})
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if p.Exited() {
			t.Fatalf("process exited before ready (log %s)", p.LogPath)
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, p.AdminURL()+"/readyz", nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				d := time.Since(p.StartedAt)
				_ = p.Stop()
				return d
			}
		}
		time.Sleep(lpe06PollEvery)
	}
	t.Fatalf("readyz not 200 in 30s (log %s)", p.LogPath)
	return 0
}

// lpe06RSSMiB returns the resident set size of pid in MiB (ps reports KiB).
func lpe06RSSMiB(pid int) (float64, error) {
	cmd := exec.CommandContext(context.Background(), "ps", "-o", "rss=", "-p", strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ps: %w", err)
	}
	kib, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("parse rss %q: %w", out, err)
	}
	return kib / 1024, nil
}

// lpe06ImageMB builds the image with `make docker` and returns its uncompressed
// size in MB (1e6 bytes). It is safe to call off the test goroutine.
func lpe06ImageMB(root string) (float64, error) {
	mk := exec.CommandContext(context.Background(), "make", "docker", "IMAGE="+lpe06ImageTag)
	mk.Dir = root
	if out, err := mk.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("make docker: %w\n%s", err, out)
	}
	out, err := exec.CommandContext(context.Background(), "docker", "image", "inspect",
		"--format", "{{.Size}}", lpe06ImageTag).Output()
	if err != nil {
		return 0, fmt.Errorf("docker image inspect: %w", err)
	}
	size, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0, fmt.Errorf("parse image size %q: %w", out, err)
	}
	return size / 1e6, nil
}

// lpe06CGODisabled reports whether the binary was built with CGO_ENABLED=0.
func lpe06CGODisabled(t *testing.T, bin string) bool {
	t.Helper()
	out, err := exec.CommandContext(context.Background(), "go", "version", "-m", bin).Output()
	if err != nil {
		t.Fatalf("go version -m: %v", err)
	}
	return strings.Contains(string(out), "CGO_ENABLED=0")
}

// lpe06WarmExec runs `loupe version` once so that the OS first-launch scan of
// a freshly built binary (macOS) is not part of the start-to-ready
// measurement; it does not start a server.
func lpe06WarmExec(t *testing.T, bin string) {
	t.Helper()
	if out, err := exec.CommandContext(context.Background(), bin, "version").CombinedOutput(); err != nil {
		t.Fatalf("loupe version: %v\n%s", err, out)
	}
}

// TestAcceptance_LPE06_Footprint is the acceptance test of criterion LPE-06.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE06_Footprint(t *testing.T) {
	run := harness.Start(t, "LPE-06")
	readyMax := lpe09Threshold(t, run, "start_to_ready_s_max")
	rssMax := lpe09Threshold(t, run, "idle_rss_mib_max")
	idleMin := lpe09Threshold(t, run, "idle_minutes")
	imageMax := lpe09Threshold(t, run, "image_mb_max")

	bin := native.Binary(t)
	cgoOff := lpe06CGODisabled(t, bin)

	// Start-to-ready first, before any other load on the machine.
	lpe06WarmExec(t, bin)
	ds := make([]time.Duration, 0, lpe06Runs)
	for range lpe06Runs {
		ds = append(ds, lpe06StartToReady(t))
	}
	slices.Sort(ds)
	readyWorst, readyMedian := ds[len(ds)-1].Seconds(), ds[len(ds)/2].Seconds()

	// Idle RSS: fresh start, no traffic; the image build runs meanwhile.
	p := native.Start(t, native.Opts{})
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root, err := criteria.FindRoot(wd)
	if err != nil {
		t.Fatalf("find root: %v", err)
	}
	type imgRes struct {
		mb  float64
		err error
	}
	imgCh := make(chan imgRes, 1)
	go func() {
		mb, ierr := lpe06ImageMB(root)
		imgCh <- imgRes{mb, ierr}
	}()

	idle := time.Duration(idleMin * float64(time.Minute))
	var peak float64
	end := time.Now().Add(idle)
	for time.Now().Before(end) {
		if p.Exited() {
			t.Fatalf("process exited during idle wait (log %s)", p.LogPath)
		}
		if v, rerr := lpe06RSSMiB(p.Pid()); rerr == nil && v > peak {
			peak = v
		}
		time.Sleep(time.Second)
	}
	rss, err := lpe06RSSMiB(p.Pid())
	if err != nil {
		t.Fatalf("rss: %v", err)
	}
	peak = max(peak, rss)
	_ = p.Stop()

	img := <-imgCh
	if img.err != nil {
		t.Fatalf("image: %v", img.err)
	}
	imageMB := img.mb

	measured := map[string]any{
		"start_to_ready_s_max":    lpe06Round(readyWorst),
		"start_to_ready_s_median": lpe06Round(readyMedian),
		"idle_rss_mib":            lpe06Round(rss),
		"idle_rss_peak_mib":       lpe06Round(peak),
		"image_mb":                lpe06Round(imageMB),
		"cgo_disabled":            cgoOff,
	}
	var bad []string
	if !cgoOff {
		bad = append(bad, "binary not built with CGO_ENABLED=0")
	}
	if readyWorst >= readyMax {
		bad = append(bad, fmt.Sprintf("start-to-ready max %.3fs >= %.0fs", readyWorst, readyMax))
	}
	if rss >= rssMax {
		bad = append(bad, fmt.Sprintf("idle RSS %.1f MiB >= %.0f", rss, rssMax))
	}
	if imageMB >= imageMax {
		bad = append(bad, fmt.Sprintf("image %.1f MB >= %.0f", imageMB, imageMax))
	}
	if len(bad) > 0 {
		run.Fail(measured, "%s", strings.Join(bad, "; "))
	}
	run.Pass(measured, fmt.Sprintf("gated on max of %d start-to-ready runs (median recorded); "+
		"RSS via ps after %.0f min idle; image size uncompressed via docker image inspect", lpe06Runs, idleMin))
}

func lpe06Round(v float64) float64 { return math.Round(v*1e3) / 1e3 }
