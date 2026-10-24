// SPDX-License-Identifier: Apache-2.0

package main

import (
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maxBody = 32 << 20

var idKeys = map[string]bool{"traceId": true, "spanId": true, "parentSpanId": true}

var scrubbed = map[string]string{
	"host.name":               "host",
	"host.id":                 "host-id",
	"process.owner":           "user",
	"process.pid":             "1",
	"process.executable.path": "/usr/local/bin/node",
	"process.command":         "/repo/app",
	"process.command_args":    "/repo/app",
}

type capture struct {
	dir, prefix string
	mu          sync.Mutex
	seq         map[string]int
}

func objects(v any, fn func(map[string]any) error) error {
	switch x := v.(type) {
	case map[string]any:
		if err := fn(x); err != nil {
			return err
		}
		for _, e := range x {
			if err := objects(e, fn); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range x {
			if err := objects(e, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

func convertIDs(doc any, enc func(string) (string, error)) error {
	return objects(doc, func(m map[string]any) error {
		for k := range idKeys {
			s, ok := m[k].(string)
			if !ok {
				continue
			}
			conv, err := enc(s)
			if err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
			m[k] = conv
		}
		return nil
	})
}

func base64ToHex(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	return hex.EncodeToString(b), err
}

func hexToBase64(s string) (string, error) {
	b, err := hex.DecodeString(s)
	return base64.StdEncoding.EncodeToString(b), err
}

func scrub(doc any) {
	wd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	clean := func(s string) string {
		if wd != "" {
			s = strings.ReplaceAll(s, wd, "/repo")
		}
		if home != "" {
			s = strings.ReplaceAll(s, home, "/home/user")
		}
		return s
	}
	_ = objects(doc, func(m map[string]any) error {
		if key, ok := m["key"].(string); ok {
			if repl, ok := scrubbed[key]; ok {
				m["value"] = map[string]any{"stringValue": repl}
				return nil
			}
		}
		for k, e := range m {
			if s, ok := e.(string); ok {
				m[k] = clean(s)
			}
		}
		return nil
	})
}

func normalise(m proto.Message) ([]byte, error) {
	raw, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var doc any
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if err = convertIDs(doc, base64ToHex); err != nil {
		return nil, err
	}
	scrub(doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func (c *capture) save(signal string, m proto.Message) error {
	out, err := normalise(m)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq[signal]++
	name := filepath.Join(c.dir, fmt.Sprintf("%s%s-%d.json", c.prefix, signal, c.seq[signal]))
	return os.WriteFile(name, out, 0o600)
}

func decode(r *http.Request, m proto.Message) error {
	var body io.Reader = http.MaxBytesReader(nil, r.Body, maxBody)
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(body)
		if err != nil {
			return err
		}
		body = zr
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if r.Header.Get("Content-Type") != "application/json" {
		return proto.Unmarshal(data, m)
	}
	var doc any
	if err = json.Unmarshal(data, &doc); err != nil {
		return err
	}
	if err = convertIDs(doc, hexToBase64); err != nil {
		return err
	}
	if data, err = json.Marshal(doc); err != nil {
		return err
	}
	return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, m)
}

func (c *capture) handler(signal string, newReq func() proto.Message, resp proto.Message, keep bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := newReq()
		if err := decode(r, req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if keep {
			if err := c.save(signal, req); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if r.Header.Get("Content-Type") == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("{}"))
			return
		}
		out, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		_, _ = w.Write(out)
	}
}

func (c *capture) mux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/traces", c.handler("traces",
		func() proto.Message { return &coltrace.ExportTraceServiceRequest{} },
		&coltrace.ExportTraceServiceResponse{}, true))
	mux.HandleFunc("POST /v1/logs", c.handler("logs",
		func() proto.Message { return &collogs.ExportLogsServiceRequest{} },
		&collogs.ExportLogsServiceResponse{}, true))
	mux.HandleFunc("POST /v1/metrics", c.handler("metrics",
		func() proto.Message { return &colmetrics.ExportMetricsServiceRequest{} },
		&colmetrics.ExportMetricsServiceResponse{}, false))
	return mux
}

func run() error {
	dir := flag.String("dir", "", "output directory (created if missing)")
	prefix := flag.String("prefix", "", "file name prefix")
	addr := flag.String("addr", "127.0.0.1:14318", "listen address")
	flag.Parse()
	if *dir == "" {
		return errors.New("-dir is required")
	}
	if err := os.MkdirAll(*dir, 0o750); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", *addr)
	if err != nil {
		return err
	}
	c := &capture{dir: *dir, prefix: *prefix, seq: map[string]int{}}
	srv := &http.Server{
		Handler:           c.mux(),
		ReadHeaderTimeout: 5 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("otlpcapture listening on %s, writing to %s", ln.Addr(), *dir)
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("otlpcapture: %v", err)
	}
}
