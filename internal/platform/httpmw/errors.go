// SPDX-License-Identifier: Apache-2.0

package httpmw

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
)

// ErrorBody is the JSON error envelope (docs/guides/errors.md).
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the content of the error envelope.
type ErrorDetail struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

// WriteJSON writes v as a JSON response with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		WriteErrorBody(w, "", http.StatusInternalServerError, "internal", "encode response")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

// WriteError writes the error envelope for r with a stable code.
func WriteError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	WriteErrorBody(w, RequestIDFrom(r.Context()), status, code, message)
}

// WriteErrorBody writes the error envelope with an explicit request id.
func WriteErrorBody(w http.ResponseWriter, requestID string, status int, code, message string) {
	b, _ := json.Marshal(ErrorBody{Error: ErrorDetail{Code: code, Message: message, RequestID: requestID}})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

// Recover turns a handler panic into a logged 500 with the error envelope.
// A nil logger discards the log line.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer handlePanic(w, r, log)
			next.ServeHTTP(w, r)
		})
	}
}

// handlePanic is deferred directly so that recover sees the panic.
func handlePanic(w http.ResponseWriter, r *http.Request, log *slog.Logger) {
	rec := recover()
	if rec == nil {
		return
	}
	if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
		panic(rec)
	}
	if log != nil {
		log.ErrorContext(r.Context(), "http handler panic", "request_id", RequestIDFrom(r.Context()),
			"panic", rec, "stack", string(debug.Stack()))
	}
	WriteError(w, r, http.StatusInternalServerError, "internal", "internal error")
}
