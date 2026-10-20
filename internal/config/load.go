// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// LookupEnv has the signature of os.LookupEnv; tests inject their own.
type LookupEnv func(key string) (string, bool)

// Load reads the YAML file at path (or the file named by LOUPE_CONFIG when path
// is empty; no file means defaults), applies environment overrides and
// validates. Unknown keys are errors. Precedence is env > file > defaults;
// command-line flags are applied by the caller on top.
func Load(path string) (*Config, error) { return LoadWith(path, os.LookupEnv) }

// LoadWith is Load with an injected environment.
func LoadWith(path string, env LookupEnv) (*Config, error) {
	cfg, err := LoadUnvalidated(path, env)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// LoadUnvalidated parses the file and environment like LoadWith but skips
// Validate; it still fails on syntax errors, unknown keys and unparsable env values.
func LoadUnvalidated(path string, env LookupEnv) (*Config, error) {
	if path == "" {
		path, _ = env(ConfigEnv)
	}
	cfg := Default()
	if path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // G304: the operator chooses the config path.
		if err != nil {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		if err := decode(data, &cfg); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	if err := applyEnv(&cfg, env); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Parse decodes YAML text over the defaults and validates it, without environment.
func Parse(data []byte) (*Config, error) {
	cfg := Default()
	if err := decode(data, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func decode(data []byte, cfg *Config) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// EnvName returns the environment variable for a dotted config path
// ("receiver.grpc.listen" gives LOUPE_RECEIVER_GRPC_LISTEN).
func EnvName(path string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
}

// applyEnv overrides every scalar option from LOUPE_<PATH>. content.redact takes
// a comma-separated preset list. Problems are collected and returned together.
func applyEnv(cfg *Config, env LookupEnv) error {
	var errs []error
	walk(reflect.ValueOf(cfg).Elem(), "", func(path string, v reflect.Value) {
		raw, ok := env(EnvName(path))
		if !ok {
			return
		}
		if err := setValue(v, raw); err != nil {
			errs = append(errs, fmt.Errorf("%s=%q: %w", EnvName(path), raw, err))
		}
	})
	if len(errs) > 0 {
		return fmt.Errorf("config: %w", &Error{errs: errs})
	}
	return nil
}

var textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()

// walk calls fn for every leaf (scalar, text-marshalled or slice) field with its yaml path.
func walk(v reflect.Value, prefix string, fn func(path string, v reflect.Value)) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		fv := v.Field(i)
		if fv.Kind() == reflect.Struct && !reflect.PointerTo(fv.Type()).Implements(textUnmarshaler) {
			walk(fv, path, fn)
			continue
		}
		fn(path, fv)
	}
}

func setValue(v reflect.Value, raw string) error {
	if u, ok := v.Addr().Interface().(encoding.TextUnmarshaler); ok {
		return u.UnmarshalText([]byte(raw))
	}
	switch v.Kind() { //nolint:exhaustive // only the kinds used by config fields exist.
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("not a boolean: %w", err)
		}
		v.SetBool(b)
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("not an integer: %w", err)
		}
		v.SetInt(int64(n))
	case reflect.Slice:
		var rules []RedactRule
		for _, p := range strings.Split(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				rules = append(rules, RedactRule{Preset: p})
			}
		}
		v.Set(reflect.ValueOf(rules))
	default:
		return fmt.Errorf("unsupported kind %s", v.Kind())
	}
	return nil
}
