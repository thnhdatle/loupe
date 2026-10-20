// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thnhdatle/loupe/internal/config"
)

func TestVersionCmd_PrintsFields(t *testing.T) {
	t.Parallel()
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version:", "commit:", "build_date:", "go_version:"} {
		if !strings.Contains(out.String(), key) {
			t.Errorf("missing %q in %q", key, out.String())
		}
	}
}

func runRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "loupe.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	good := writeConfig(t, "retention: {spans: 30d}\n")
	out, _, err := runRoot(t, "config", "validate", "--config", good)
	if err != nil || !strings.Contains(out, "valid") {
		t.Fatalf("out=%q err=%v", out, err)
	}

	bad := writeConfig(t, "ingest: {overflow: nope}\nadmin: {listen: \"127.0.0.1:3000\"}\n")
	_, _, err = runRoot(t, "config", "validate", "--config", bad)
	if !errors.Is(err, config.ErrInvalid) || exitCode(err) != 2 {
		t.Fatalf("err=%v exit=%d, want ErrInvalid and 2", err, exitCode(err))
	}
	for _, want := range []string{"ingest.overflow", "must not collide"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q (all problems at once)", err, want)
		}
	}

	_, _, err = runRoot(t, "config", "validate", "--config", filepath.Join(t.TempDir(), "missing.yaml"))
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("missing file: err=%v exit=%d, want exit 1", err, exitCode(err))
	}
}

func TestConfigValidateWarnsOnPlainNonLoopback(t *testing.T) {
	t.Parallel()
	p := writeConfig(t, "ui: {listen: \"0.0.0.0:3000\", allow_unauthenticated: true}\n")
	_, stderr, err := runRoot(t, "config", "validate", "--config", p)
	if err != nil || !strings.Contains(stderr, "warning:") {
		t.Fatalf("stderr=%q err=%v", stderr, err)
	}
}

func TestConfigValidatePricingFileAndSelfTracing(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "prices.yaml")
	p := writeConfig(t, "pricing: {file: "+missing+"}\n")
	_, _, err := runRoot(t, "config", "validate", "--config", p)
	if !errors.Is(err, config.ErrInvalid) || !strings.Contains(err.Error(), missing) {
		t.Fatalf("missing pricing file: err=%v, want ErrInvalid naming %s", err, missing)
	}
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if werr := os.WriteFile(bad, []byte("not: [valid"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	_, _, err = runRoot(t, "config", "validate", "--config", writeConfig(t, "pricing: {file: "+bad+"}\n"))
	if !errors.Is(err, config.ErrInvalid) {
		t.Fatalf("unparsable pricing file: err=%v", err)
	}
	st := writeConfig(t, "self_tracing: {endpoint: \"http://x:4318\"}\n")
	_, stderr, err := runRoot(t, "config", "validate", "--config", st)
	if err != nil || !strings.Contains(stderr, "self_tracing.endpoint") {
		t.Fatalf("stderr=%q err=%v", stderr, err)
	}
}

func TestConfigPrint(t *testing.T) {
	t.Parallel()
	p := writeConfig(t, "api: {token_file: /secrets/api_token}\n")
	plain, _, err := runRoot(t, "config", "print", "--config", p)
	if err != nil || !strings.Contains(plain, "/secrets/api_token") || !strings.Contains(plain, "spans: 14d") {
		t.Fatalf("plain=%q err=%v", plain, err)
	}
	redacted, _, err := runRoot(t, "config", "print", "--redact", "--config", p)
	if err != nil || strings.Contains(redacted, "/secrets/api_token") || !strings.Contains(redacted, config.Redacted) {
		t.Fatalf("redacted=%q err=%v", redacted, err)
	}
}
