// Package portal is the minimal tournament-server slice needed to drive one field:
// a device registry with the registration handshake (DESIGN §3) and a controller
// that issues control-plane commands to a single field (DESIGN §13 Phase 1). The
// full tournament data model, scoring, and view plane are later phases.
package portal

import (
	"fmt"
	"sort"
	"sync"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/match"
)

// Device is a registry entry keyed by stable device ID. Identity never lives in the
// address, so a device returning on a new DHCP lease is recognised as the same node
// (DESIGN §3).
type Device struct {
	ID       string
	HWSerial string
	Address  string // last-seen address
	Role     contract.Role
	Field    string // field binding, if any
	LastSeen string // RFC3339; string so the registry is trivially serialisable
}

// Registry is the device registry and event-day health view (DESIGN §3).
type Registry struct {
	clock match.Clock
	mu    sync.RWMutex
	m     map[string]*Device
}

// NewRegistry returns an empty registry using clock for last-seen timestamps.
func NewRegistry(clock match.Clock) *Registry {
	return &Registry{clock: clock, m: make(map[string]*Device)}
}

// Register handles a boot-time registration. An unknown device is recorded as
// Unassigned and must be assigned a role by an admin; a known device is updated in
// place and its stored role/field are returned (self-restore). Either way the ack
// tells the device what it is.
func (r *Registry) Register(reg contract.Register) contract.RegisterAck {
	r.mu.Lock()
	defer r.mu.Unlock()

	d, ok := r.m[reg.DeviceID]
	if !ok {
		d = &Device{ID: reg.DeviceID, HWSerial: reg.HWSerial, Role: contract.RoleUnassigned}
		r.m[reg.DeviceID] = d
	}
	d.Address = reg.Address
	d.LastSeen = r.clock.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
	if d.HWSerial == "" {
		d.HWSerial = reg.HWSerial
	}
	return contract.RegisterAck{DeviceID: d.ID, Role: d.Role, Field: d.Field}
}

// Assign sets a device's role and field binding (admin action). It errors if the
// device is unknown.
func (r *Registry) Assign(deviceID string, role contract.Role, field string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.m[deviceID]
	if !ok {
		return fmt.Errorf("portal: unknown device %q", deviceID)
	}
	d.Role = role
	d.Field = field
	return nil
}

// Get returns a copy of a device entry.
func (r *Registry) Get(deviceID string) (Device, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	d, ok := r.m[deviceID]
	if !ok {
		return Device{}, false
	}
	return *d, true
}

// List returns all devices sorted by ID — the event-day health view (DESIGN §3).
func (r *Registry) List() []Device {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Device, 0, len(r.m))
	for _, d := range r.m {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
