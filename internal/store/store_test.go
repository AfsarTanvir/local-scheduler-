package store

import (
	"errors"
	"testing"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
)

func TestAddGetListDelete(t *testing.T) {
	s := New()
	if list := s.List(); list == nil || len(list) != 0 {
		t.Fatalf("empty store should list [], got %v", list)
	}

	now := time.Now()
	s.Add(job.Job{ID: "b", CreatedAt: now.Add(time.Second)})
	s.Add(job.Job{ID: "a", CreatedAt: now})

	if got, err := s.Get("a"); err != nil || got.ID != "a" {
		t.Fatalf("Get(a) = %v, %v", got, err)
	}
	if list := s.List(); len(list) != 2 || list[0].ID != "a" {
		t.Fatalf("List should be oldest first, got %v", list)
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
	s := New()
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

	updated, err := s.Update("a", func(j *job.Job) error {
		j.Enabled = false
		return nil
	})
	if err != nil || updated.Enabled {
		t.Fatalf("Update = %v, %v", updated, err)
	}
	if j, _ := s.Get("a"); j.Enabled {
		t.Fatal("change was not saved")
	}

	if _, err := s.Update("missing", func(*job.Job) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
