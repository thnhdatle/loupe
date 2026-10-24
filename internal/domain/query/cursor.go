// SPDX-License-Identifier: Apache-2.0

package query

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/thnhdatle/loupe/internal/domain/trace"
)

const cursorVersion = 1

type Cursor struct {
	SortKey int64
	ID      string
}

func TraceCursor(sortKey int64, id trace.ID) Cursor {
	return Cursor{SortKey: sortKey, ID: id.String()}
}

func (c Cursor) TraceID() (trace.ID, error) {
	id, err := trace.ParseID(c.ID)
	if err != nil {
		return trace.ID{}, fmt.Errorf("%w: %w", ErrInvalidCursor, err)
	}
	return id, nil
}

type cursorWire struct {
	V int    `json:"v"`
	S Sort   `json:"s"`
	O Order  `json:"o"`
	K int64  `json:"k"`
	I string `json:"i"`
}

func EncodeCursor(c Cursor, sort Sort, order Order) string {
	b, err := json.Marshal(cursorWire{V: cursorVersion, S: sort, O: order, K: c.SortKey, I: c.ID})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCursor(s string, sort Sort, order Order) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: encoding", ErrInvalidCursor)
	}
	var w cursorWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return Cursor{}, fmt.Errorf("%w: payload", ErrInvalidCursor)
	}
	if w.V != cursorVersion {
		return Cursor{}, fmt.Errorf("%w: version %d", ErrInvalidCursor, w.V)
	}
	if w.S != sort || w.O != order {
		return Cursor{}, fmt.Errorf("%w: issued for another sort or order", ErrInvalidCursor)
	}
	return Cursor{SortKey: w.K, ID: w.I}, nil
}
