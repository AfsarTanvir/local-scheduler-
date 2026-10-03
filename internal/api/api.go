// Package api is the HTTP + JSON interface of the scheduler. Anything
// that can send an HTTP request (curl, Python, Node, Java, .NET, PHP, ...)
// can create and manage jobs through it.
package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
	"github.com/AfsarTanvir/local-scheduler-/internal/store"
)

// maxBodyBytes limits the size of a request body.
const maxBodyBytes = 1 << 20 // 1 MB

type Server struct {
	store      *store.Store
	allowShell bool // shell jobs run commands on this machine, so they are opt-in
}

func New(st *store.Store, allowShell bool) *Server {
	return &Server{store: st, allowShell: allowShell}
}

// Handler returns all routes. Method and {id} patterns need Go 1.22+.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /jobs", s.createJob)
	mux.HandleFunc("GET /jobs", s.listJobs)
	mux.HandleFunc("GET /jobs/{id}", s.getJob)
	mux.HandleFunc("DELETE /jobs/{id}", s.deleteJob)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) createJob(w http.ResponseWriter, r *http.Request) {
	var spec job.Spec
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields() // a typo like "shedule" is an error, not silently ignored
	if err := dec.Decode(&spec); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	if spec.Type == job.TypeShell && !s.allowShell {
		writeError(w, http.StatusForbidden, "shell_disabled",
			"shell jobs are disabled; start the server with ALLOW_SHELL_JOBS=true to enable them")
		return
	}

	j, err := job.New(spec, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_job", err.Error())
		return
	}
	s.store.Add(j)

	w.Header().Set("Location", "/jobs/"+j.ID)
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.store.List())
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
