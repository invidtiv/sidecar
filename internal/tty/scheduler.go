package tty

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// KeyedScheduler provides one generation/invalidation mechanism for any number
// of independently polled panes. It is intended for Bubble Tea's single update
// goroutine; callers namespace keys by pane source.
type KeyedScheduler struct {
	generations map[string]int
	base        int
	next        int
}

// Current returns the current generation token for key.
func (s *KeyedScheduler) Current(key string) int {
	if s == nil {
		return 0
	}
	if generation, ok := s.generations[key]; ok {
		return generation
	}
	return s.base
}

// IsCurrent reports whether generation still owns key's poll chain.
func (s *KeyedScheduler) IsCurrent(key string, generation int) bool {
	return generation == s.Current(key)
}

// Invalidate advances key's generation and returns the new token.
func (s *KeyedScheduler) Invalidate(key string) int {
	if s.generations == nil {
		s.generations = make(map[string]int)
	}
	if s.next < s.base {
		s.next = s.base
	}
	s.next++
	s.generations[key] = s.next
	return s.next
}

// Reset invalidates all outstanding keys without retaining old pane names.
func (s *KeyedScheduler) Reset() {
	if s.next < s.base {
		s.next = s.base
	}
	s.next++
	s.base = s.next
	s.generations = nil
}

// Schedule supersedes any outstanding work for key and returns a delayed
// command stamped with the new generation.
func (s *KeyedScheduler) Schedule(key string, delay time.Duration, message func(generation int) tea.Msg) tea.Cmd {
	generation := s.Invalidate(key)
	if delay <= 0 {
		return func() tea.Msg { return message(generation) }
	}
	return tea.Tick(delay, func(time.Time) tea.Msg { return message(generation) })
}
