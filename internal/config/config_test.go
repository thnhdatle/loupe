// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/thnhdatle/loupe/internal/config"
)

func env(m map[string]string) config.LookupEnv {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "loupe.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func TestDefaultIsValid(t *testing.T) {
	t.Parallel()
	cfg := config.Default()
	require.NoError(t, cfg.Validate())
	assert.Equal(t, "127.0.0.1:4317", cfg.Receiver.GRPC.Listen)
	assert.Equal(t, int64(10<<30), cfg.Retention.MaxDBBytes.Bytes())
	assert.Equal(t, 14*24*time.Hour, cfg.RetentionConfig().Spans)
	assert.Equal(t, []string{"email", "api_key"}, cfg.Content.RedactPresets())
	assert.Empty(t, cfg.Warnings())
}

func TestExampleFileMatchesDefaults(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../deploy/config.example.yaml")
	require.NoError(t, err)
	cfg, err := config.Parse(data)
	require.NoError(t, err)
	def := config.Default()
	assert.Equal(t, &def, cfg)

	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(data, &doc))
	assertKeys(t, reflect.TypeOf(def), doc, "")
}

func assertKeys(t *testing.T, typ reflect.Type, doc map[string]any, prefix string) {
	t.Helper()
	for i := range typ.NumField() {
		f := typ.Field(i)
		key := f.Tag.Get("yaml")
		path := prefix + key
		val, ok := doc[key]
		if !assert.Truef(t, ok, "example config lacks %s", path) {
			continue
		}
		if f.Type.Kind() == reflect.Struct && f.Type.PkgPath() != "" && f.Type.NumField() > 0 &&
			f.Type != reflect.TypeFor[config.Duration]() {
			sub, isMap := val.(map[string]any)
			require.Truef(t, isMap, "%s must be a mapping", path)
			assertKeys(t, f.Type, sub, path+".")
		}
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		env     map[string]string
		check   func(t *testing.T, c *config.Config)
		wantErr string
	}{
		{name: "empty file is defaults", yaml: "", check: func(t *testing.T, c *config.Config) {
			t.Helper()
			def := config.Default()
			assert.Equal(t, &def, c)
		}},
		{
			name: "file overrides", yaml: "retention:\n  spans: 30d\ncontent: {capture: on, max_bytes: 1MiB}\n",
			check: func(t *testing.T, c *config.Config) {
				t.Helper()
				assert.Equal(t, 30*24*time.Hour, c.Retention.Spans.Std())
				assert.Equal(t, "on", c.Content.Capture)
				assert.Equal(t, int64(1<<20), c.Content.MaxBytes.Bytes())
				assert.Equal(t, 7*24*time.Hour, c.Retention.Content.Std(), "other defaults kept")
			},
		},
		{
			name: "env beats file", yaml: "ingest: {queue_spans: 5}\n",
			env: map[string]string{
				"LOUPE_INGEST_QUEUE_SPANS": "7", "LOUPE_RECEIVER_GRPC_LISTEN": "127.0.0.1:5000",
				"LOUPE_INGEST_SYNC_ACK": "true", "LOUPE_RETENTION_INTERVAL": "1h", "LOUPE_CONTENT_REDACT": "phone, email",
			},
			check: func(t *testing.T, c *config.Config) {
				t.Helper()
				assert.Equal(t, 7, c.Ingest.QueueSpans)
				assert.Equal(t, "127.0.0.1:5000", c.Receiver.GRPC.Listen)
				assert.True(t, c.Ingest.SyncAck)
				assert.Equal(t, time.Hour, c.Retention.Interval.Std())
				assert.Equal(t, []string{"phone", "email"}, c.Content.RedactPresets())
			},
		},
		{
			name: "custom redact rule",
			yaml: "content:\n  redact:\n    - email\n    - {name: t, pattern: 'TCK-[0-9]+', replacement: '[T]'}\n",
			check: func(t *testing.T, c *config.Config) {
				t.Helper()
				assert.Equal(t, []string{"email"}, c.Content.RedactPresets())
				require.Len(t, c.Content.RedactRules(), 1)
				assert.Equal(t, "[T]", c.Content.RedactRules()[0].Replacement)
			},
		},
		{name: "unknown key", yaml: "retention:\n  spanz: 3d\n", wantErr: "spanz"},
		{name: "unknown redact key", yaml: "content:\n  redact:\n    - {name: a, patern: b}\n", wantErr: "patern"},
		{name: "bad redact entry", yaml: "content:\n  redact: [[a]]\n", wantErr: "preset name"},
		{name: "bad yaml", yaml: "receiver: [", wantErr: "parse"},
		{name: "bad unit", yaml: "retention:\n  spans: 14x\n", wantErr: "invalid duration"},
		{
			name:    "bad env int",
			env:     map[string]string{"LOUPE_INGEST_QUEUE_SPANS": "x"},
			wantErr: "LOUPE_INGEST_QUEUE_SPANS",
		},
		{name: "bad env bool", env: map[string]string{"LOUPE_INGEST_SYNC_ACK": "maybe"}, wantErr: "boolean"},
		{name: "bad env size", env: map[string]string{"LOUPE_RUNTIME_MEMORY_LIMIT": "lots"}, wantErr: "invalid size"},
		{name: "invalid values fail", yaml: "ingest: {overflow: sideways}\n", wantErr: "ingest.overflow"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := ""
			if tc.yaml != "" || tc.name == "empty file is defaults" {
				path = writeFile(t, tc.yaml)
			}
			c, err := config.LoadWith(path, env(tc.env))
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			tc.check(t, c)
		})
	}
}

func TestLoadPathFromEnvAndMissingFile(t *testing.T) {
	t.Parallel()
	p := writeFile(t, "admin: {listen: '127.0.0.1:9999'}\n")
	c, err := config.LoadWith("", env(map[string]string{"LOUPE_CONFIG": p}))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9999", c.Admin.Listen)

	_, err = config.LoadWith(filepath.Join(t.TempDir(), "nope.yaml"), env(nil))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoadUnknownKeyWrapsErrInvalid(t *testing.T) {
	t.Parallel()
	_, err := config.LoadWith(writeFile(t, "bogus: 1\n"), env(nil))
	require.ErrorIs(t, err, config.ErrInvalid)
}

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mod  func(c *config.Config)
		want []string
	}{
		{name: "defaults ok", mod: func(*config.Config) {}},
		{
			name: "non-loopback receivers need auth",
			mod: func(c *config.Config) {
				c.Receiver.GRPC.Listen = "0.0.0.0:4317"
				c.Receiver.HTTP.Listen = "10.0.0.5:4318"
			},
			want: []string{"receiver.grpc.listen", "receiver.http.listen"},
		},
		{
			name: "non-loopback receiver with tokens ok",
			mod: func(c *config.Config) {
				c.Receiver.GRPC.Listen = "0.0.0.0:4317"
				c.Receiver.TokensFile = "/t"
			},
		},
		{
			name: "non-loopback receiver allow_unauthenticated ok",
			mod: func(c *config.Config) {
				c.Receiver.HTTP.Listen = ":4318"
				c.Receiver.AllowUnauthenticated = true
			},
		},
		{
			name: "non-loopback ui needs token or flag",
			mod:  func(c *config.Config) { c.UI.Listen = "0.0.0.0:3000" },
			want: []string{"ui.listen"},
		},
		{
			name: "non-loopback ui with token ok",
			mod:  func(c *config.Config) { c.UI.Listen = "0.0.0.0:3000"; c.API.TokenFile = "/a" },
		},
		{
			name: "non-loopback ui with flag ok",
			mod:  func(c *config.Config) { c.UI.Listen = "[::]:3000"; c.UI.AllowUnauthenticated = true },
		},
		{name: "localhost is loopback", mod: func(c *config.Config) { c.UI.Listen = "localhost:3000" }},
		{name: "ipv6 loopback", mod: func(c *config.Config) { c.UI.Listen = "[::1]:3000" }},
		{
			name: "hostname is non-loopback",
			mod:  func(c *config.Config) { c.UI.Listen = "loupe.internal:3000" },
			want: []string{"ui.listen"},
		},
		{
			name: "identical listeners collide",
			mod:  func(c *config.Config) { c.Admin.Listen = c.UI.Listen },
			want: []string{"admin.listen"},
		},
		{
			name: "wildcard collides with loopback on same port",
			mod: func(c *config.Config) {
				c.Receiver.HTTP.Listen = "0.0.0.0:3000"
				c.Receiver.AllowUnauthenticated = true
			},
			want: []string{"receiver.http.listen"},
		},
		{
			name: "different ports on distinct hosts ok",
			mod:  func(c *config.Config) { c.Admin.Listen = "127.0.0.2:3000" },
		},
		{name: "bad listen", mod: func(c *config.Config) { c.Admin.Listen = "nine" }, want: []string{"want host:port"}},
		{
			name: "bad port",
			mod:  func(c *config.Config) { c.Admin.Listen = "127.0.0.1:99999" },
			want: []string{"invalid port"},
		},
		{name: "tls cert only", mod: func(c *config.Config) { c.UI.TLS.CertFile = "c.pem" }, want: []string{"ui.tls"}},
		{
			name: "tls key only",
			mod:  func(c *config.Config) { c.Receiver.GRPC.TLS.KeyFile = "k.pem" },
			want: []string{"receiver.grpc.tls"},
		},
		{
			name: "tls both ok",
			mod:  func(c *config.Config) { c.Receiver.HTTP.TLS = config.TLS{CertFile: "c", KeyFile: "k"} },
		},
		{
			name: "generate token needs file",
			mod:  func(c *config.Config) { c.API.GenerateToken = true },
			want: []string{"api.generate_token"},
		},
		{
			name: "generate token with file ok",
			mod:  func(c *config.Config) { c.API.GenerateToken = true; c.API.TokenFile = "/data/api_token" },
		},
		{
			name: "content above spans",
			mod:  func(c *config.Config) { c.Retention.Content = c.Retention.Spans + 1 },
			want: []string{"retention.content"},
		},
		{
			name: "hourly above daily",
			mod:  func(c *config.Config) { c.Retention.RollupsHourly = c.Retention.RollupsDaily + 1 },
			want: []string{"retention.rollups_hourly"},
		},
		{name: "max_db_bytes zero ok", mod: func(c *config.Config) { c.Retention.MaxDBBytes = 0 }},
		{
			name: "max_db_bytes too small",
			mod:  func(c *config.Config) { c.Retention.MaxDBBytes = 1 << 20 },
			want: []string{"max_db_bytes"},
		},
		{
			name: "zero limits",
			mod:  func(c *config.Config) { c.Ingest.QueueSpans = 0; c.Writer.BatchSpans = 0 },
			want: []string{"ingest.queue_spans", "writer.batch_spans"},
		},
		{
			name: "negative memory limit",
			mod:  func(c *config.Config) { c.Runtime.MemoryLimit = -1 },
			want: []string{"runtime.memory_limit"},
		},
		{name: "memory limit zero ok", mod: func(c *config.Config) { c.Runtime.MemoryLimit = 0 }},
		{
			name: "empty storage path",
			mod:  func(c *config.Config) { c.Storage.Path = "" },
			want: []string{"storage.path"},
		},
		{
			name: "bad overflow",
			mod:  func(c *config.Config) { c.Ingest.Overflow = "x" },
			want: []string{"ingest.overflow"},
		},
		{name: "drop overflow ok", mod: func(c *config.Config) { c.Ingest.Overflow = "drop" }},
		{
			name: "bad capture",
			mod:  func(c *config.Config) { c.Content.Capture = "all" },
			want: []string{"content.capture"},
		},
		{name: "metadata_only ok", mod: func(c *config.Config) { c.Content.Capture = "metadata_only" }},
		{name: "bad log level", mod: func(c *config.Config) { c.Log.Level = "trace" }, want: []string{"log.level"}},
		{name: "bad log format", mod: func(c *config.Config) { c.Log.Format = "xml" }, want: []string{"log.format"}},
		{
			name: "unknown preset",
			mod:  func(c *config.Config) { c.Content.Redact = []config.RedactRule{{Preset: "ssn"}} },
			want: []string{"ssn"},
		},
		{
			name: "bad custom rule",
			mod: func(c *config.Config) {
				c.Content.Redact = []config.RedactRule{{Name: "a", Pattern: "("}, {Name: "", Pattern: "x"}}
			},
			want: []string{"content.redact", "content.redact"},
		},
		{
			name: "duplicate rule name",
			mod: func(c *config.Config) {
				c.Content.Redact = []config.RedactRule{{Name: "a", Pattern: "x"}, {Name: "a", Pattern: "y"}}
			},
			want: []string{"duplicate rule name"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := config.Default()
			tc.mod(&c)
			err := c.Validate()
			if len(tc.want) == 0 {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, config.ErrInvalid)
			var ce *config.Error
			require.ErrorAs(t, err, &ce)
			require.Len(t, ce.Problems(), len(tc.want), err.Error())
			for _, w := range tc.want {
				assert.Contains(t, err.Error(), w)
			}
		})
	}
}

func TestValidateReportsAllProblems(t *testing.T) {
	t.Parallel()
	c := config.Default()
	c.Admin.Listen = c.UI.Listen
	c.Ingest.Overflow = "x"
	c.API.GenerateToken = true
	c.Retention.MaxDBBytes = 5
	var ce *config.Error
	require.ErrorAs(t, c.Validate(), &ce)
	assert.Len(t, ce.Problems(), 4)
}

func TestWarnings(t *testing.T) {
	t.Parallel()
	c := config.Default()
	c.Receiver.HTTP.Listen = "0.0.0.0:4318"
	c.Receiver.GRPC.Listen = "0.0.0.0:4317"
	c.Receiver.GRPC.TLS = config.TLS{CertFile: "c", KeyFile: "k"}
	w := c.Warnings()
	require.Len(t, w, 1)
	assert.Contains(t, w[0], "receiver.http")
}

func TestSecureCookieForcedByTLS(t *testing.T) {
	t.Parallel()
	c := config.Default()
	assert.False(t, c.SecureCookieEnabled())
	c.UI.TLS = config.TLS{CertFile: "c", KeyFile: "k"}
	assert.True(t, c.SecureCookieEnabled())
}

func TestPrintRoundTripAndRedact(t *testing.T) {
	t.Parallel()
	c := config.Default()
	c.Receiver.TokensFile = "/secrets/tokens"
	c.API.TokenFile = "/secrets/api"
	c.UI.TLS = config.TLS{CertFile: "/c.pem", KeyFile: "/k.pem"}
	c.SelfTracing.Endpoint = "https://user:pw@otel.example:4318/v1/traces?key=abc"
	c.Content.Redact = append(c.Content.Redact, config.RedactRule{Name: "t", Pattern: "T+", Replacement: "[T]"})

	var plain bytes.Buffer
	require.NoError(t, c.WriteYAML(&plain))
	back, err := config.Parse(plain.Bytes())
	require.NoError(t, err)
	assert.Equal(t, &c, back, "print output loads back identically")

	r := c.Redact()
	var out bytes.Buffer
	require.NoError(t, r.WriteYAML(&out))
	for _, secret := range []string{"/secrets/tokens", "/secrets/api", "/k.pem", "pw", "key=abc"} {
		assert.NotContains(t, out.String(), secret)
	}
	assert.Contains(t, out.String(), config.Redacted)
	assert.Contains(t, out.String(), "/c.pem", "certificates are public")
	assert.Equal(t, "/secrets/api", c.API.TokenFile, "Redact does not modify the receiver")
	assert.Len(t, r.Content.Redact, len(c.Content.Redact))
	assert.Contains(t, out.String(), "spans: 14d")
}

func TestRedactEmptyAndOpaqueEndpoint(t *testing.T) {
	t.Parallel()
	c := config.Default()
	r := c.Redact()
	assert.Empty(t, r.API.TokenFile)
	assert.Empty(t, r.SelfTracing.Endpoint)
	c.SelfTracing.Endpoint = "not a url ::"
	assert.Equal(t, config.Redacted, c.Redact().SelfTracing.Endpoint)
}

func TestEnvName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "LOUPE_RECEIVER_GRPC_LISTEN", config.EnvName("receiver.grpc.listen"))
	assert.Equal(t, "LOUPE_SHUTDOWN_TIMEOUT", config.EnvName("shutdown_timeout"))
}
