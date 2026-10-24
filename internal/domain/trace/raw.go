// SPDX-License-Identifier: Apache-2.0

package trace

type ValueType uint8

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

// Value is a decoded OTLP AnyValue.
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

func StringValue(s string) Value { return Value{Type: ValueString, Str: s} }

func IntValue(i int64) Value { return Value{Type: ValueInt, Int: i} }

func DoubleValue(f float64) Value { return Value{Type: ValueDouble, Double: f} }

func BoolValue(b bool) Value { return Value{Type: ValueBool, Bool: b} }

func ArrayValue(vs ...Value) Value { return Value{Type: ValueArray, Array: vs} }

func MapValue(kvs ...Attr) Value { return Value{Type: ValueMap, Map: kvs} }

type Attr struct {
	Key   string
	Value Value
}

type RawResource struct {
	Attributes []Attr
}

type RawEvent struct {
	Name       string
	TimeNs     int64
	Attributes []Attr
}

const (
	StatusCodeUnset int32 = 0
	StatusCodeOK    int32 = 1
	StatusCodeError int32 = 2
)

type RawSpan struct {
	Resource      *RawResource
	TraceID       ID
	SpanID        SpanID
	ParentSpanID  SpanID
	Name          string
	SpanKind      int32
	StartNs       int64
	EndNs         int64
	StatusCode    int32
	StatusMessage string
	Attributes    []Attr
	Events        []RawEvent
}

type RawLogRecord struct {
	Resource   *RawResource
	TraceID    ID
	SpanID     SpanID
	TimeNs     int64
	EventName  string
	Body       Value
	Attributes []Attr
}
