package match

import "time"

// Type is the kind of match being run.
type Type int

const (
	TypeUnknown Type = iota
	// Alliance is a 2v2 match: countdown → autonomous → countdown → driver.
	// Standard Pinnacle: 3s + 15s + 3s + 105s.
	Alliance
	// SoloDriving is a single-robot timed run: countdown → driver only (60s).
	SoloDriving
	// SoloCoding is a single-robot autonomous-only run: countdown → autonomous (60s).
	// No driver countdown — there is no driver period.
	SoloCoding
)

func (t Type) String() string {
	switch t {
	case Alliance:
		return "alliance"
	case SoloDriving:
		return "solo_driving"
	case SoloCoding:
		return "solo_coding"
	default:
		return "unknown"
	}
}

// Mode is the control mode asserted to robots while the field is enabled.
type Mode int

const (
	ModeNone Mode = iota
	ModeAutonomous
	ModeDriver
)

func (m Mode) String() string {
	switch m {
	case ModeAutonomous:
		return "autonomous"
	case ModeDriver:
		return "driver"
	default:
		return "none"
	}
}

// Phase is the current phase of the match state machine.
// Robots are only ever enabled in Autonomous and Driver.
// Countdown phases are disabled — they exist for the field display and
// driver awareness only; robots see no difference from pre_match.
type Phase int

const (
	PhaseIdle            Phase = iota // no match loaded
	PhasePreMatch                     // loaded, waiting for start (disabled)
	PhaseCountdownAuton               // 3..2..1 before autonomous (disabled)
	PhaseAutonomous                   // enabled, autonomous mode
	PhaseTransition                   // disabled pause between autonomous and driver
	PhaseCountdownDriver              // 3..2..1 before driver control (disabled)
	PhaseDriver                       // enabled, driver control
	PhaseEnded                        // match completed normally (disabled)
	PhaseEstop                        // emergency stop, latched (disabled)
	PhaseFault                        // recovered from interruption; awaits operator (disabled)
)

func (p Phase) String() string {
	switch p {
	case PhasePreMatch:
		return "pre_match"
	case PhaseCountdownAuton:
		return "countdown_auton"
	case PhaseAutonomous:
		return "autonomous"
	case PhaseTransition:
		return "transition"
	case PhaseCountdownDriver:
		return "countdown_driver"
	case PhaseDriver:
		return "driver"
	case PhaseEnded:
		return "ended"
	case PhaseEstop:
		return "estop"
	case PhaseFault:
		return "fault"
	default:
		return "idle"
	}
}

// Config fully describes a match. The portal pushes this at load time; once the
// Machine has it, the match can be run to completion with no further input.
//
// Countdown phases are disabled and exist only for field display / driver awareness.
// Robots see no signal difference between pre_match and a countdown phase.
type Config struct {
	MatchID         string
	Type            Type
	CountdownAuton  time.Duration // disabled countdown before autonomous (default 3s)
	Autonomous      time.Duration
	Transition      time.Duration // disabled gap between auton and driver countdown (may be 0)
	CountdownDriver time.Duration // disabled countdown before driver control (default 3s; 0 for SoloCoding)
	Driver          time.Duration
}

// Standard RECF Achieve Pinnacle durations.

// AllianceConfig returns a standard 2v2 match:
// 3s countdown → 15s autonomous → 3s countdown → 105s driver.
func AllianceConfig(matchID string) Config {
	return Config{
		MatchID:         matchID,
		Type:            Alliance,
		CountdownAuton:  3 * time.Second,
		Autonomous:      15 * time.Second,
		Transition:      0,
		CountdownDriver: 3 * time.Second,
		Driver:          105 * time.Second,
	}
}

// SoloDrivingConfig returns a 60s driver-only skills run:
// 3s countdown → 60s driver.
func SoloDrivingConfig(matchID string) Config {
	return Config{
		MatchID:        matchID,
		Type:           SoloDriving,
		CountdownAuton: 3 * time.Second, // countdown before the run starts
		Driver:         60 * time.Second,
	}
}

// SoloCodingConfig returns a 60s autonomous-only skills run:
// 3s countdown → 60s autonomous. No driver countdown (no driver period).
func SoloCodingConfig(matchID string) Config {
	return Config{
		MatchID:        matchID,
		Type:           SoloCoding,
		CountdownAuton: 3 * time.Second,
		Autonomous:     60 * time.Second,
	}
}

// State is the output the driver layer and display clients consume.
// Enabled and Mode are the two field control signals every controller understands.
// Remaining is the time left in the current phase (countdown or enabled);
// CountdownRemaining is non-zero only during a countdown phase.
type State struct {
	MatchID            string
	Phase              Phase
	Enabled            bool
	Mode               Mode
	Remaining          time.Duration // time left in current enabled phase
	CountdownRemaining time.Duration // time left in countdown (display use)
}
