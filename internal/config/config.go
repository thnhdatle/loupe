// SPDX-License-Identifier: Apache-2.0

package config

import (
	"time"

	"github.com/thnhdatle/loupe/internal/app/retention"
	"github.com/thnhdatle/loupe/internal/domain/content"
)

const EnvPrefix = "LOUPE_"

const ConfigEnv = EnvPrefix + "CONFIG"

// Config is the complete Loupe configuration.
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

type TLS struct {
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

func (t TLS) Enabled() bool { return t.CertFile != "" && t.KeyFile != "" }

type Listener struct {
	Listen string `yaml:"listen"`
	TLS    TLS    `yaml:"tls"`
}

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

type UI struct {
	Listen               string   `yaml:"listen"`
	TLS                  TLS      `yaml:"tls"`
	AllowUnauthenticated bool     `yaml:"allow_unauthenticated"`
	SessionTTL           Duration `yaml:"session_ttl"`
	SecureCookie         bool     `yaml:"secure_cookie"`
}

type API struct {
	TokenFile     string `yaml:"token_file"`
	GenerateToken bool   `yaml:"generate_token"`
}

type Admin struct {
	Listen string `yaml:"listen"`
}

type Debug struct {
	Pprof bool `yaml:"pprof"`
}

type Storage struct {
	Path                string `yaml:"path"`
	BackupBeforeMigrate bool   `yaml:"backup_before_migrate"`
}

type Runtime struct {
	MemoryLimit Size `yaml:"memory_limit"`
}

type Ingest struct {
	QueueSpans int    `yaml:"queue_spans"`
	QueueBytes Size   `yaml:"queue_bytes"`
	Overflow   string `yaml:"overflow"`
	SyncAck    bool   `yaml:"sync_ack"`
}

type Writer struct {
	BatchSpans    int      `yaml:"batch_spans"`
	BatchInterval Duration `yaml:"batch_interval"`
}

type Content struct {
	Capture  string       `yaml:"capture"`
	MaxBytes Size         `yaml:"max_bytes"`
	Redact   []RedactRule `yaml:"redact"`
}

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

type Pricing struct {
	File string `yaml:"file"`
}

type SelfTracing struct {
	Endpoint string `yaml:"endpoint"`
}

type Log struct {
	Level     string `yaml:"level"`
	Format    string `yaml:"format"`
	AccessLog bool   `yaml:"access_log"`
}

// Default returns the default configuration.
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

func (c *Config) SecureCookieEnabled() bool { return c.UI.SecureCookie || c.UI.TLS.Enabled() }

func (c *Content) RedactPresets() []string {
	var out []string
	for _, r := range c.Redact {
		if r.Preset != "" {
			out = append(out, r.Preset)
		}
	}
	return out
}

func (c *Content) RedactRules() []content.Rule {
	var out []content.Rule
	for _, r := range c.Redact {
		if r.Preset == "" {
			out = append(out, content.Rule{Name: r.Name, Pattern: r.Pattern, Replacement: r.Replacement})
		}
	}
	return out
}
