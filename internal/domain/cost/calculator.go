// SPDX-License-Identifier: Apache-2.0

package cost

import (
	"fmt"
	"time"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

// Calculator prices spans against a PriceTable.
type Calculator struct {
	table *PriceTable
}

// NewCalculator returns a Calculator over table.
func NewCalculator(table *PriceTable) (*Calculator, error) {
	if table == nil {
		return nil, ErrNilTable
	}
	return &Calculator{table: table}, nil
}

type Input struct {
	Provider      string
	ResponseModel string
	RequestModel  string
	Start         time.Time
	Usage         trace.Usage
}

type Result struct {
	Model string
	Cost  trace.Cost
	Flags trace.Flags
	Price Price
}

func PricingModel(responseModel, requestModel string) string {
	if responseModel != "" {
		return responseModel
	}
	return requestModel
}

// Cost prices one span.
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
