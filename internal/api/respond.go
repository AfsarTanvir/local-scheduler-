package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/AfsarTanvir/local-scheduler-/internal/scheduler"
	"github.com/AfsarTanvir/local-scheduler-/internal/store"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError sends every error in the same shape, so clients in any
// language can handle them the same way:
//
//	{"error": {"code": "not_found", "message": "job not found"}}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}

// writeStoreError turns an error from the store or scheduler into an HTTP error.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, scheduler.ErrAlreadyRunning):
		writeError(w, http.StatusConflict, "already_running", err.Error())
	case errors.Is(err, scheduler.ErrStopped):
		writeError(w, http.StatusServiceUnavailable, "shutting_down", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
	}
}
