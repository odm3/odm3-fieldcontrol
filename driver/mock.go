package driver

import (
	"sync"

	"github.com/odm3/e6events/fieldcontrol/match"
)

// Mock is an in-memory Driver that records every Output applied to it, so the full
// match lifecycle can be tested with no hardware (DESIGN §12).
type Mock struct {
	mu      sync.Mutex
	Applied []match.Output
	Closed  bool
}

// NewMock returns a ready Mock.
func NewMock() *Mock { return &Mock{} }

// Name implements Driver.
func (m *Mock) Name() string { return "mock" }

// Apply implements Driver.
func (m *Mock) Apply(o match.Output) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Applied = append(m.Applied, o)
	return nil
}

// Last returns the most recently applied output (or the zero Output if none).
func (m *Mock) Last() match.Output {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Applied) == 0 {
		return match.Output{}
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
