package driver

import (
	"sync"

	"github.com/odm3/e6events/fieldcontrol/match"
)

// Applied records one call to Mock.Apply.
type Applied struct {
	Enabled bool
	Mode    match.Mode
}

// Mock is an in-memory Driver that records every Apply call, so the full match
// lifecycle can be tested with no hardware (DESIGN §14).
type Mock struct {
	mu      sync.Mutex
	Applied []Applied
	Closed  bool
}

// NewMock returns a ready Mock.
func NewMock() *Mock { return &Mock{} }

// Name implements Driver.
func (m *Mock) Name() string { return "mock" }

// Apply implements Driver.
func (m *Mock) Apply(enabled bool, mode match.Mode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Applied = append(m.Applied, Applied{Enabled: enabled, Mode: mode})
	return nil
}

// Last returns the most recently applied call (or zero if none).
func (m *Mock) Last() Applied {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Applied) == 0 {
		return Applied{}
	}
	return m.Applied[len(m.Applied)-1]
}

// Close implements Driver.
func (m *Mock) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Closed = true
	return nil
}
