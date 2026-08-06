package api

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Store keeps jobs in memory with artifacts on disk. v1: no persistence
// across restarts.
type Store struct {
	mu      sync.Mutex
	jobs    map[string]*Job
	dataDir string
	ttl     time.Duration
}

func NewStore(dataDir string, ttl time.Duration) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	return &Store{
		jobs:    make(map[string]*Job),
		dataDir: dataDir,
		ttl:     ttl,
	}, nil
}

// Create registers a new queued job and its disk directory.
func (s *Store) Create(opts JobOptions) (*Job, error) {
	id := NewJobID()
	dir := filepath.Join(s.dataDir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	now := time.Now()
	job := &Job{
		ID:        id,
		Status:    StatusQueued,
		Options:   opts,
		CreatedAt: now,
		ExpiresAt: now.Add(s.ttl),
		Dir:       dir,
	}
	s.mu.Lock()
	s.jobs[id] = job
	s.mu.Unlock()
	return job, nil
}

// Update runs fn with the store lock held so job mutations are atomic.
func (s *Store) Update(id string, fn func(*Job)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if job, ok := s.jobs[id]; ok {
		fn(job)
	}
}

// Snapshot returns a deep-enough copy of the job for reading without locks.
func (s *Store) Snapshot(id string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, ok := s.jobs[id]
	if !ok {
		return Job{}, false
	}
	cp := *job
	cp.Pages = append([]PageResult(nil), job.Pages...)
	return cp, true
}

// Delete removes the job and its artifacts.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	job, ok := s.jobs[id]
	if ok {
		delete(s.jobs, id)
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	os.RemoveAll(job.Dir)
	return true
}

// Count returns the number of live jobs.
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.jobs)
}

// StartSweeper deletes expired jobs every interval until stop is closed.
func (s *Store) StartSweeper(interval time.Duration, stop <-chan struct{}) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				s.sweep()
			}
		}
	}()
}

func (s *Store) sweep() {
	now := time.Now()
	var expired []string
	s.mu.Lock()
	for id, job := range s.jobs {
		// never expire a job mid-render
		if job.Status != StatusQueued && job.Status != StatusProcessing && now.After(job.ExpiresAt) {
			expired = append(expired, id)
		}
	}
	s.mu.Unlock()
	for _, id := range expired {
		s.Delete(id)
	}
}
