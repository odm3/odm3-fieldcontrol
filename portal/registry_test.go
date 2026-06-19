package portal_test

import (
	"testing"
	"time"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/portal"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

var epoch = time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

// TestRegistrationHandshake covers first-time registration (unknown ID → unassigned)
// and self-restore on a new address (known ID → stored role/field), the core of the
// device-identity model (DESIGN §3).
func TestRegistrationHandshake(t *testing.T) {
	reg := portal.NewRegistry(fixedClock{epoch})

	// First boot: unknown device, comes up unassigned.
	ack := reg.Register(contract.Register{DeviceID: "e6-serial1", HWSerial: "serial1", Address: "10.0.0.20"})
	if ack.Role != contract.RoleUnassigned {
		t.Fatalf("first registration role: got %q want unassigned", ack.Role)
	}

	// Admin assigns it to field 1.
	if err := reg.Assign("e6-serial1", contract.RoleField, "field-1"); err != nil {
		t.Fatal(err)
	}

	// Reboot on a NEW DHCP address: recognised as the same node, role restored.
	ack = reg.Register(contract.Register{DeviceID: "e6-serial1", HWSerial: "serial1", Address: "10.0.0.99"})
	if ack.Role != contract.RoleField || ack.Field != "field-1" {
		t.Fatalf("self-restore: got role=%q field=%q", ack.Role, ack.Field)
	}

	d, ok := reg.Get("e6-serial1")
	if !ok || d.Address != "10.0.0.99" {
		t.Fatalf("address should track latest lease: %+v ok=%v", d, ok)
	}
}

func TestAssignUnknownDevice(t *testing.T) {
	reg := portal.NewRegistry(fixedClock{epoch})
	if err := reg.Assign("nope", contract.RoleField, "field-1"); err == nil {
		t.Fatal("expected error assigning unknown device")
	}
}

func TestListHealthView(t *testing.T) {
	reg := portal.NewRegistry(fixedClock{epoch})
	reg.Register(contract.Register{DeviceID: "e6-b", HWSerial: "b", Address: "10.0.0.2"})
	reg.Register(contract.Register{DeviceID: "e6-a", HWSerial: "a", Address: "10.0.0.1"})

	list := reg.List()
	if len(list) != 2 || list[0].ID != "e6-a" || list[1].ID != "e6-b" {
		t.Fatalf("health view should be sorted by ID: %+v", list)
	}
	if list[0].LastSeen == "" {
		t.Fatal("last-seen should be populated")
	}
}
