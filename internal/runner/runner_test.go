package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
)

func httpJob(url string) job.Job {
	return job.Job{
		ID: "test",
		Spec: job.Spec{
			Name:           "test",
			Type:           job.TypeHTTP,
			HTTP:           &job.HTTPTarget{URL: url, Method: "GET"},
			TimeoutSeconds: 5,
		},
	}
}

func TestHTTPSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.Header.Get("X-Token") != "secret" || string(body) != `{"a":1}` {
			t.Errorf("unexpected request: %s %v %s", r.Method, r.Header, body)
		}
		w.Write([]byte("backup done"))
	}))
	defer srv.Close()

	j := httpJob(srv.URL)
	j.HTTP.Method = "POST"
	j.HTTP.Headers = map[string]string{"X-Token": "secret"}
	j.HTTP.Body = `{"a":1}`

	run := Run(context.Background(), j)
	if run.Status != job.StatusSuccess || run.Error != "" {
		t.Fatalf("want success, got %+v", run)
	}
	if !strings.Contains(run.Output, "200 OK") || !strings.Contains(run.Output, "backup done") {
		t.Fatalf("output should contain status and body, got %q", run.Output)
	}
	if run.FinishedAt.Before(run.StartedAt) {
		t.Fatal("FinishedAt is before StartedAt")
	}
}

func TestHTTPErrorStatusFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	run := Run(context.Background(), httpJob(srv.URL))
	if run.Status != job.StatusFailed || !strings.Contains(run.Error, "500") {
		t.Fatalf("want failed with 500, got %+v", run)
	}
}

func TestHTTPTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select { // respond slower than the job's timeout
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	j := httpJob(srv.URL)
	j.TimeoutSeconds = 1

	start := time.Now()
	run := Run(context.Background(), j)
	if run.Status != job.StatusFailed || run.Error != "timed out after 1s" {
		t.Fatalf("want timeout, got %+v", run)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("timeout did not stop the request, took %s", took)
	}
}

func TestUnreachableURLFails(t *testing.T) {
	run := Run(context.Background(), httpJob("http://127.0.0.1:1/nothing-here"))
	if run.Status != job.StatusFailed || run.Error == "" {
		t.Fatalf("want failure, got %+v", run)
	}
}
