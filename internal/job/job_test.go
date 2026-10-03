package job

import (
	"strings"
	"testing"
	"time"
)

func httpSpec() Spec {
	return Spec{
		Name:     "ping",
		Schedule: "*/5 * * * *",
		Type:     TypeHTTP,
		HTTP:     &HTTPTarget{URL: "http://example.com/ping"},
	}
}

func TestValidate(t *testing.T) {
	runAt := time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		change  func(*Spec)
		wantErr string // "" means valid
	}{
		{"valid cron job", func(s *Spec) {}, ""},
		{"valid one-time job", func(s *Spec) { s.Schedule = ""; s.RunAt = &runAt }, ""},
		{"valid shell job", func(s *Spec) { s.Type = TypeShell; s.HTTP = nil; s.Shell = &ShellTarget{Command: "echo hi"} }, ""},
		{"missing name", func(s *Spec) { s.Name = " " }, "name is required"},
		{"no schedule and no runAt", func(s *Spec) { s.Schedule = "" }, "either schedule or runAt"},
		{"schedule and runAt", func(s *Spec) { s.RunAt = &runAt }, "not both"},
		{"bad cron", func(s *Spec) { s.Schedule = "every day" }, "invalid schedule"},
		{"cron that never fires", func(s *Spec) { s.Schedule = "0 0 30 2 *" }, "never runs"}, // 30 February
		{"bad timezone", func(s *Spec) { s.Timezone = "Mars/Olympus" }, "invalid timezone"},
		{"unknown type", func(s *Spec) { s.Type = "email" }, "type must be"},
		{"http without http object", func(s *Spec) { s.HTTP = nil }, `needs an "http" object`},
		{"relative url", func(s *Spec) { s.HTTP.URL = "/ping" }, "full http://"},
		{"ftp url", func(s *Spec) { s.HTTP.URL = "ftp://example.com" }, "full http://"},
		{"bad method", func(s *Spec) { s.HTTP.Method = "FETCH" }, "unsupported http.method"},
		{"shell without command", func(s *Spec) { s.Type = TypeShell; s.Shell = &ShellTarget{} }, "shell.command is required"},
		{"timeout too long", func(s *Spec) { s.TimeoutSeconds = MaxTimeoutSeconds + 1 }, "timeoutSeconds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := httpSpec()
			tt.change(&s)
			err := s.Validate()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("want valid, got error %q", err)
			case tt.wantErr != "" && err == nil:
				t.Fatalf("want error containing %q, got nil", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Fatalf("want error containing %q, got %q", tt.wantErr, err)
			}
		})
	}
}

func TestNewFillsDefaults(t *testing.T) {
	spec := httpSpec()
	j, err := New(spec, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(j.ID) != 16 || !j.Enabled || j.Running {
		t.Errorf("unexpected new job state: %+v", j)
	}
	if j.Timezone != "UTC" || j.TimeoutSeconds != DefaultTimeoutSeconds || j.HTTP.Method != "GET" {
		t.Errorf("defaults not filled: tz=%q timeout=%d method=%q", j.Timezone, j.TimeoutSeconds, j.HTTP.Method)
	}
	if spec.HTTP.Method != "" {
		t.Error("New changed the caller's spec")
	}
}

func TestCronUsesTimezone(t *testing.T) {
	spec := httpSpec()
	spec.Schedule = "0 2 * * *"  // every day at 02:00
	spec.Timezone = "Asia/Dhaka" // UTC+6

	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC) // 06:00 in Dhaka
	j, err := New(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	// Next 02:00 in Dhaka is 4 October 02:00, which is 3 October 20:00 UTC.
	want := time.Date(2026, 10, 3, 20, 0, 0, 0, time.UTC)
	if !j.NextRunAt.Equal(want) {
		t.Fatalf("NextRunAt = %v, want %v", j.NextRunAt, want)
	}
	if next := j.NextRun(want); !next.Equal(want.Add(24 * time.Hour)) {
		t.Fatalf("NextRun after first run = %v, want one day later", next)
	}
}

func TestOneTimeJobRunsOnce(t *testing.T) {
	runAt := time.Date(2026, 10, 4, 2, 0, 0, 0, time.UTC)
	spec := httpSpec()
	spec.Schedule = ""
	spec.RunAt = &runAt

	j, err := New(spec, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !j.NextRunAt.Equal(runAt) {
		t.Fatalf("first run = %v, want %v", j.NextRunAt, runAt)
	}
	if next := j.NextRun(runAt); next != nil {
		t.Fatalf("one-time job should have no next run, got %v", next)
	}
}
