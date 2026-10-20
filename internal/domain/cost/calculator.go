// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Calculator prices spans against a PriceTable. It is immutable and safe for
// concurrent use; a price reload builds a new Calculator.
type Calculator struct {
	table *PriceTable
}

// NewCalculator returns a Calculator over table. It returns ErrNilTable when
// table is nil.
func NewCalculator(table *PriceTable) (*Calculator, error) {
	if table == nil {
		return nil, ErrNilTable
	}
	return &Calculator{table: table}, nil
}

// Input is what the Calculator needs to know about one span.
type Input struct {
	Provider      string
	ResponseModel string
	RequestModel  string
	Start         time.Time // span start; selects the price version
	Usage         trace.Usage
}

// Result is the cost of one span.
type Result struct {
	// Model is the pricing model (PricingModel of the input), also stored in
	// the span's model column.
	Model string
	// Cost is priced when an entry matched; a priced span without usage costs 0.
	Cost trace.Cost
	// Flags holds trace.FlagUsageClamped when the formula clamped; never other bits.
	Flags trace.Flags
	// Price is the version used; the zero Price when unpriced.
	Price Price
}

// PricingModel returns the model a span is priced by: the response model,
// or the request model when the response model is empty. A non-empty but
// unpriced response model does not fall back to a priced request model,
// because the response model names what actually served the call.
func PricingModel(responseModel, requestModel string) string {
	if responseModel != "" {
		return responseModel
	}
	return requestModel
}

// Cost prices one span. An unmatched model (including an empty one) yields an
// unpriced Result and a nil error; whether the span counts as unpriced in
// metrics (it has tokens) is the caller's decision. Errors are those of
// Price.Cost: ErrNegativeUsage and ErrCostOverflow.
func (c *Calculator) Cost(in Input) (Result, error) {
	model := PricingModel(in.ResponseModel, in.RequestModel)
	res := Result{Model: model}
	p, ok := c.table.Lookup(in.Provider, model, in.Start)
	if !ok {
		return res, nil
	}
	nanos, flags, err := p.Cost(in.Usage)
	if err != nil {
		return res, fmt.Errorf("cost: price model %q: %w", model, err)
	}
	res.Cost, res.Flags, res.Price = trace.Priced(nanos), flags, p
	return res, nil
}
