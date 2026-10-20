// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/thnhdatle/loupe/internal/config"
)

// Job states reported by the admin API.
const (
	jobDone   = "done"
	jobFailed = "failed"
)

const (
	defaultPollInterval = 500 * time.Millisecond
	maxAdminResponse    = 1 << 20
	loopbackHost        = "127.0.0.1"
)

var errAdminAPI = errors.New("admin api")

// adminFlags are the flags shared by the commands that call the admin API of
// a running server.
type adminFlags struct {
	server    string
	tokenFile string
	offline   bool
}

func (f *adminFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.server, "server", "", "base URL of the running server (default derived from ui.listen)")
	cmd.Flags().StringVar(&f.tokenFile, "token-file", "", "file holding the API token (default api.token_file)")
	cmd.Flags().BoolVar(&f.offline, "offline", false, "run locally holding the database lock (server stopped)")
}

// dialHost returns host:port of a listen address with an unspecified host
// replaced by loopback, so that it can be dialled.
func dialHost(listen string) (string, error) {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "", fmt.Errorf("parse listen address %q: %w", listen, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = loopbackHost
	}
	return net.JoinHostPort(host, port), nil
}

// adminClient calls the admin API of a running server.
type adminClient struct {
	base  string
	token string
	http  *http.Client
	poll  time.Duration
}

// newAdminClient resolves the server URL and the token from the flags and cfg.
func newAdminClient(cfg *config.Config, f adminFlags) (*adminClient, error) {
	base := strings.TrimRight(f.server, "/")
	if base == "" {
		hp, err := dialHost(cfg.UI.Listen)
		if err != nil {
			return nil, err
		}
		scheme := "http"
		if cfg.UI.TLS.Enabled() {
			scheme = "https"
		}
		base = scheme + "://" + hp
	}
	path := f.tokenFile
	if path == "" {
		path = cfg.API.TokenFile
	}
	tokens, err := readTokens(path)
	if err != nil {
		return nil, fmt.Errorf("api token: %w", err)
	}
	c := &adminClient{base: base, http: &http.Client{Timeout: 30 * time.Second}, poll: defaultPollInterval}
	if len(tokens) > 0 {
		c.token = tokens[0]
	}
	return c, nil
}

// jobView is the job document of the admin API.
type jobView struct {
	JobID string `json:"job_id"`
	Kind  string `json:"kind"`
	State string `json:"state"`
	Rows  int    `json:"rows"`
	Error string `json:"error"`
}

// runJob starts a job with POST path and waits until it is done or failed.
func (c *adminClient) runJob(ctx context.Context, path string, body any) (jobView, error) {
	var payload io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return jobView{}, fmt.Errorf("encode request: %w", err)
		}
		payload = bytes.NewReader(raw)
	}
	var started struct {
		JobID string `json:"job_id"`
	}
	if err := c.do(ctx, call{http.MethodPost, path, payload, http.StatusAccepted}, &started); err != nil {
		return jobView{}, err
	}
	if started.JobID == "" {
		return jobView{}, fmt.Errorf("%w: response without job_id", errAdminAPI)
	}
	for {
		var v jobView
		poll := call{http.MethodGet, "/v1/admin/jobs/" + started.JobID, http.NoBody, http.StatusOK}
		if err := c.do(ctx, poll, &v); err != nil {
			return jobView{}, err
		}
		switch v.State {
		case jobDone:
			return v, nil
		case jobFailed:
			return v, fmt.Errorf("%w: job %s failed: %s", errAdminAPI, v.JobID, v.Error)
		}
		select {
		case <-ctx.Done():
			return v, fmt.Errorf("wait for job %s: %w", v.JobID, ctx.Err())
		case <-time.After(c.poll):
		}
	}
}

// call describes one admin API request and the status it expects.
type call struct {
	method, path string
	body         io.Reader
	want         int
}

func (c *adminClient) do(ctx context.Context, rq call, out any) error {
	method, path, body, want := rq.method, rq.path, rq.body, rq.want
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s (is the server running? use --offline otherwise): %w", method, c.base+path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAdminResponse))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != want {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Error.Code != "" {
			return fmt.Errorf("%w: %s %s: %d %s: %s", errAdminAPI, method, path, resp.StatusCode,
				env.Error.Code, env.Error.Message)
		}
		return fmt.Errorf("%w: %s %s: unexpected status %d", errAdminAPI, method, path, resp.StatusCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: decode %s response: %w", errAdminAPI, path, err)
	}
	return nil
}
