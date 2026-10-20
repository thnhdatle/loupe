// SPDX-License-Identifier: Apache-2.0

package trace

// ValueType is the type of an OTLP AnyValue.
type ValueType uint8

// Value types, mirroring OTLP AnyValue.
const (
	ValueEmpty ValueType = iota
	ValueString
	ValueBool
	ValueInt
	ValueDouble
	ValueBytes
	ValueArray
	ValueMap
)

// Value is a decoded OTLP AnyValue. Only the field selected by Type is meaningful.
type Value struct {
	Type   ValueType
	Str    string
	Bool   bool
	Int    int64
	Double float64
	Bytes  []byte
	Array  []Value
	Map    []Attr
}

// StringValue returns a string Value.
func StringValue(s string) Value { return Value{Type: ValueString, Str: s} }

// IntValue returns an int Value.
func IntValue(i int64) Value { return Value{Type: ValueInt, Int: i} }

// DoubleValue returns a double Value.
func DoubleValue(f float64) Value { return Value{Type: ValueDouble, Double: f} }

// BoolValue returns a bool Value.
func BoolValue(b bool) Value { return Value{Type: ValueBool, Bool: b} }

// ArrayValue returns an array Value.
func ArrayValue(vs ...Value) Value { return Value{Type: ValueArray, Array: vs} }

// MapValue returns a key-value list Value.
func MapValue(kvs ...Attr) Value { return Value{Type: ValueMap, Map: kvs} }

// Attr is one OTLP KeyValue.
type Attr struct {
	Key   string
	Value Value
}

// RawResource is an OTLP resource as decoded by the receiver.
type RawResource struct {
	Attributes []Attr
}

// RawEvent is an OTLP span event.
type RawEvent struct {
	Name       string
	TimeNs     int64
	Attributes []Attr
}

// OTLP span status codes.
const (
	StatusCodeUnset int32 = 0
	StatusCodeOK    int32 = 1
	StatusCodeError int32 = 2
)

// RawSpan is an OTLP span as decoded by the receiver, before semantic translation.
type RawSpan struct {
	Resource      *RawResource // shared by all spans of one ResourceSpans
	TraceID       ID
	SpanID        SpanID
	ParentSpanID  SpanID
	Name          string
	SpanKind      int32 // OTLP SpanKind; informational only, never decides Kind
	StartNs       int64
	EndNs         int64
	StatusCode    int32
	StatusMessage string
	Attributes    []Attr
	Events        []RawEvent
}

// RawLogRecord is an OTLP log record as decoded by the receiver.
type RawLogRecord struct {
	Resource   *RawResource
	TraceID    ID // zero when the record has no span context
	SpanID     SpanID
	TimeNs     int64
	EventName  string // LogRecord.event_name; may be empty
	Body       Value
	Attributes []Attr
}
