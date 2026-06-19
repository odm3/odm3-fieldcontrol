// Package driver is the boundary between the match state machine and field
// hardware. The field only ever asserts two signals — enabled/disabled and
// autonomous/driver (DESIGN §1) — so a Driver consumes exactly those two values
// and is opaque about how it reaches the robot. A registry lets the run loop
// select a driver (mock for tests, V5 USB serial on real hardware) by name.
package driver

import (
	"fmt"
	"sort"
	"sync"

	"github.com/odm3/e6events/fieldcontrol/match"
)

// Driver asserts the field's two signals to hardware. Apply must be idempotent:
// the run loop reasserts the current output on every tick as a watchdog heartbeat.
type Driver interface {
	Name() string
	Apply(enabled bool, mode match.Mode) error
	Close() error
}

// Factory constructs a Driver. Registration uses factories so opening hardware
// (e.g. serial ports) is deferred until a field actually selects the driver.
type Factory func() (Driver, error)

// Registry maps driver names to factories.
type Registry struct {
	mu sync.RWMutex
	m  map[string]Factory
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{m: make(map[string]Factory)}
}

// Register adds a named factory, overwriting any existing one.
func (r *Registry) Register(name string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[name] = f
}

// Open constructs the driver registered under name.
func (r *Registry) Open(name string) (Driver, error) {
	r.mu.RLock()
	f, ok := r.m[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("driver: %q not registered", name)
	}
	return f()
}

// Names lists registered driver names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for n := range r.m {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
