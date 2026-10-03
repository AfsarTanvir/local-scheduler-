package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
	"github.com/AfsarTanvir/local-scheduler-/internal/scheduler"
	"github.com/AfsarTanvir/local-scheduler-/internal/store"
)

const validJob = `{
	"name": "backup",
	"schedule": "0 2 * * *",
	"type": "http",
	"http": {"url": "http://example.com/backup", "method": "POST"}
}`

// fakeRun pretends every run succeeds.
func fakeRun(ctx context.Context, j job.Job) job.Run {
	return job.Run{Status: job.StatusSuccess}
}

// newServer returns a server whose scheduler loop is not started,
// so jobs only run when the test asks for it.
func newServer(t *testing.T, allowShell bool) (http.Handler, *scheduler.Scheduler) {
	t.Helper()
	st := store.New()
	sched := scheduler.New(st, fakeRun)
	return New(st, sched, allowShell).Handler(), sched
}

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	h, _ := newServer(t, false)
	return h
}

// do sends a request and returns the response recorder.
func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// errorCode returns error.code from an error response.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error response is not JSON: %s", rec.Body)
	}
	return body.Error.Code
}

func TestHealth(t *testing.T) {
	rec := do(t, newTestServer(t), "GET", "/health", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestOpenAPISpec(t *testing.T) {
	rec := do(t, newTestServer(t), "GET", "/openapi.yaml", "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Body.String(), "openapi: 3") {
		t.Fatalf("got %d %.40s", rec.Code, rec.Body)
	}
}

func TestCreateGetListDelete(t *testing.T) {
	h := newTestServer(t)

	rec := do(t, h, "POST", "/jobs", validJob)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: got %d %s", rec.Code, rec.Body)
	}
	var created job.Job
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.ID == "" || !created.Enabled || created.NextRunAt == nil {
		t.Fatalf("unexpected created job: %s", rec.Body)
	}
	if loc := rec.Header().Get("Location"); loc != "/jobs/"+created.ID {
		t.Fatalf("Location = %q", loc)
	}

	if rec := do(t, h, "GET", "/jobs/"+created.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("get: got %d", rec.Code)
	}

	rec = do(t, h, "GET", "/jobs", "")
	var list []job.Job
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || len(list) != 1 {
		t.Fatalf("list: got %d %s", rec.Code, rec.Body)
	}

	if rec := do(t, h, "DELETE", "/jobs/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d", rec.Code)
	}
	if rec := do(t, h, "GET", "/jobs/"+created.ID, ""); rec.Code != http.StatusNotFound || errorCode(t, rec) != "not_found" {
		t.Fatalf("get after delete: got %d %s", rec.Code, rec.Body)
	}
}

func TestEmptyListIsArray(t *testing.T) {
	rec := do(t, newTestServer(t), "GET", "/jobs", "")
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("want [], got %s", rec.Body)
	}
}

func TestCreateErrors(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
		wantErr  string
	}{
		{"not json", `hello`, http.StatusBadRequest, "invalid_json"},
		{"unknown field", `{"name":"x","shedule":"* * * * *"}`, http.StatusBadRequest, "invalid_json"},
		{"invalid job", `{"name":"x","type":"http"}`, http.StatusBadRequest, "invalid_job"},
		{"shell disabled", `{"name":"x","schedule":"* * * * *","type":"shell","shell":{"command":"ls"}}`, http.StatusForbidden, "shell_disabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, newTestServer(t), "POST", "/jobs", tt.body)
			if rec.Code != tt.wantCode || errorCode(t, rec) != tt.wantErr {
				t.Fatalf("got %d %s, want %d %s", rec.Code, rec.Body, tt.wantCode, tt.wantErr)
			}
		})
	}
}

func TestShellAllowed(t *testing.T) {
	h, _ := newServer(t, true)
	rec := do(t, h, "POST", "/jobs", `{"name":"x","schedule":"* * * * *","type":"shell","shell":{"command":"ls"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestRunPauseResume(t *testing.T) {
	h, sched := newServer(t, false)
	rec := do(t, h, "POST", "/jobs", validJob)
	var created job.Job
	json.Unmarshal(rec.Body.Bytes(), &created)
	base := "/jobs/" + created.ID

	if rec := do(t, h, "POST", base+"/pause", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("pause: got %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", base+"/resume", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("resume: got %d %s", rec.Code, rec.Body)
	}

	if rec := do(t, h, "POST", base+"/run", ""); rec.Code != http.StatusAccepted {
		t.Fatalf("run: got %d %s", rec.Code, rec.Body)
	}
	sched.Stop(context.Background()) // waits for the run to finish

	rec = do(t, h, "GET", base, "")
	var got job.Job
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.LastRun == nil || got.LastRun.Status != job.StatusSuccess {
		t.Fatalf("run result not saved: %s", rec.Body)
	}

	if rec := do(t, h, "POST", base+"/run", ""); rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "shutting_down" {
		t.Fatalf("run after stop: got %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, "POST", "/jobs/missing/pause", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("pause missing: got %d", rec.Code)
	}
}
