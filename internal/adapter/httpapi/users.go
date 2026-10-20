// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"

	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

func (h *Handler) deleteUserData(w http.ResponseWriter, r *http.Request) {
	if h.deleter == nil {
		httpmw.WriteError(w, r, http.StatusNotFound, CodeNotFound, "user deletion is not available")
		return
	}
	if _, err := newParams(r.URL.Query()); err != nil {
		h.fail(w, r, err)
		return
	}
	id, err := h.deleter.DeleteUser(r.PathValue("user_id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpmw.WriteJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
}

func (h *Handler) version(w http.ResponseWriter, _ *http.Request) {
	httpmw.WriteJSON(w, http.StatusOK, h.opts.Version)
}
