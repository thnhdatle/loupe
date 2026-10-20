// SPDX-License-Identifier: Apache-2.0

// Package openapispec loads api/openapi.yaml for route-vs-spec and contract
// tests. It is test support only.
//
// Usage:
//
//	spec := openapispec.Load(t)
//	for _, op := range spec.Operations() { _ = op.Route() }
package openapispec

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Spec is a parsed OpenAPI document.
type Spec struct {
	doc map[string]any
}

// Operation is one method on one path.
type Operation struct {
	Method string // upper case
	Path   string // OpenAPI path template, identical to the Go 1.22 route pattern path
	ID     string
	spec   *Spec
	raw    map[string]any
}

// Load reads api/openapi.yaml from the repository root (found from the test's directory).
func Load(tb testing.TB) *Spec {
	tb.Helper()
	dir, err := os.Getwd()
	require.NoError(tb, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			break
		}
		parent := filepath.Dir(dir)
		require.NotEqual(tb, parent, dir, "go.mod not found")
		dir = parent
	}
	b, err := os.ReadFile(filepath.Join(dir, "api", "openapi.yaml")) //nolint:gosec // fixed path under the repository
	require.NoError(tb, err)
	var doc map[string]any
	require.NoError(tb, yaml.Unmarshal(b, &doc))
	require.Equal(tb, "3.1.0", doc["openapi"])
	return &Spec{doc: doc}
}

// Operations returns every operation sorted by path then method.
func (s *Spec) Operations() []Operation {
	var out []Operation
	paths, _ := s.doc["paths"].(map[string]any)
	for path, item := range paths {
		methods, _ := item.(map[string]any)
		for m, v := range methods {
			raw, ok := v.(map[string]any)
			if !ok {
				continue
			}
			id, _ := raw["operationId"].(string)
			out = append(out, Operation{Method: strings.ToUpper(m), Path: path, ID: id, spec: s, raw: raw})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route() < out[j].Route() })
	return out
}

// Route returns "METHOD /path", the format of the handlers' Routes().
func (o Operation) Route() string { return o.Method + " " + o.Path }

// resolve follows a local "$ref".
func (s *Spec) resolve(v any) any {
	for {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		ref, ok := m["$ref"].(string)
		if !ok {
			return v
		}
		cur := any(s.doc)
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			cm, _ := cur.(map[string]any)
			cur = cm[part]
		}
		v = cur
	}
}

// Target returns the request target: path parameters substituted and every
// query parameter set to its example.
func (o Operation) Target() string {
	path := o.Path
	q := url.Values{}
	params, _ := o.raw["parameters"].([]any)
	for _, p := range params {
		pm, _ := o.spec.resolve(p).(map[string]any)
		name, _ := pm["name"].(string)
		ex, has := pm["example"]
		if !has {
			continue
		}
		val := fmt.Sprint(ex)
		switch pm["in"] {
		case "path":
			path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(val))
		case "query":
			q.Set(name, val)
		}
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// Response returns the media type and example of the response with the given
// status ("" and nil when it has none).
func (o Operation) Response(status string) (mediaType string, example any) {
	resps, _ := o.raw["responses"].(map[string]any)
	r, _ := o.spec.resolve(resps[status]).(map[string]any)
	content, _ := r["content"].(map[string]any)
	for mt, v := range content {
		m, _ := v.(map[string]any)
		return mt, m["example"]
	}
	return "", nil
}

// Statuses returns the documented response statuses, sorted.
func (o Operation) Statuses() []string {
	resps, _ := o.raw["responses"].(map[string]any)
	out := make([]string, 0, len(resps))
	for s := range resps {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// RequestExample returns the example JSON request body, if the operation has one.
func (o Operation) RequestExample() (any, bool) {
	rb, _ := o.spec.resolve(o.raw["requestBody"]).(map[string]any)
	content, _ := rb["content"].(map[string]any)
	for _, v := range content {
		m, _ := v.(map[string]any)
		ex, ok := m["example"]
		return ex, ok
	}
	return nil, false
}

// SameShape reports the first structural difference between an example and a
// response body: object key sets must be equal, and array elements are compared
// by their first element when both arrays are non-empty. Scalar values are not compared.
func SameShape(path string, want, got any) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path + ": want object"
		}
		return sameKeys(path, w, g)
	case []any:
		g, ok := got.([]any)
		if !ok {
			return path + ": want array"
		}
		if len(w) > 0 && len(g) > 0 {
			return SameShape(path+"[0]", w[0], g[0])
		}
	}
	return ""
}

func sameKeys(path string, want, got map[string]any) string {
	for _, k := range sortedKeys(want) {
		if _, ok := got[k]; !ok {
			return path + "." + k + ": missing"
		}
	}
	for _, k := range sortedKeys(got) {
		wv, ok := want[k]
		if !ok {
			return path + "." + k + ": not in the example"
		}
		if d := SameShape(path+"."+k, wv, got[k]); d != "" {
			return d
		}
	}
	return ""
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
