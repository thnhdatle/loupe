// SPDX-License-Identifier: Apache-2.0

package config

import (
	"time"

	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/domain/content"
)

// EnvPrefix is the prefix of every environment override (LOUPE_<PATH_IN_UPPER_SNAKE>).
const EnvPrefix = "LOUPE_"

// ConfigEnv names the environment variable that holds the config file path.
const ConfigEnv = EnvPrefix + "CONFIG"

// Config is the complete Loupe configuration. Treat it as
// immutable after Load. Start from Default to build one in code.
type Config struct {
	Receiver        Receiver    `yaml:"receiver"`
	UI              UI          `yaml:"ui"`
	API             API         `yaml:"api"`
	Admin           Admin       `yaml:"admin"`
	Debug           Debug       `yaml:"debug"`
	Storage         Storage     `yaml:"storage"`
	Runtime         Runtime     `yaml:"runtime"`
	Ingest          Ingest      `yaml:"ingest"`
	Writer          Writer      `yaml:"writer"`
	Content         Content     `yaml:"content"`
	Retention       Retention   `yaml:"retention"`
	Pricing         Pricing     `yaml:"pricing"`
	SelfTracing     SelfTracing `yaml:"self_tracing"`
	Log             Log         `yaml:"log"`
	ShutdownTimeout Duration    `yaml:"shutdown_timeout"`
}

// TLS holds the certificate and key files of one listener; both or neither.
type TLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

// Enabled reports whether both files are set.
func (t TLS) Enabled() bool { return t.CertFile != "" && t.KeyFile != "" }

// Listener is one OTLP receiver listener.
type Listener struct {
	Listen string `yaml:"listen"`
	TLS    TLS    `yaml:"tls"`
}

// Receiver is the receiver section.
type Receiver struct {
	GRPC                 Listener `yaml:"grpc"`
	HTTP                 Listener `yaml:"http"`
	TokensFile           string   `yaml:"tokens_file"`
	AllowUnauthenticated bool     `yaml:"allow_unauthenticated"`
	MaxRequestBytes      Size     `yaml:"max_request_bytes"`
	MaxSpansPerRequest   int      `yaml:"max_spans_per_request"`
	MaxAttributeBytes    Size     `yaml:"max_attribute_bytes"`
	MaxInflightBytes     Size     `yaml:"max_inflight_bytes"`
}

// UI is the dashboard and REST API listener.
type UI struct {
	Listen               string   `yaml:"listen"`
	TLS                  TLS      `yaml:"tls"`
	AllowUnauthenticated bool     `yaml:"allow_unauthenticated"`
	SessionTTL           Duration `yaml:"session_ttl"`
	SecureCookie         bool     `yaml:"secure_cookie"`
}

// API is the API token section.
type API struct {
	TokenFile     string `yaml:"token_file"`
	GenerateToken bool   `yaml:"generate_token"`
}

// Admin is the admin listener.
type Admin struct {
	Listen string `yaml:"listen"`
}

// Debug holds debugging switches.
type Debug struct {
	Pprof bool `yaml:"pprof"`
}

// Storage is the database location.
type Storage struct {
	Path                string `yaml:"path"`
	BackupBeforeMigrate bool   `yaml:"backup_before_migrate"`
}

// Runtime holds process-level limits.
type Runtime struct {
	// MemoryLimit is the GOMEMLIMIT applied unless the environment sets one; 0 = off.
	MemoryLimit Size `yaml:"memory_limit"`
}

// Ingest is the bounded ingest queue.
type Ingest struct {
	QueueSpans int    `yaml:"queue_spans"`
	QueueBytes Size   `yaml:"queue_bytes"`
	Overflow   string `yaml:"overflow"` // reject | drop
	SyncAck    bool   `yaml:"sync_ack"`
}

// Writer is the single-writer batching policy.
type Writer struct {
	BatchSpans    int      `yaml:"batch_spans"`
	BatchInterval Duration `yaml:"batch_interval"`
}

// Content is the capture and redaction policy.
type Content struct {
	Capture  string       `yaml:"capture"` // off | on | metadata_only
	MaxBytes Size         `yaml:"max_bytes"`
	Redact   []RedactRule `yaml:"redact"`
}

// Retention is the retention section.
type Retention struct {
	Spans         Duration `yaml:"spans"`
	Content       Duration `yaml:"content"`
	RollupsHourly Duration `yaml:"rollups_hourly"`
	RollupsDaily  Duration `yaml:"rollups_daily"`
	OrphanContent Duration `yaml:"orphan_content"`
	MaxDBBytes    Size     `yaml:"max_db_bytes"`
	Interval      Duration `yaml:"interval"`
	Batch         int      `yaml:"batch"`
}

// Pricing points at the user price table merged over the embedded default.
type Pricing struct {
	File string `yaml:"file"`
}

// SelfTracing optionally exports Loupe's own traces elsewhere.
type SelfTracing struct {
	Endpoint string `yaml:"endpoint"`
}

// Log is the logging section.
type Log struct {
	Level     string `yaml:"level"`  // debug | info | warn | error
	Format    string `yaml:"format"` // json | text
	AccessLog bool   `yaml:"access_log"`
}

// Default returns the documented defaults.
func Default() Config {
	r := retention.DefaultConfig()
	return Config{
		Receiver: Receiver{
			GRPC:               Listener{Listen: "127.0.0.1:4317"},
			HTTP:               Listener{Listen: "127.0.0.1:4318"},
			MaxRequestBytes:    16 << 20,
			MaxSpansPerRequest: 10_000,
			MaxAttributeBytes:  64 << 10,
			MaxInflightBytes:   64 << 20,
		},
		UI:      UI{Listen: "127.0.0.1:3000", SessionTTL: Duration(12 * time.Hour)},
		Admin:   Admin{Listen: "127.0.0.1:9092"},
		Storage: Storage{Path: "./loupe.db", BackupBeforeMigrate: true},
		Runtime: Runtime{MemoryLimit: 640 << 20},
		Ingest:  Ingest{QueueSpans: 100_000, QueueBytes: 256 << 20, Overflow: "reject"},
		Writer:  Writer{BatchSpans: 2000, BatchInterval: Duration(200 * time.Millisecond)},
		Content: Content{
			Capture:  string(content.ModeOff),
			MaxBytes: 32 << 10,
			Redact:   []RedactRule{{Preset: content.PresetEmail}, {Preset: content.PresetAPIKey}},
		},
		Retention: Retention{
			Spans:         Duration(r.Spans),
			Content:       Duration(r.Content),
			RollupsHourly: Duration(r.RollupsHourly),
			RollupsDaily:  Duration(r.RollupsDaily),
			OrphanContent: Duration(r.OrphanContent),
			MaxDBBytes:    Size(r.MaxDBBytes),
			Interval:      Duration(r.Interval),
			Batch:         r.Batch,
		},
		Log:             Log{Level: "info", Format: "json"},
		ShutdownTimeout: Duration(30 * time.Second),
	}
}

// RetentionConfig converts the retention section for app/retention.
func (c *Config) RetentionConfig() retention.Config {
	r := c.Retention
	return retention.Config{
		Spans:         r.Spans.Std(),
		Content:       r.Content.Std(),
		RollupsHourly: r.RollupsHourly.Std(),
		RollupsDaily:  r.RollupsDaily.Std(),
		OrphanContent: r.OrphanContent.Std(),
		MaxDBBytes:    r.MaxDBBytes.Bytes(),
		Interval:      r.Interval.Std(),
		Batch:         r.Batch,
	}
}

// SecureCookieEnabled reports whether the session cookie carries Secure: ui.secure_cookie,
// forced on when ui.tls is set.
func (c *Config) SecureCookieEnabled() bool { return c.UI.SecureCookie || c.UI.TLS.Enabled() }

// RedactPresets returns the preset names of content.redact in order.
func (c *Content) RedactPresets() []string {
	var out []string
	for _, r := range c.Redact {
		if r.Preset != "" {
			out = append(out, r.Preset)
		}
	}
	return out
}

// RedactRules returns the custom rules of content.redact in order.
func (c *Content) RedactRules() []content.Rule {
	var out []content.Rule
	for _, r := range c.Redact {
		if r.Preset == "" {
			out = append(out, content.Rule{Name: r.Name, Pattern: r.Pattern, Replacement: r.Replacement})
		}
	}
	return out
}
