package time

import (
	"sync"
	"time"
)

type SharedTime struct {
	sync.RWMutex
	time time.Time
}

func (s *SharedTime) Before(other time.Time) bool {
	s.RLock()
	defer s.RUnlock()

	return s.time.Before(other)
}

func (s *SharedTime) After(other time.Time) bool {
	s.RLock()
	defer s.RUnlock()

	return !s.time.Before(other)
}

func (s *SharedTime) Set(current time.Time) {
	s.Lock()
	defer s.Unlock()

	s.time = current
}
