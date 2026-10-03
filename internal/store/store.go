// Package store keeps jobs in memory. It is safe for concurrent use.
// Roadmap Step 2 replaces it with a database, so jobs survive restarts.
package store

import (
	"cmp"
	"errors"
	"slices"
	"sync"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
)

var ErrNotFound = errors.New("job not found")

// Store holds jobs in a map guarded by a mutex. It stores and returns
// copies, so the only way to change a stored job is through Update.
//
// Rule for callers: replace pointer fields (NextRunAt, LastRun, ...)
// with new values, never modify what they point to, because copies
// share those pointers.
type Store struct {
	mu   sync.Mutex
	jobs map[string]job.Job
}

func New() *Store {
	return &Store{jobs: make(map[string]job.Job)}
}

// Add saves a new job.
func (s *Store) Add(j job.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[j.ID] = j
	return nil
}

// Get returns the job with the given id.
func (s *Store) Get(id string) (job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return job.Job{}, ErrNotFound
	}
	return j, nil
}

// List returns all jobs, oldest first. It never returns nil,
// so an empty list is encoded as [] in JSON.
func (s *Store) List() ([]job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]job.Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		list = append(list, j)
	}
	slices.SortFunc(list, func(a, b job.Job) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return list, nil
}

// Delete removes the job with the given id.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return ErrNotFound
	}
	delete(s.jobs, id)
	return nil
}

// Update changes a job atomically. fn gets a copy of the job; the copy is
// saved only if fn returns nil. The lock is held while fn runs, so no
// other change can happen in between "check" and "change".
func (s *Store) Update(id string, fn func(*job.Job) error) (job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return job.Job{}, ErrNotFound
	}
	if err := fn(&j); err != nil {
		return job.Job{}, err
	}
	s.jobs[id] = j
	return j, nil
}
