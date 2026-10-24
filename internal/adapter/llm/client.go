// SPDX-License-Identifier: Apache-2.0

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/thnhdatle/loupe/internal/app/analyze"
)

const (
	maxResponseBytes = 1 << 20
	maxErrorRunes    = 200
)

var (
	ErrInvalidOptions = errors.New("llm: invalid options")
	ErrStatus         = errors.New("llm: unexpected status")
	ErrTooLarge       = errors.New("llm: response too large")
	ErrNoChoice       = errors.New("llm: response has no choices")
)

type Options struct {
	BaseURL    string
	Model      string
	APIKey     string
	HTTPClient *http.Client
}

// Client sends prompts to the chat completions endpoint under a base URL such as http://127.0.0.1:11434/v1.
type Client struct {
	endpoint string
	model    string
	apiKey   string
	http     *http.Client
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message message `json:"message"`
	} `json:"choices"`
}

type errorResponse struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// New returns a Client; requests are bounded by the caller's context.
func New(o Options) (*Client, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%w: base url %q", ErrInvalidOptions, o.BaseURL)
	}
	if o.Model == "" {
		return nil, fmt.Errorf("%w: empty model", ErrInvalidOptions)
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{endpoint: u.JoinPath("chat/completions").String(), model: o.Model, apiKey: o.APIKey, http: hc}, nil
}

// Complete sends p as a system and a user message and returns the first choice's content.
func (c *Client) Complete(ctx context.Context, p analyze.Prompt) (string, error) {
	body, err := json.Marshal(chatRequest{Model: c.model, Messages: []message{
		{Role: "system", Content: p.System},
		{Role: "user", Content: p.User},
	}})
	if err != nil {
		return "", fmt.Errorf("llm: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("llm: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("llm: read response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return "", ErrTooLarge
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w %d: %s", ErrStatus, resp.StatusCode, errorMessage(data))
	}
	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("llm: decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", ErrNoChoice
	}
	return out.Choices[0].Message.Content, nil
}

func errorMessage(data []byte) string {
	var e errorResponse
	msg := strings.TrimSpace(string(data))
	if json.Unmarshal(data, &e) == nil && e.Error.Message != "" {
		msg = e.Error.Message
	}
	if r := []rune(msg); len(r) > maxErrorRunes {
		msg = string(r[:maxErrorRunes]) + "…"
	}
	return msg
}
