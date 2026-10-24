// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/thnhdatle/loupe/internal/adapter/httpapi"
	"github.com/thnhdatle/loupe/internal/adapter/llm"
	"github.com/thnhdatle/loupe/internal/app/analyze"
)

// newAnalyzer returns nil when analyze.base_url is empty.
func (s *server) newAnalyzer() (httpapi.Analyzer, error) {
	cfg := s.cfg
	if !cfg.Analyze.Enabled() {
		return nil, nil //nolint:nilnil // nil means Analyze is off.
	}
	var key string
	if cfg.Analyze.APIKeyFile != "" {
		keys, err := readTokens(cfg.Analyze.APIKeyFile)
		if err != nil {
			return nil, fmt.Errorf("analyze.api_key_file: %w", err)
		}
		key = keys[0]
	}
	client, err := llm.New(llm.Options{BaseURL: cfg.Analyze.BaseURL, Model: cfg.Analyze.Model, APIKey: key})
	if err != nil {
		return nil, fmt.Errorf("analyze model client: %w", err)
	}
	svc, err := analyze.New(s.store, client, analyze.Config{
		Timeout:        cfg.Analyze.Timeout.Std(),
		MaxSpans:       cfg.Analyze.MaxSpans,
		IncludeContent: cfg.AnalyzeContent(),
	}, analyze.WithLogger(s.log))
	if err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}
	s.log.Info("analyze enabled", "component", "analyze", "model", cfg.Analyze.Model,
		"include_content", cfg.AnalyzeContent())
	return svc, nil
}
