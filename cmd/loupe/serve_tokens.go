// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/thnhdatle/loupe/internal/config"
)

const (
	generatedTokenBytes = 32
	tokenFileMode       = 0o600
)

func readTokens(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: the operator names this file in the config.
	if err != nil {
		return nil, fmt.Errorf("read tokens file: %w", err)
	}
	var tokens []string
	for line := range strings.SplitSeq(string(data), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("tokens file %s: no tokens", path)
	}
	return tokens, nil
}

func ensureAPIToken(cfg *config.Config) (bool, error) {
	if !cfg.API.GenerateToken || cfg.API.TokenFile == "" {
		return false, nil
	}
	if _, err := os.Stat(cfg.API.TokenFile); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat api token file: %w", err)
	}
	raw := make([]byte, generatedTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return false, fmt.Errorf("generate api token: %w", err)
	}
	f, err := os.OpenFile(cfg.API.TokenFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, tokenFileMode)
	if err != nil {
		return false, fmt.Errorf("create api token file: %w", err)
	}
	_, werr := f.WriteString(hex.EncodeToString(raw) + "\n")
	cerr := f.Close()
	if werr != nil {
		return false, fmt.Errorf("write api token file: %w", werr)
	}
	if cerr != nil {
		return false, fmt.Errorf("close api token file: %w", cerr)
	}
	return true, nil
}

func loadTLS(t config.TLS) (*tls.Config, error) {
	if !t.Enabled() {
		return nil, nil //nolint:nilnil // nil means plain TCP.
	}
	cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls key pair: %w", err)
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
