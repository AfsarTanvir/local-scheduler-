// Package api is the HTTP + JSON interface of the scheduler. Anything
// that can send an HTTP request (curl, Python, Node, Java, .NET, PHP, ...)
// can create and manage jobs through it.
package api

import (
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
	"github.com/AfsarTanvir/local-scheduler-/internal/scheduler"
	"github.com/AfsarTanvir/local-scheduler-/internal/store"
)

// openAPISpec describes the whole API. It is compiled into the binary.
//
//go:embed openapi.yaml
var openAPISpec []byte

// maxBodyBytes limits the size of a request body.
const maxBodyBytes = 1 << 20 // 1 MB

type Server struct {
	store      *store.Store
	sched      *scheduler.Scheduler
	allowShell bool   // shell jobs run commands on this machine, so they are opt-in
	apiKey     string // when set, every route except /health and /openapi.yaml needs it
}

// New returns the API. An empty apiKey leaves the API open, which is fine
// when only this machine can reach it.
func New(st *store.Store, sched *scheduler.Scheduler, allowShell bool, apiKey string) *Server {
	return &Server{store: st, sched: sched, allowShell: allowShell, apiKey: apiKey}
}

// Handler returns all routes. Method and {id} patterns need Go 1.22+.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Public: health checks (Docker, load balancers) and the API description.
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /openapi.yaml", s.openAPI)

	mux.HandleFunc("POST /jobs", s.requireKey(s.createJob))
	mux.HandleFunc("GET /jobs", s.requireKey(s.listJobs))
	mux.HandleFunc("GET /jobs/{id}", s.requireKey(s.getJob))
	mux.HandleFunc("GET /jobs/{id}/runs", s.requireKey(s.listRuns))
	mux.HandleFunc("DELETE /jobs/{id}", s.requireKey(s.deleteJob))
	mux.HandleFunc("POST /jobs/{id}/run", s.requireKey(s.runJob))
	mux.HandleFunc("POST /jobs/{id}/pause", s.requireKey(s.pauseJob))
	mux.HandleFunc("POST /jobs/{id}/resume", s.requireKey(s.resumeJob))
	return mux
}

// requireKey lets a request through only if it sends "Authorization: Bearer <apiKey>".
// With no key configured, every request is let through.
func (s *Server) requireKey(next http.HandlerFunc) http.HandlerFunc {
	if s.apiKey == "" {
		return next
	}
	// Compare hashes, not the strings: equal-length inputs make the
	// constant-time compare hide the key's length too.
	want := sha256.Sum256([]byte(s.apiKey))
	return func(w http.ResponseWriter, r *http.Request) {
		// The scheme name is case-insensitive (RFC 7235): "bearer" works too.
		scheme, key, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		got := sha256.Sum256([]byte(key))
		if !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="local-scheduler"`)
			writeError(w, http.StatusUnauthorized, "unauthorized",
				"missing or wrong API key; send the header Authorization: Bearer <API_KEY>")
			return
		}
		next(w, r)
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// openAPI serves the API description. Tools like Swagger UI, Postman or
// OpenAPI Generator can read it to show docs or generate clients.
func (s *Server) openAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.Write(openAPISpec)
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
	if err := s.store.Add(j); err != nil {
		writeStoreError(w, err)
		return
	}

	w.Header().Set("Location", "/jobs/"+j.ID)
	writeJSON(w, http.StatusCreated, j)
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.store.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

// listRuns returns the job's run history, newest first.
// ?limit=N (1 to 100, default 20) sets how many runs are returned.
func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be a number from 1 to 100")
			return
		}
		limit = n
	}

	runs, err := s.store.Runs(r.PathValue("id"), limit)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) deleteJob(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runJob starts a job now. The run happens in the background, so the
// response is 202 Accepted; poll GET /jobs/{id} to see lastRun.
func (s *Server) runJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.sched.RunNow(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}

func (s *Server) pauseJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.sched.Pause(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) resumeJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.sched.Resume(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, j)
}
