// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidPrice    = errors.New("cost: invalid price")
	ErrNegativePrice   = errors.New("cost: negative price")
	ErrTooManyDecimals = errors.New("cost: price has more than 9 fractional digits")
	ErrPriceOverflow   = errors.New("cost: price overflows int64 nano-USD")
)

var (
	ErrInvalidMatch         = errors.New("cost: invalid match kind")
	ErrEmptyModel           = errors.New("cost: empty model")
	ErrMissingEffectiveFrom = errors.New("cost: missing effective_from")
	ErrMissingProvenance    = errors.New("cost: default entry lacks source or as_of")
	ErrDuplicateVersion     = errors.New("cost: duplicate effective_from in series")
)

var (
	ErrNegativeUsage = errors.New("cost: negative token count")
	ErrCostOverflow  = errors.New("cost: cost overflows int64 nano-USD")
	ErrNilTable      = errors.New("cost: nil price table")
)

type EntryError struct {
	Origin Origin
	Index  int
	Model  string
	Err    error
}

func (e *EntryError) Error() string {
	return fmt.Sprintf("cost: %s price entry %d (%q): %v", e.Origin, e.Index, e.Model, e.Err)
}

func (e *EntryError) Unwrap() error { return e.Err }
