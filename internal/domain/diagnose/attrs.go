// SPDX-License-Identifier: Apache-2.0

package diagnose

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/query"
)

const maxTextRunes = 200

var (
	messageKeys    = []string{"error.message", "exception.message", "otel.status_description"}
	statusCodeKeys = []string{"http.response.status_code", "http.status_code"}
)

func attributes(s *query.SpanView) map[string]any {
	var m map[string]any
	if len(s.Attributes) == 0 || json.Unmarshal(s.Attributes, &m) != nil {
		return nil
	}
	return m
}

func errorText(s *query.SpanView) string {
	attrs := attributes(s)
	var parts []string
	if s.ErrorType != "" {
		parts = append(parts, s.ErrorType)
	}
	for _, k := range messageKeys {
		if v, ok := attrs[k].(string); ok && v != "" {
			parts = append(parts, v)
			break
		}
	}
	if code := statusCode(attrs); code >= 400 {
		parts = append(parts, "HTTP "+strconv.Itoa(code))
	}
	return truncate(strings.Join(parts, ", "), maxTextRunes)
}

func statusCode(attrs map[string]any) int {
	for _, k := range statusCodeKeys {
		switch v := attrs[k].(type) {
		case float64:
			return int(v)
		case string:
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func quote(name string) string {
	if name == "" {
		return "(unnamed)"
	}
	return fmt.Sprintf("%q", truncate(name, maxTextRunes))
}
