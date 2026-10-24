// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"math"
	"strconv"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const maxExactFloat = 1 << 53

type attrIndex map[string]trace.Value

func newAttrIndex(attrs []trace.Attr) attrIndex {
	ix := make(attrIndex, len(attrs))
	for _, a := range attrs {
		if _, ok := ix[a.Key]; !ok {
			ix[a.Key] = a.Value
		}
	}
	return ix
}

type intResult struct {
	N       int64
	Key     string
	Present bool
	OK      bool
}

func (ix attrIndex) intField(f Field) intResult {
	var r intResult
	for _, k := range keysOf(f) {
		v, found := ix[k]
		if !found {
			continue
		}
		r.Present = true
		if n, ok := intValue(v); ok {
			r.N, r.Key, r.OK = n, k, true
			return r
		}
	}
	return r
}

func (ix attrIndex) strField(f Field) string {
	for _, k := range keysOf(f) {
		if s, ok := scalarString(ix[k]); ok {
			return s
		}
	}
	return ""
}

func (ix attrIndex) str(key string) string {
	s, _ := scalarString(ix[key])
	return s
}

func scalarString(v trace.Value) (string, bool) {
	switch v.Type {
	case trace.ValueString:
		return v.Str, v.Str != ""
	case trace.ValueInt:
		return strconv.FormatInt(v.Int, 10), true
	case trace.ValueEmpty, trace.ValueBool, trace.ValueDouble, trace.ValueBytes, trace.ValueArray, trace.ValueMap:
		return "", false
	default:
		return "", false
	}
}

func intValue(v trace.Value) (int64, bool) {
	switch v.Type {
	case trace.ValueInt:
		return v.Int, v.Int >= 0
	case trace.ValueDouble:
		return floatCount(v.Double)
	case trace.ValueString:
		s := strings.TrimSpace(v.Str)
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, n >= 0
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return floatCount(f)
	case trace.ValueEmpty, trace.ValueBool, trace.ValueBytes, trace.ValueArray, trace.ValueMap:
		return 0, false
	default:
		return 0, false
	}
}

func floatCount(f float64) (int64, bool) {
	if f < 0 || f > maxExactFloat || f != math.Trunc(f) {
		return 0, false
	}
	return int64(f), true
}
