package driver_test

import (
	"testing"

	"github.com/odm3/e6events/fieldcontrol/driver"
	"github.com/odm3/e6events/fieldcontrol/match"
)

func TestRegistryOpen(t *testing.T) {
	r := driver.NewRegistry()
	r.Register("mock", func() (driver.Driver, error) { return driver.NewMock(), nil })

	d, err := r.Open("mock")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name() != "mock" {
		t.Fatalf("name: got %q", d.Name())
	}
	if _, err := r.Open("nope"); err == nil {
		t.Fatal("expected error opening unregistered driver")
	}
	if got := r.Names(); len(got) != 1 || got[0] != "mock" {
		t.Fatalf("names: got %v", got)
	}
}

// fakePin records the last level it was driven to.
type fakePin struct {
	level   bool
	driven  bool
	failNow bool
}

func (p *fakePin) Set(high bool) error {
	if p.failNow {
		return errPin
	}
	p.level = high
	p.driven = true
	return nil
}

var errPin = errFake("pin failure")

type errFake string

func (e errFake) Error() string { return string(e) }

// TestV5GPIOActiveHigh checks the enable/auton line levels for every output, with
// active-high wiring.
func TestV5GPIOActiveHigh(t *testing.T) {
	en, au := &fakePin{}, &fakePin{}
	d, err := driver.NewV5GPIO(en, au, true)
	if err != nil {
		t.Fatal(err)
	}
	// Constructed safe: both deasserted (low for active-high).
	if en.level || au.level {
		t.Fatalf("after construct: enable=%v auton=%v, want both low", en.level, au.level)
	}

	cases := []struct {
		name             string
		out              match.Output
		wantEn, wantAuto bool
	}{
		{"disabled", match.Output{Enabled: false, Mode: match.ModeDisabled}, false, false},
		{"autonomous", match.Output{Enabled: true, Mode: match.ModeAutonomous}, true, true},
		{"driver", match.Output{Enabled: true, Mode: match.ModeDriver}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := d.Apply(tc.out); err != nil {
				t.Fatal(err)
			}
			if en.level != tc.wantEn || au.level != tc.wantAuto {
				t.Fatalf("enable=%v auton=%v, want enable=%v auton=%v",
					en.level, au.level, tc.wantEn, tc.wantAuto)
			}
		})
	}
}

// TestV5GPIOActiveLow verifies the level inversion for active-low wiring.
func TestV5GPIOActiveLow(t *testing.T) {
	en, au := &fakePin{}, &fakePin{}
	d, err := driver.NewV5GPIO(en, au, false)
	if err != nil {
		t.Fatal(err)
	}
	// Disabled with active-low means lines are HIGH (deasserted).
	if !en.level || !au.level {
		t.Fatalf("active-low disabled: enable=%v auton=%v, want both high", en.level, au.level)
	}
	if err := d.Apply(match.Output{Enabled: true, Mode: match.ModeDriver}); err != nil {
		t.Fatal(err)
	}
	// Enabled asserts enable (low), driver mode deasserts auton (high).
	if en.level || !au.level {
		t.Fatalf("active-low driver: enable=%v auton=%v, want enable low, auton high", en.level, au.level)
	}
}

func TestV5GPIORequiresPins(t *testing.T) {
	if _, err := driver.NewV5GPIO(nil, &fakePin{}, true); err == nil {
		t.Fatal("expected error with nil enable pin")
	}
}

func TestMockRecords(t *testing.T) {
	m := driver.NewMock()
	_ = m.Apply(match.Output{Enabled: true, Mode: match.ModeDriver})
	_ = m.Apply(match.Output{})
	if len(m.Applied) != 2 {
		t.Fatalf("applied: got %d want 2", len(m.Applied))
	}
	if last := m.Last(); last.Enabled {
		t.Fatalf("last should be disabled, got %+v", last)
	}
	_ = m.Close()
	if !m.Closed {
		t.Fatal("expected Closed")
	}
}
