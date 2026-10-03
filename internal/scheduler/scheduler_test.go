package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
	"github.com/AfsarTanvir/local-scheduler-/internal/store"
)

// fakeRunner counts runs and, if block is set, waits for it to be closed
// (or for ctx to end) before finishing.
type fakeRunner struct {
	calls atomic.Int32
	block chan struct{}
}

func (f *fakeRunner) run(ctx context.Context, j job.Job) job.Run {
	f.calls.Add(1)
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return job.Run{Status: job.StatusFailed, Error: "canceled"}
		}
	}
	return job.Run{Status: job.StatusSuccess}
}

func setup(t *testing.T) (*Scheduler, *store.Store, *fakeRunner) {
	t.Helper()
	st := openStore(t)
	f := &fakeRunner{}
	return New(st, f.run), st, f
}

// openStore opens a database in a temporary directory that is deleted after the test.
func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// addJob creates a job at now. A non-empty schedule makes a repeating job,
// otherwise it is a one-time job at runAt.
func addJob(t *testing.T, st *store.Store, now time.Time, schedule string, runAt time.Time) job.Job {
	t.Helper()
	spec := job.Spec{
		Name: "test",
		Type: job.TypeHTTP,
		HTTP: &job.HTTPTarget{URL: "http://example.com"},
	}
	if schedule != "" {
		spec.Schedule = schedule
	} else {
		spec.RunAt = &runAt
	}
	j, err := job.New(spec, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Add(j); err != nil {
		t.Fatal(err)
	}
	return j
}

// wait stops the scheduler, which waits for all runs to finish.
func wait(t *testing.T, s *Scheduler) {
	t.Helper()
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOneTimeJobRunsOnceWhenDue(t *testing.T) {
	s, st, f := setup(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	j := addJob(t, st, now, "", now.Add(time.Minute))

	s.tick(now) // not due yet
	if f.calls.Load() != 0 {
		t.Fatal("job ran before its time")
	}

	s.tick(now.Add(time.Minute))
	s.tick(now.Add(2 * time.Minute))
	wait(t, s)

	if got := f.calls.Load(); got != 1 {
		t.Fatalf("want 1 run, got %d", got)
	}
	got, _ := st.Get(j.ID)
	if got.Running || got.NextRunAt != nil || got.LastRun == nil || got.LastRun.Status != job.StatusSuccess {
		t.Fatalf("unexpected state after run: %+v", got)
	}
}

func TestRepeatingJobMovesToNextRun(t *testing.T) {
	s, st, f := setup(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	j := addJob(t, st, now, "@every 1m", time.Time{})

	s.tick(now.Add(time.Minute))
	wait(t, s)

	got, _ := st.Get(j.ID)
	if want := now.Add(2 * time.Minute); f.calls.Load() != 1 || !got.NextRunAt.Equal(want) {
		t.Fatalf("calls=%d NextRunAt=%v, want 1 call and %v", f.calls.Load(), got.NextRunAt, want)
	}
}

func TestDoesNotOverlapRuns(t *testing.T) {
	s, st, f := setup(t)
	f.block = make(chan struct{})
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	j := addJob(t, st, now, "@every 1m", time.Time{})

	s.tick(now.Add(time.Minute))     // starts, and keeps running
	s.tick(now.Add(2 * time.Minute)) // still running: skipped

	got, _ := st.Get(j.ID)
	if !got.NextRunAt.Equal(now.Add(3 * time.Minute)) {
		t.Fatalf("skipped run should still move NextRunAt forward, got %v", got.NextRunAt)
	}
	if _, err := s.RunNow(j.ID); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("RunNow while running: want ErrAlreadyRunning, got %v", err)
	}

	close(f.block)
	wait(t, s)
	if got := f.calls.Load(); got != 1 {
		t.Fatalf("want 1 run, got %d", got)
	}
}

func TestPausedJobDoesNotRun(t *testing.T) {
	s, st, f := setup(t)
	now := time.Now()
	j := addJob(t, st, now, "", now.Add(-time.Second)) // already due

	if _, err := s.Pause(j.ID); err != nil {
		t.Fatal(err)
	}
	s.tick(now)
	if f.calls.Load() != 0 {
		t.Fatal("paused job ran")
	}

	if _, err := s.Resume(j.ID); err != nil {
		t.Fatal(err)
	}
	s.tick(now)
	wait(t, s)
	if f.calls.Load() != 1 {
		t.Fatal("resumed one-time job did not run")
	}
}

func TestResumeSkipsMissedRepeatingRuns(t *testing.T) {
	s, st, _ := setup(t)
	past := time.Now().Add(-time.Hour)
	j := addJob(t, st, past, "@every 1m", time.Time{}) // NextRunAt is an hour ago

	s.Pause(j.ID)
	got, _ := s.Resume(j.ID)
	if !got.NextRunAt.After(time.Now()) {
		t.Fatalf("after resume NextRunAt should be in the future, got %v", got.NextRunAt)
	}
}

func TestRunNow(t *testing.T) {
	s, st, f := setup(t)
	now := time.Now()
	j := addJob(t, st, now, "", now.Add(time.Hour))

	if _, err := s.RunNow("missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := s.RunNow(j.ID); err != nil {
		t.Fatal(err)
	}
	wait(t, s)

	got, _ := st.Get(j.ID)
	if f.calls.Load() != 1 || got.LastRun == nil {
		t.Fatal("RunNow did not run the job")
	}
	if !got.NextRunAt.Equal(*j.NextRunAt) {
		t.Fatal("RunNow should not change NextRunAt")
	}
}

func TestStopWaitsThenCancels(t *testing.T) {
	s, st, f := setup(t)
	f.block = make(chan struct{}) // never closed: only cancel can end the run
	now := time.Now()
	j := addJob(t, st, now, "", now)

	s.tick(now)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}

	got, _ := st.Get(j.ID)
	if got.Running || got.LastRun == nil || got.LastRun.Error != "canceled" {
		t.Fatalf("run should have been canceled and saved, got %+v", got)
	}
	if _, err := s.RunNow(j.ID); !errors.Is(err, ErrStopped) {
		t.Fatalf("RunNow after Stop: want ErrStopped, got %v", err)
	}
}

func TestStartAndStopLoop(t *testing.T) {
	s, st, f := setup(t)
	now := time.Now()
	addJob(t, st, now, "", now) // due now

	s.Start()
	deadline := time.Now().Add(3 * time.Second)
	for f.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	wait(t, s)
	if f.calls.Load() != 1 {
		t.Fatal("loop did not run the due job")
	}
}
