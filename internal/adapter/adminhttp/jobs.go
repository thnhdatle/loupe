// SPDX-License-Identifier: Apache-2.0

package adminhttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/thnhdatle/loupe/internal/app/admin"
	"github.com/thnhdatle/loupe/internal/platform/httpmw"
)

const keyJobID = "job_id"

type jobView struct {
	JobID      string  `json:"job_id"`
	Kind       string  `json:"kind"`
	State      string  `json:"state"`
	Rows       int     `json:"rows"`
	Error      string  `json:"error,omitempty"`
	CreatedAt  string  `json:"created_at"`
	StartedAt  *string `json:"started_at"`
	FinishedAt *string `json:"finished_at"`
}

func newJobView(st admin.JobStatus) jobView {
	opt := func(t time.Time) *string {
		if t.IsZero() {
			return nil
		}
		s := t.UTC().Format(time.RFC3339Nano)
		return &s
	}
	return jobView{
		JobID: st.ID, Kind: string(st.Kind), State: string(st.State), Rows: st.Rows, Error: st.Error,
		CreatedAt: st.CreatedAt.UTC().Format(time.RFC3339Nano), StartedAt: opt(st.StartedAt), FinishedAt: opt(st.FinishedAt),
	}
}

type recomputeRequest struct {
	Since *time.Time `json:"since"`
	Until *time.Time `json:"until"`
}

func (h *Handler) recompute(w http.ResponseWriter, r *http.Request) {
	var req recomputeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpmw.WriteError(w, r, http.StatusRequestEntityTooLarge, CodeBodyTooLarge, "request body too large")
			return
		}
		httpmw.WriteError(w, r, http.StatusBadRequest, CodeInvalidParameter,
			"body must be a JSON object with optional RFC 3339 since and until")
		return
	}
	calc, err := h.opts.Calculator()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var rng admin.Range
	if req.Since != nil {
		rng.Since = *req.Since
	}
	if req.Until != nil {
		rng.Until = *req.Until
	}
	id, err := h.svc.Recompute(calc, rng)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpmw.WriteJSON(w, http.StatusAccepted, map[string]string{keyJobID: id})
}

func (h *Handler) vacuum(w http.ResponseWriter, r *http.Request) {
	id, err := h.svc.Vacuum()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpmw.WriteJSON(w, http.StatusAccepted, map[string]string{keyJobID: id})
}

func (h *Handler) backup(w http.ResponseWriter, r *http.Request) {
	path := admin.BackupPath(h.opts.StorageDir, h.clk.Now())
	id, err := h.svc.Backup(path)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpmw.WriteJSON(w, http.StatusAccepted, map[string]string{keyJobID: id, "path": path})
}

func (h *Handler) job(w http.ResponseWriter, r *http.Request) {
	st, err := h.svc.JobStatus(r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpmw.WriteJSON(w, http.StatusOK, newJobView(st))
}
