package driver

import (
	"fmt"

	"github.com/odm3/e6events/fieldcontrol/match"
)

// Pin is a single binary GPIO output line. Abstracting it keeps the V5 driver fully
// testable with a fake pin and no hardware (DESIGN §12); the real implementation
// wraps a Pi GPIO library.
type Pin interface {
	// Set drives the line high (true) or low (false).
	Set(high bool) error
}

// V5GPIO drives a VEX V5 robot through the legacy competition port, broken out to
// two Pi GPIO lines: an ENABLE line and an AUTONOMOUS line. This mirrors the two
// signals the field asserts (DESIGN §1, §5).
//
// NOTE: the exact competition-port pin map / RJ45 breakout wiring and active level
// are still an open question (DESIGN §11). Pins and ActiveHigh are therefore both
// injected/configurable rather than hard-coded.
type V5GPIO struct {
	enable     Pin
	auton      Pin
	activeHigh bool
}

// NewV5GPIO builds a driver over the given enable and autonomous pins. activeHigh
// selects the asserted electrical level (true = drive high to assert).
func NewV5GPIO(enable, auton Pin, activeHigh bool) (*V5GPIO, error) {
	if enable == nil || auton == nil {
		return nil, fmt.Errorf("v5gpio: enable and auton pins are required")
	}
	d := &V5GPIO{enable: enable, auton: auton, activeHigh: activeHigh}
	// Come up safe: robot disabled.
	if err := d.Apply(match.Output{}); err != nil {
		return nil, err
	}
	return d, nil
}

// level maps a logical "asserted" to the configured electrical level.
func (d *V5GPIO) level(asserted bool) bool {
	if d.activeHigh {
		return asserted
	}
	return !asserted
}

// Name implements Driver.
func (d *V5GPIO) Name() string { return "v5-gpio" }

// Apply implements Driver. ENABLE is asserted whenever the robot is enabled;
// AUTONOMOUS is asserted only in autonomous mode. Driver mode is enabled with the
// autonomous line deasserted; every disabled phase deasserts both.
func (d *V5GPIO) Apply(out match.Output) error {
	if err := d.enable.Set(d.level(out.Enabled)); err != nil {
		return fmt.Errorf("v5gpio: enable line: %w", err)
	}
	auto := out.Enabled && out.Mode == match.ModeAutonomous
	if err := d.auton.Set(d.level(auto)); err != nil {
		return fmt.Errorf("v5gpio: auton line: %w", err)
	}
	return nil
}

// Close deasserts both lines, leaving the robot disabled.
func (d *V5GPIO) Close() error {
	return d.Apply(match.Output{})
}
