// Package job defines what a job is: what to run, when to run it,
// and the state the scheduler keeps about it.
package job

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Type says how a job is executed.
type Type string

const (
	TypeHTTP  Type = "http"  // send an HTTP request
	TypeShell Type = "shell" // run a shell command
)

const (
	DefaultTimeoutSeconds = 30
	MaxTimeoutSeconds     = 3600
)

// HTTPTarget is the request an http job sends.
type HTTPTarget struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"` // default GET
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
}

// ShellTarget is the command a shell job runs.
type ShellTarget struct {
	Command string `json:"command"`
}

// Spec is what the user sends to create a job.
// A job has either Schedule (repeats) or RunAt (runs once), never both.
type Spec struct {
	Name           string       `json:"name"`
	Schedule       string       `json:"schedule,omitempty"` // cron expression, e.g. "0 2 * * *"
	RunAt          *time.Time   `json:"runAt,omitempty"`    // one-time run, e.g. "2026-10-04T02:00:00Z"
	Timezone       string       `json:"timezone,omitempty"` // IANA name used with Schedule, default UTC
	Type           Type         `json:"type"`
	HTTP           *HTTPTarget  `json:"http,omitempty"`
	Shell          *ShellTarget `json:"shell,omitempty"`
	TimeoutSeconds int          `json:"timeoutSeconds,omitempty"` // default 30
}

// Status is the outcome of one run.
type Status string

const (
	StatusRunning Status = "running" // only seen in run history, while in progress
	StatusSuccess Status = "success"
	StatusFailed  Status = "failed"
)

// Run is the result of executing a job once.
type Run struct {
	ID         int64     `json:"id,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt,omitzero"` // empty while running
	Status     Status    `json:"status"`
	Output     string    `json:"output,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Job is a Spec plus the state the scheduler manages.
type Job struct {
	ID string `json:"id"`
	Spec
	Enabled   bool       `json:"enabled"`             // false while paused
	Running   bool       `json:"running"`             // true while a run is in progress
	NextRunAt *time.Time `json:"nextRunAt,omitempty"` // nil when nothing is left to run
	LastRun   *Run       `json:"lastRun,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
}

// New validates spec, fills in defaults and returns a new, enabled job.
func New(spec Spec, now time.Time) (Job, error) {
	if err := spec.Validate(); err != nil {
		return Job{}, err
	}
	spec = spec.withDefaults()
	return Job{
		ID:        newID(),
		Spec:      spec,
		Enabled:   true,
		NextRunAt: spec.FirstRun(now),
		CreatedAt: now.UTC(),
	}, nil
}

// Validate returns the first problem found in s, or nil.
func (s Spec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}

	switch {
	case s.Schedule == "" && s.RunAt == nil:
		return errors.New("either schedule or runAt is required")
	case s.Schedule != "" && s.RunAt != nil:
		return errors.New("use schedule or runAt, not both")
	case s.Schedule != "":
		sched, err := cron.ParseStandard(s.Schedule)
		if err != nil {
			return fmt.Errorf("invalid schedule: %w", err)
		}
		if sched.Next(time.Now()).IsZero() {
			return errors.New("schedule never runs")
		}
	}

	if _, err := time.LoadLocation(s.Timezone); err != nil { // "" means UTC
		return fmt.Errorf("invalid timezone: %w", err)
	}

	switch s.Type {
	case TypeHTTP:
		if s.HTTP == nil {
			return errors.New(`an http job needs an "http" object`)
		}
		u, err := url.Parse(s.HTTP.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("http.url must be a full http:// or https:// URL")
		}
		switch strings.ToUpper(s.HTTP.Method) {
		case "", "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
		default:
			return fmt.Errorf("unsupported http.method %q", s.HTTP.Method)
		}
	case TypeShell:
		if s.Shell == nil || strings.TrimSpace(s.Shell.Command) == "" {
			return errors.New("shell.command is required")
		}
	default:
		return errors.New(`type must be "http" or "shell"`)
	}

	if s.TimeoutSeconds < 0 || s.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("timeoutSeconds must be between 0 and %d", MaxTimeoutSeconds)
	}
	return nil
}

// withDefaults returns a copy of s with empty optional fields filled in,
// so API responses show the values that are really used.
func (s Spec) withDefaults() Spec {
	if s.Timezone == "" {
		s.Timezone = "UTC"
	}
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if s.HTTP != nil {
		h := *s.HTTP // copy, so the caller's value is not changed
		h.Method = strings.ToUpper(h.Method)
		if h.Method == "" {
			h.Method = "GET"
		}
		s.HTTP = &h
	}
	return s
}

// IsOneTime reports whether the job runs only once (RunAt instead of Schedule).
func (s Spec) IsOneTime() bool {
	return s.RunAt != nil
}

// Timeout is the longest one run may take.
func (s Spec) Timeout() time.Duration {
	return time.Duration(s.TimeoutSeconds) * time.Second
}

// FirstRun returns when a new job should run for the first time.
func (s Spec) FirstRun(now time.Time) *time.Time {
	if s.IsOneTime() {
		t := s.RunAt.UTC()
		return &t
	}
	return s.nextAfter(now)
}

// NextRun returns when the job should run again after a run started at now.
// One-time jobs return nil: they are finished after one run.
func (s Spec) NextRun(now time.Time) *time.Time {
	if s.IsOneTime() {
		return nil
	}
	return s.nextAfter(now)
}

// nextAfter returns the next time the cron schedule fires after t, in UTC,
// or nil if it never fires again.
func (s Spec) nextAfter(t time.Time) *time.Time {
	sched, err := cron.ParseStandard(s.Schedule)
	if err != nil {
		return nil // not reachable for a validated spec
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		loc = time.UTC
	}
	// Cron fields like "2 AM" are read in the job's timezone.
	next := sched.Next(t.In(loc))
	if next.IsZero() {
		return nil
	}
	next = next.UTC()
	return &next
}

// newID returns a random 16-character hex ID.
func newID() string {
	b := make([]byte, 8)
	rand.Read(b) // never fails since Go 1.24
	return hex.EncodeToString(b)
}
