// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"

	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

func (h *Handler) analyzeTrace(w http.ResponseWriter, r *http.Request) {
	a := h.opts.Analyzer
	if a == nil {
		httpmw.WriteError(w, r, http.StatusNotFound, CodeAnalyzeDisabled, "analyze is off: set analyze.base_url")
		return
	}
	if _, err := newParams(r.URL.Query()); err != nil {
		h.fail(w, r, err)
		return
	}
	httpmw.ExtendWriteDeadline(w, a.Timeout())
	rep, err := a.Analyze(r.Context(), r.PathValue("trace_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpmw.WriteJSON(w, http.StatusOK, rep)
}
