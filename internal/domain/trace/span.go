// SPDX-License-Identifier: Apache-2.0

package trace

// Kind classifies a span.
type Kind string

// Span kinds.
const (
	KindLLM       Kind = "llm"
	KindEmbedding Kind = "embedding"
	KindTool      Kind = "tool"
	KindRetrieval Kind = "retrieval"
	KindRerank    Kind = "rerank"
	KindAgent     Kind = "agent"
	KindChain     Kind = "chain"
	KindGuardrail Kind = "guardrail"
	KindOther     Kind = "other"
)

// Kinds lists every kind in a stable order.
func Kinds() []Kind {
	return []Kind{
		KindLLM, KindEmbedding, KindTool, KindRetrieval, KindRerank,
		KindAgent, KindChain, KindGuardrail, KindOther,
	}
}

// IsValid reports whether k is one of the defined kinds.
func (k Kind) IsValid() bool {
	for _, v := range Kinds() {
		if k == v {
			return true
		}
	}
	return false
}

// HasUsageDedupe reports whether spans of this kind take part in the usage-duplicate rule.
func (k Kind) HasUsageDedupe() bool { return k == KindLLM || k == KindEmbedding }

// Status is the span status as stored.
type Status string

// Span statuses.
const (
	StatusUnset Status = "unset"
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// StatusFromCode maps an OTLP status code to a Status; unknown codes are unset.
func StatusFromCode(code int32) Status {
	switch code {
	case StatusCodeOK:
		return StatusOK
	case StatusCodeError:
		return StatusError
	default:
		return StatusUnset
	}
}

// Flags is the bit set stored in spans.flags.
type Flags uint32

// Span flags.
const (
	// FlagAttrTruncated: a non-content attribute value was cut to receiver.max_attribute_bytes.
	FlagAttrTruncated Flags = 1 << iota
	// FlagUsageDuplicate: the span's usage duplicates its nearest same-kind ancestor's.
	FlagUsageDuplicate
	// FlagUsageClamped: a max(0, …) clamp applied in the cost formula.
	FlagUsageClamped
)

// Has reports whether every bit of f2 is set in f.
func (f Flags) Has(f2 Flags) bool { return f&f2 == f2 }

// Usage holds token counts with inclusive semantics: Input includes cache-read and
// cache-write tokens, Output includes reasoning tokens.
type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Reasoning  int64 `json:"reasoning"`
}

// IsZero reports whether every count is zero.
func (u Usage) IsZero() bool { return u == Usage{} }

// Add returns the field-wise sum of u and v.
func (u Usage) Add(v Usage) Usage {
	return Usage{
		Input:      u.Input + v.Input,
		Output:     u.Output + v.Output,
		CacheRead:  u.CacheRead + v.CacheRead,
		CacheWrite: u.CacheWrite + v.CacheWrite,
		Reasoning:  u.Reasoning + v.Reasoning,
	}
}

// Sub returns the field-wise difference u − v.
func (u Usage) Sub(v Usage) Usage {
	return Usage{
		Input:      u.Input - v.Input,
		Output:     u.Output - v.Output,
		CacheRead:  u.CacheRead - v.CacheRead,
		CacheWrite: u.CacheWrite - v.CacheWrite,
		Reasoning:  u.Reasoning - v.Reasoning,
	}
}

// Cost is a span's cost: Nanos is meaningful only when Priced is true
// (stored as NULL otherwise).
type Cost struct {
	Nanos  int64
	Priced bool
}

// Priced returns a priced Cost of n nano-USD.
func Priced(n int64) Cost { return Cost{Nanos: n, Priced: true} }

// Resource is a stored resource: ID is a hash of the canonical attributes JSON.
type Resource struct {
	ID         uint64
	Attributes []byte // canonical JSON object
}

// Span is a translated span ready for storage.
type Span struct {
	TraceID      ID
	SpanID       SpanID
	ParentSpanID SpanID
	ResourceID   uint64
	Service      string
	Name         string
	Kind         Kind
	Status       Status
	StartNs      int64
	EndNs        int64
	Model        string // pricing model: response model, else request model
	Provider     string
	Usage        Usage
	HasUsage     bool // at least one usage key was present
	Cost         Cost
	SessionID    string
	UserID       string
	ErrorType    string
	Flags        Flags
	Attributes   []byte // non-content attributes as a JSON object, serialised once
	Content      []ContentPart
	// ContentStripped reports that content arrived and was not stored as text
	// (capture off or metadata_only).
	ContentStripped bool
}

// DurationNs returns EndNs − StartNs, or 0 when the span ends before it starts.
func (s *Span) DurationNs() int64 {
	if s.EndNs < s.StartNs {
		return 0
	}
	return s.EndNs - s.StartNs
}

// IsRoot reports whether the span has no parent.
func (s *Span) IsRoot() bool { return s.ParentSpanID.IsZero() }
