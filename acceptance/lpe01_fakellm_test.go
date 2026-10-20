//go:build acceptance

// SPDX-License-Identifier: Apache-2.0

package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/thnhdatle/loupe/acceptance/internal/native"
)

// lpe01FakeLLM is a fakellm container started with plain docker run (not
// compose, whose fixed host ports collide with other containers).
type lpe01FakeLLM struct {
	Image string
	Base  string // http://127.0.0.1:<port>
}

// lpe01Candidates lists the sibling images in order: the override, the
// released image, then the local development tag.
func lpe01Candidates() []string {
	var out []string
	if v := os.Getenv("RAYLD_FAKELLM_IMAGE"); v != "" {
		out = append(out, v)
	}
	return append(out, "ghcr.io/thnhdatle/fakellm:v0.1.0", "thnhdatle/fakellm:dev")
}

func lpe01PickImage(ctx context.Context) (string, error) {
	var tried []string
	for _, img := range lpe01Candidates() {
		cmd := exec.CommandContext(ctx, "docker", "image", "inspect", img)
		if err := cmd.Run(); err == nil {
			return img, nil
		}
		tried = append(tried, img)
	}
	return "", fmt.Errorf("no fakellm image found (tried %s)", strings.Join(tried, ", "))
}

// lpe01StartFakeLLM runs fakellm on a free loopback port and removes it at
// cleanup.
func lpe01StartFakeLLM(t *testing.T) *lpe01FakeLLM {
	t.Helper()
	ctx := context.Background()
	img, err := lpe01PickImage(ctx)
	if err != nil {
		t.Fatalf("fakellm: %v", err)
	}
	port := native.FreePorts(t, 1)[0]
	name := "lpe01-fakellm-" + strconv.Itoa(os.Getpid())
	out, err := exec.CommandContext(ctx, "docker", "run", "-d", "--rm", "--name", name,
		"-p", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))+":8090", img).CombinedOutput()
	if err != nil {
		t.Fatalf("fakellm: docker run %s: %v\n%s", img, err, out)
	}
	t.Cleanup(func() {
		_ = exec.CommandContext(context.Background(), "docker", "rm", "-f", name).Run()
	})
	f := &lpe01FakeLLM{Image: img, Base: "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port))}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := f.Requests(ctx); err == nil {
			return f
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("fakellm: %s not ready in 30s", img)
	return nil
}

// OpenAIURL is the base URL given to the OpenAI SDKs.
func (f *lpe01FakeLLM) OpenAIURL() string { return f.Base + "/v1" }

// lpe01Request is one chat request fakellm received.
type lpe01Request struct {
	// Input is the whitespace token count of every message text, which is
	// the usage fakellm returns for the request.
	Input int
	// ToolArgs are the arguments of assistant tool calls in the messages.
	ToolArgs []string
}

// Requests returns the chat requests received so far, oldest first.
func (f *lpe01FakeLLM) Requests(ctx context.Context) ([]lpe01Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.Base+"/_fake/requests", nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /_fake/requests: %s", resp.Status)
	}
	var log struct {
		Requests []struct {
			Path string `json:"path"`
			Body string `json:"body"`
		} `json:"requests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&log); err != nil {
		return nil, err
	}
	var out []lpe01Request
	for _, r := range log.Requests {
		if !strings.HasSuffix(r.Path, "/chat/completions") {
			continue
		}
		var body struct {
			Messages []struct {
				Content   json.RawMessage `json:"content"`
				ToolCalls []struct {
					Function struct {
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
			return nil, fmt.Errorf("decode request body: %w", err)
		}
		var q lpe01Request
		for _, m := range body.Messages {
			q.Input += len(strings.Fields(lpe01Text(m.Content)))
			for _, tc := range m.ToolCalls {
				q.ToolArgs = append(q.ToolArgs, tc.Function.Arguments)
			}
		}
		out = append(out, q)
	}
	return out, nil
}

// lpe01Text returns the text of a message content: a string or an array of
// text parts.
func lpe01Text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
		b.WriteByte(' ')
	}
	return b.String()
}
