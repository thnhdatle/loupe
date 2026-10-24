// SPDX-License-Identifier: Apache-2.0

package trace

type Kind string

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

func Kinds() []Kind {
	return []Kind{
		KindLLM, KindEmbedding, KindTool, KindRetrieval, KindRerank,
		KindAgent, KindChain, KindGuardrail, KindOther,
	}
}

func (k Kind) IsValid() bool {
	for _, v := range Kinds() {
		if k == v {
			return true
		}
	}
	return false
}

func (k Kind) HasUsageDedupe() bool { return k == KindLLM || k == KindEmbedding }

type Status string

const (
	StatusUnset Status = "unset"
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

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

type Flags uint32

const (
	FlagAttrTruncated Flags = 1 << iota
	FlagUsageDuplicate
	FlagUsageClamped
)

func (f Flags) Has(f2 Flags) bool { return f&f2 == f2 }

type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Reasoning  int64 `json:"reasoning"`
}

func (u Usage) IsZero() bool { return u == Usage{} }

func (u Usage) Add(v Usage) Usage {
	return Usage{
		Input:      u.Input + v.Input,
		Output:     u.Output + v.Output,
		CacheRead:  u.CacheRead + v.CacheRead,
		CacheWrite: u.CacheWrite + v.CacheWrite,
		Reasoning:  u.Reasoning + v.Reasoning,
	}
}

func (u Usage) Sub(v Usage) Usage {
	return Usage{
		Input:      u.Input - v.Input,
		Output:     u.Output - v.Output,
		CacheRead:  u.CacheRead - v.CacheRead,
		CacheWrite: u.CacheWrite - v.CacheWrite,
		Reasoning:  u.Reasoning - v.Reasoning,
	}
}

type Cost struct {
	Nanos  int64
	Priced bool
}

func Priced(n int64) Cost { return Cost{Nanos: n, Priced: true} }

type Resource struct {
	ID         uint64
	Attributes []byte
}

// Span is a translated span ready for storage.
type Span struct {
	TraceID         ID
	SpanID          SpanID
	ParentSpanID    SpanID
	ResourceID      uint64
	Service         string
	Name            string
	Kind            Kind
	Status          Status
	StartNs         int64
	EndNs           int64
	Model           string
	Provider        string
	Usage           Usage
	HasUsage        bool
	Cost            Cost
	SessionID       string
	UserID          string
	ErrorType       string
	Flags           Flags
	Attributes      []byte
	Content         []ContentPart
	ContentStripped bool
}

func (s *Span) DurationNs() int64 {
	if s.EndNs < s.StartNs {
		return 0
	}
	return s.EndNs - s.StartNs
}

func (s *Span) IsRoot() bool { return s.ParentSpanID.IsZero() }
