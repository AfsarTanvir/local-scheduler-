package store

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
)

// openTemp opens a new database in a temporary directory that is
// deleted after the test.
func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

// fullJob returns a job with every field set, to check nothing is lost.
func fullJob(t *testing.T) job.Job {
	t.Helper()
	j, err := job.New(job.Spec{
		Name:     "backup",
		Schedule: "0 2 * * *",
		Timezone: "Asia/Dhaka",
		Type:     job.TypeHTTP,
		HTTP: &job.HTTPTarget{
			URL:     "http://example.com/backup",
			Method:  "POST",
			Headers: map[string]string{"Authorization": "Bearer x"},
			Body:    `{"db":"main"}`,
		},
		TimeoutSeconds: 10,
	}, time.Date(2026, 10, 3, 12, 0, 0, 123456789, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	j.LastRun = &job.Run{
		StartedAt:  time.Date(2026, 10, 3, 12, 1, 0, 0, time.UTC),
		FinishedAt: time.Date(2026, 10, 3, 12, 1, 2, 0, time.UTC),
		Status:     job.StatusFailed,
		Output:     "500 Internal Server Error",
		Error:      "unexpected status",
	}
	return j
}

func TestRoundTripKeepsAllFields(t *testing.T) {
	s, _ := openTemp(t)
	want := fullJob(t)
	if err := s.Add(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("job changed after save and load\n got: %+v\nwant: %+v", got, want)
	}
}

func TestAddGetListDelete(t *testing.T) {
	s, _ := openTemp(t)
	if list, err := s.List(); err != nil || list == nil || len(list) != 0 {
		t.Fatalf("empty store should list [], got %v, %v", list, err)
	}

	now := time.Now()
	s.Add(job.Job{ID: "b", CreatedAt: now.Add(time.Second)})
	s.Add(job.Job{ID: "a", CreatedAt: now})

	if got, err := s.Get("a"); err != nil || got.ID != "a" {
		t.Fatalf("Get(a) = %v, %v", got, err)
	}
	if list, _ := s.List(); len(list) != 2 || list[0].ID != "a" {
		t.Fatalf("List should be oldest first, got %v", list)
	}
	if err := s.Add(job.Job{ID: "a", CreatedAt: now}); err == nil {
		t.Fatal("adding a duplicate id should fail")
	}
	if err := s.Delete("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after Delete: want ErrNotFound, got %v", err)
	}
	if err := s.Delete("a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete: want ErrNotFound, got %v", err)
	}
}

func TestUpdateSavesOnlyOnSuccess(t *testing.T) {
	s, _ := openTemp(t)
	s.Add(job.Job{ID: "a", Enabled: true})

	errStop := errors.New("stop")
	_, err := s.Update("a", func(j *job.Job) error {
		j.Enabled = false
		return errStop
	})
	if !errors.Is(err, errStop) {
		t.Fatalf("want errStop, got %v", err)
	}
	if j, _ := s.Get("a"); !j.Enabled {
		t.Fatal("change was saved even though fn returned an error")
	}

	next := time.Now().UTC()
	updated, err := s.Update("a", func(j *job.Job) error {
		j.Enabled = false
		j.NextRunAt = &next
		return nil
	})
	if err != nil || updated.Enabled {
		t.Fatalf("Update = %v, %v", updated, err)
	}
	if j, _ := s.Get("a"); j.Enabled || !j.NextRunAt.Equal(next) {
		t.Fatalf("change was not saved: %+v", j)
	}

	if _, err := s.Update("missing", func(*job.Job) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestDue(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Minute), now.Add(time.Minute)

	s.Add(job.Job{ID: "due", Enabled: true, NextRunAt: &past})
	s.Add(job.Job{ID: "due-exactly-now", Enabled: true, NextRunAt: &now})
	s.Add(job.Job{ID: "future", Enabled: true, NextRunAt: &future})
	s.Add(job.Job{ID: "paused", Enabled: false, NextRunAt: &past})
	s.Add(job.Job{ID: "finished", Enabled: true, NextRunAt: nil})

	due, err := s.Due(now)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, j := range due {
		ids = append(ids, j.ID)
	}
	if want := []string{"due", "due-exactly-now"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("Due = %v, want %v", ids, want)
	}
}

func TestJobsSurviveReopen(t *testing.T) {
	s, path := openTemp(t)
	want := fullJob(t)
	s.Add(want)
	s.Close()

	s2, err := Open(path) // runs migrate again: must not fail or lose data
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.Get(want.ID)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("after reopen: %+v, %v", got, err)
	}
}

func TestTimeFormatSortsLikeTime(t *testing.T) {
	a := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	b := a.Add(time.Nanosecond)
	c := time.Date(2026, 10, 3, 15, 0, 0, 0, time.FixedZone("Dhaka", 6*3600)) // 09:00 UTC + 0
	if !(formatTime(a) < formatTime(b)) {
		t.Fatal("string order differs from time order")
	}
	if formatTime(a) != formatTime(c) {
		t.Fatal("same instant in different zones should format the same")
	}
	if got, _ := parseTime(formatTime(b)); !got.Equal(b) {
		t.Fatal("parse(format(t)) != t")
	}
}
