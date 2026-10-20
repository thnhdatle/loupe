// SPDX-License-Identifier: Apache-2.0

package adminhttp_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/testutil/openapispec"
)

// TestOpenAPI_Contract drives every admin operation of api/openapi.yaml with
// its request example and checks the response against the success example.
// Route-vs-spec coverage lives in the httpapi tests, which see both handlers.
func TestOpenAPI_Contract(t *testing.T) {
	t.Parallel()
	for _, op := range openapispec.Load(t).Operations() {
		if !strings.HasPrefix(op.Path, "/v1/admin/") {
			continue
		}
		t.Run(op.ID, func(t *testing.T) {
			t.Parallel()
			var status string
			for _, st := range op.Statuses() {
				if strings.HasPrefix(st, "2") {
					status = st
				}
			}
			mt, ex := op.Response(status)
			require.NotNil(t, ex)
			body := ""
			if rq, ok := op.RequestExample(); ok {
				b, err := json.Marshal(rq)
				require.NoError(t, err)
				body = string(b)
			}
			f := &fakeAdmin{status: admin.JobStatus{
				ID: "01J9Z3K4M5N6P7Q8R9S0T1V2W3", Kind: admin.KindVacuum, State: admin.StateDone, Rows: 128,
				CreatedAt:  time.Date(2026, 10, 19, 8, 30, 15, 0, time.UTC),
				StartedAt:  time.Date(2026, 10, 19, 8, 30, 16, 0, time.UTC),
				FinishedAt: time.Date(2026, 10, 19, 8, 30, 17, 0, time.UTC),
			}}
			rec := do(t, newHandler(t, f, opts()), op.Method, op.Target(), body)
			require.Equal(t, status, itoa(rec.Code), rec.Body.String())
			assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), mt))
			var got any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			assert.Empty(t, openapispec.SameShape("$", ex, got))
			if op.ID == "adminGetJob" {
				want, err := json.Marshal(ex)
				require.NoError(t, err)
				assert.JSONEq(t, string(want), rec.Body.String())
			}
		})
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
