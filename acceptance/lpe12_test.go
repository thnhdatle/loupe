//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/criteria"
	"github.com/thnhdatle/loupe/acceptance/internal/harness"
)

const lpe12Timeout = 15 * time.Minute

var (
	lpe12LoginRe   = regexp.MustCompile(`dashboard login: (http://[^"\s]+/login\?code=[A-Za-z0-9_-]+)`)
	lpe12VisibleRe = regexp.MustCompile(`trace visible after ([0-9.]+) s`)
)

// lpe12Env returns the environment of the quickstart: host ports moved off the
// defaults (containers already running on the host may hold them) and the example pointed at them.
func lpe12Env() []string {
	return append(os.Environ(),
		"LOUPE_UI_HOST_PORT=13300", "LOUPE_GRPC_HOST_PORT=14417", "LOUPE_HTTP_HOST_PORT=14418",
		"LOUPE_ADMIN_HOST_PORT=19192", "FAKELLM_HOST_PORT=18190",
		"LOUPE_OTLP_ENDPOINT=http://127.0.0.1:14418", "LOUPE_API=http://127.0.0.1:13300",
		"FAKELLM_URL=http://127.0.0.1:18190/v1", "QUICKSTART_KEEP=1",
	)
}

// TestAcceptance_LPE12_Quickstart is the acceptance test of criterion LPE-12.
// It runs scripts/quickstart-test.sh (README commands on a fresh clone), then
// opens the logged login URL on the still running stack.
//
//nolint:paralleltest // acceptance scenarios own ports, processes and disk.
func TestAcceptance_LPE12_Quickstart(t *testing.T) {
	r := harness.Start(t, "LPE-12")
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root, err := criteria.FindRoot(wd)
	if err != nil {
		t.Fatalf("root: %v", err)
	}
	t.Cleanup(func() {
		args := []string{"compose", "-p", "loupe", "down", "-v", "--remove-orphans"}
		_ = exec.CommandContext(context.Background(), "docker", args...).Run()
	})

	ctx, cancel := context.WithTimeout(context.Background(), lpe12Timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "bash", "scripts/quickstart-test.sh")
	cmd.Dir = root
	cmd.Env = lpe12Env()
	cmd.Stdout = &out
	cmd.Stderr = &out
	start := time.Now()
	runErr := cmd.Run()
	minutes := time.Since(start).Minutes()
	text := out.String()
	_ = os.WriteFile(root+"/acceptance/logs/LPE-12.txt", out.Bytes(), 0o600)
	measured := map[string]any{"minutes": minutes}
	if runErr != nil {
		r.Fail(measured, "quickstart script failed: %v\n%s", runErr, tail(text, 3000))
	}

	visible := -1.0
	if m := lpe12VisibleRe.FindStringSubmatch(text); m != nil {
		visible, _ = strconv.ParseFloat(m[1], 64)
	}
	measured["trace_visible_s"] = visible
	m := lpe12LoginRe.FindStringSubmatch(text)
	if m == nil {
		r.Fail(measured, "no dashboard login URL in quickstart output:\n%s", tail(text, 3000))
	}
	// The container logs port 3000; the host port was moved.
	status, cookie, err := lpe12Login(ctx, strings.Replace(m[1], "localhost:3000", "127.0.0.1:13300", 1))
	measured["login_status"] = status
	if err != nil {
		r.Fail(measured, "login URL request: %v", err)
	}
	if status != http.StatusSeeOther && status != http.StatusFound || !cookie {
		r.Fail(measured, "login URL: status %d, session cookie %t", status, cookie)
	}
	maxVisible := lpe09Threshold(t, r, "trace_visible_s_max")
	maxMinutes := lpe09Threshold(t, r, "minutes_max")
	if visible < 0 || visible > maxVisible || minutes > maxMinutes {
		r.Fail(measured, "thresholds: minutes %.2f (max %v), trace visible %.1f s (max %v)",
			minutes, maxMinutes, visible, maxVisible)
	}
	r.Pass(measured, "README quickstart ran; login URL signed in; trace visible")
}

// lpe12Login opens the login URL without following the redirect.
func lpe12Login(ctx context.Context, url string) (status int, cookie bool, err error) {
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, false, err
	}
	_ = resp.Body.Close()
	for _, c := range resp.Cookies() {
		cookie = cookie || c.Name == "loupe_session"
	}
	return resp.StatusCode, cookie, nil
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
