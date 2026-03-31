package configsdk

import "sync"

// Snapshot is a generic thread-safe wrapper for configuration values.
// It is intended for use with watch mode to safely read configuration
// while it may be updated concurrently.
type Snapshot[T any] struct {
	mu    sync.RWMutex
	value T
}

// NewSnapshot creates a new Snapshot with the zero value of T.
func NewSnapshot[T any]() *Snapshot[T] {
	return &Snapshot[T]{}
}

// Load returns the current value. Safe for concurrent use.
func (s *Snapshot[T]) Load() T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value
}

// Store sets a new value. Safe for concurrent use.
func (s *Snapshot[T]) Store(v T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.value = v
}
