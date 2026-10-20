// SPDX-License-Identifier: Apache-2.0

package semconv

import (
	"math"
	"strconv"
	"strings"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// maxExactFloat is the largest integer a float64 holds exactly.
const maxExactFloat = 1 << 53

// attrIndex maps attribute keys to values; the first occurrence of a key wins.
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

// intResult is the outcome of resolving an integer field.
type intResult struct {
	N       int64
	Key     string // accepted key the value came from
	Present bool   // some accepted key was present, parseable or not
	OK      bool   // a value parsed
}

// intField resolves an integer field: the first accepted key, in precedence
// order, whose value is an int, an integral double or a numeric string.
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

// strField resolves a string field: the first accepted key with a non-empty
// string (or integer) value.
func (ix attrIndex) strField(f Field) string {
	for _, k := range keysOf(f) {
		if s, ok := scalarString(ix[k]); ok {
			return s
		}
	}
	return ""
}

// str returns the non-empty string value of key.
func (ix attrIndex) str(key string) string {
	s, _ := scalarString(ix[key])
	return s
}

// scalarString returns a non-empty string, or the decimal form of an int.
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

// intValue converts an int, integral double or numeric string to a count.
// Negative or non-integral values are rejected.
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
