// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"errors"
	"fmt"
)

// Price parsing errors (ParsePrice).
var (
	// ErrInvalidPrice reports price text that is not a plain decimal number
	// (digits, optionally a dot and more digits).
	ErrInvalidPrice = errors.New("cost: invalid price")
	// ErrNegativePrice reports a price below zero.
	ErrNegativePrice = errors.New("cost: negative price")
	// ErrTooManyDecimals reports a price with more than 9 fractional digits
	// (finer than one nano-USD per million tokens).
	ErrTooManyDecimals = errors.New("cost: price has more than 9 fractional digits")
	// ErrPriceOverflow reports a price whose nano-USD value does not fit in an int64.
	ErrPriceOverflow = errors.New("cost: price overflows int64 nano-USD")
)

// Price table errors (NewPriceTable), wrapped in an *EntryError.
var (
	// ErrInvalidMatch reports a match kind other than exact or glob.
	ErrInvalidMatch = errors.New("cost: invalid match kind")
	// ErrEmptyModel reports an entry without a model or pattern.
	ErrEmptyModel = errors.New("cost: empty model")
	// ErrMissingEffectiveFrom reports an entry with a zero effective_from.
	ErrMissingEffectiveFrom = errors.New("cost: missing effective_from")
	// ErrMissingProvenance reports a default entry without source or as_of.
	ErrMissingProvenance = errors.New("cost: default entry lacks source or as_of")
	// ErrDuplicateVersion reports two entries of one series with the same effective_from.
	ErrDuplicateVersion = errors.New("cost: duplicate effective_from in series")
)

// Calculation errors (Price.Cost, Calculator.Cost).
var (
	// ErrNegativeUsage reports a negative token count.
	ErrNegativeUsage = errors.New("cost: negative token count")
	// ErrCostOverflow reports a cost that does not fit in an int64 nano-USD.
	ErrCostOverflow = errors.New("cost: cost overflows int64 nano-USD")
	// ErrNilTable reports a Calculator built without a price table.
	ErrNilTable = errors.New("cost: nil price table")
)

// EntryError reports an invalid price entry passed to NewPriceTable.
type EntryError struct {
	Origin Origin
	Index  int // position in the defaults or user slice
	Model  string
	Err    error
}

// Error implements error.
func (e *EntryError) Error() string {
	return fmt.Sprintf("cost: %s price entry %d (%q): %v", e.Origin, e.Index, e.Model, e.Err)
}

// Unwrap returns the underlying sentinel error.
func (e *EntryError) Unwrap() error { return e.Err }
