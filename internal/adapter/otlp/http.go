// SPDX-License-Identifier: Apache-2.0

package otlp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	collogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const (
	contentTypeProto = "application/x-protobuf"
	contentTypeJSON  = "application/json"
	retryAfterValue  = "1"
	maxHeaderBytes   = 64 << 10
)

// HTTPHandler returns the OTLP/HTTP handler.
func (s *Server) HTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/traces", exportHandler(s, trace.SignalTraces,
		func() *coltrace.ExportTraceServiceRequest { return &coltrace.ExportTraceServiceRequest{} },
		s.exportTraces))
	mux.HandleFunc("POST /v1/logs", exportHandler(s, trace.SignalLogs,
		func() *collogs.ExportLogsServiceRequest { return &collogs.ExportLogsServiceRequest{} },
		s.exportLogs))
	mux.HandleFunc("POST /v1/metrics", exportHandler(s, trace.SignalMetrics,
		func() *colmetrics.ExportMetricsServiceRequest { return &colmetrics.ExportMetricsServiceRequest{} },
		func(_ context.Context, req *colmetrics.ExportMetricsServiceRequest) (
			*colmetrics.ExportMetricsServiceResponse, error,
		) {
			return s.exportMetrics(req), nil
		}))
	return s.recoverMiddleware(mux)
}

func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				s.log.Error("panic in otlp http handler", "error", fmt.Sprint(p), "stack", string(debug.Stack()))
				writeStatus(w, encodingProto, http.StatusInternalServerError, codes.Internal, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type encoding int

const (
	encodingProto encoding = iota
	encodingJSON
)

func exportHandler[Req, Resp proto.Message](
	s *Server, sig trace.Signal, newReq func() Req, call func(context.Context, Req) (Resp, error),
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		enc, gz, herr := negotiate(r)
		if herr != nil {
			writeStatus(w, encodingProto, herr.status, herr.code, herr.msg)
			return
		}
		switch {
		case s.shutting.Load():
			writeStatus(w, enc, http.StatusServiceUnavailable, codes.Unavailable, "receiver is shutting down")
			return
		case !s.auth.check(r.Header.Get("Authorization"), r.Header.Get("X-Loupe-Token")):
			w.Header().Set("WWW-Authenticate", `Bearer realm="loupe"`)
			writeStatus(w, enc, http.StatusUnauthorized, codes.Unauthenticated, "missing or invalid token")
			return
		}
		body := http.MaxBytesReader(w, r.Body, s.cfg.MaxRequestBytes)
		data, release, err := s.readBody(body, gz)
		defer release()
		if err != nil {
			s.writeReadError(w, enc, sig, err)
			return
		}
		req := newReq()
		if derr := decodeRequest(enc, data, req); derr != nil {
			writeStatus(w, enc, http.StatusBadRequest, codes.InvalidArgument, derr.Error())
			return
		}
		resp, err := call(r.Context(), req)
		if err != nil {
			s.writeIngestError(w, enc, err)
			return
		}
		writeMessage(w, enc, http.StatusOK, resp)
	}
}

type httpError struct {
	status int
	code   codes.Code
	msg    string
}

func negotiate(r *http.Request) (enc encoding, gz bool, herr *httpError) {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch {
	case err == nil && mt == contentTypeProto:
		enc = encodingProto
	case err == nil && mt == contentTypeJSON:
		enc = encodingJSON
	default:
		return encodingProto, false, &httpError{
			http.StatusUnsupportedMediaType, codes.InvalidArgument,
			"content type must be application/x-protobuf or application/json",
		}
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
		return enc, false, nil
	case "gzip":
		return enc, true, nil
	default:
		return enc, false, &httpError{
			http.StatusUnsupportedMediaType, codes.InvalidArgument,
			"content encoding must be gzip or identity",
		}
	}
}

func decodeRequest(enc encoding, data []byte, m proto.Message) error {
	if enc == encodingJSON {
		return unmarshalJSON(data, m)
	}
	return unmarshalProto(data, m)
}

func (s *Server) writeReadError(w http.ResponseWriter, enc encoding, sig trace.Signal, err error) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, errInflight):
		s.count(sig, trace.ResultRejected, trace.ReasonInflight, 1)
		writeOverloaded(w, enc, "in-flight byte limit reached, retry later")
	case errors.Is(err, errBodyTooLarge), errors.As(err, &maxErr):
		s.count(sig, trace.ResultRejected, trace.ReasonRequestTooBig, 1)
		writeStatus(w, enc, http.StatusRequestEntityTooLarge, codes.InvalidArgument,
			fmt.Sprintf("request larger than %d bytes after decompression", s.cfg.MaxRequestBytes))
	default:
		writeStatus(w, enc, http.StatusBadRequest, codes.InvalidArgument, err.Error())
	}
}

func (s *Server) writeIngestError(w http.ResponseWriter, enc encoding, err error) {
	switch {
	case errors.Is(err, errTooManySpans):
		writeStatus(w, enc, http.StatusBadRequest, codes.InvalidArgument, err.Error())
	case isOverloaded(err):
		writeOverloaded(w, enc, "ingest queue is full, retry later")
	case errors.Is(err, trace.ErrShuttingDown):
		writeStatus(w, enc, http.StatusServiceUnavailable, codes.Unavailable, "receiver is shutting down")
	default:
		s.log.Error("ingest failed", "error", err)
		writeStatus(w, enc, http.StatusInternalServerError, codes.Internal, "internal error")
	}
}

func writeOverloaded(w http.ResponseWriter, enc encoding, msg string) {
	w.Header().Set("Retry-After", retryAfterValue)
	writeStatus(w, enc, http.StatusTooManyRequests, codes.ResourceExhausted, msg)
}

func writeStatus(w http.ResponseWriter, enc encoding, httpStatus int, code codes.Code, msg string) {
	writeMessage(w, enc, httpStatus, status.New(code, msg).Proto())
}

func writeMessage(w http.ResponseWriter, enc encoding, httpStatus int, m proto.Message) {
	var (
		b   []byte
		err error
		ct  = contentTypeProto
	)
	if enc == encodingJSON {
		ct = contentTypeJSON
		b, err = protojson.MarshalOptions{UseProtoNames: false}.Marshal(m)
	} else {
		b, err = proto.Marshal(m)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(httpStatus)
	_, _ = w.Write(b)
}

// RunHTTP serves OTLP/HTTP on ln until ctx ends.
func (s *Server) RunHTTP(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.HTTPHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    maxHeaderBytes,
		TLSConfig:         s.cfg.HTTPTLS,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	sctx, stop := context.WithCancel(ctx)
	defer stop()
	var g errgroup.Group
	g.Go(func() error {
		<-sctx.Done()
		s.BeginShutdown()
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(dctx); err != nil {
			_ = srv.Close()
			return fmt.Errorf("otlp http shutdown: %w", err)
		}
		return nil
	})
	var err error
	if s.cfg.HTTPTLS != nil {
		err = srv.ServeTLS(ln, "", "")
	} else {
		err = srv.Serve(ln)
	}
	stop()
	werr := g.Wait()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("otlp http serve: %w", err)
	}
	return werr //nolint:wrapcheck // wrapped in the goroutine
}
