// Package match implements an authoritative single-match state machine for
// RECF Achieve Pinnacle field control. The live match runs on the local clock
// so a dropped portal connection cannot affect match progression.
package match

import (
	"errors"
	"fmt"
	"time"
)

// Clock is injected to allow deterministic testing.
type Clock interface {
	Now() time.Time
}

// RealClock implements Clock using the system clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// Phase represents the current state of the match state machine.
type Phase int

const (
	PhaseIdle       Phase = iota // no match loaded
	PhasePreMatch                // match loaded, waiting for start
	PhaseAutonomous              // robots enabled, autonomous mode
	PhaseTransition              // brief gap between auto and driver, robots disabled
	PhaseDriver                  // robots enabled, driver mode
	PhaseEnded                   // match over
	PhaseEStop                   // latched disabled, overrides everything
	PhaseFault                   // mid-match reboot recovery
)

func (p Phase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhasePreMatch:
		return "pre_match"
	case PhaseAutonomous:
		return "autonomous"
	case PhaseTransition:
		return "transition"
	case PhaseDriver:
		return "driver"
	case PhaseEnded:
		return "ended"
	case PhaseEStop:
		return "estop"
	case PhaseFault:
		return "fault"
	}
	return fmt.Sprintf("Phase(%d)", int(p))
}

// Mode is the robot enable mode communicated to field hardware.
type Mode int

const (
	ModeDisabled Mode = iota
	ModeAutonomous
	ModeDriver
)

func (m Mode) String() string {
	switch m {
	case ModeDisabled:
		return "disabled"
	case ModeAutonomous:
		return "autonomous"
	case ModeDriver:
		return "driver"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

// Output is the derived robot enable signal sent to field hardware.
type Output struct {
	Enabled bool
	Mode    Mode
}

// MatchType defines the sequence of timed phases.
type MatchType int

const (
	// Alliance: 15s autonomous + 5s transition + 105s driver.
	Alliance MatchType = iota
	// SoloDriving: 60s driver only.
	SoloDriving
	// SoloCoding: 60s autonomous only.
	SoloCoding
)

func (mt MatchType) String() string {
	switch mt {
	case Alliance:
		return "alliance"
	case SoloDriving:
		return "solo_driving"
	case SoloCoding:
		return "solo_coding"
	}
	return fmt.Sprintf("MatchType(%d)", int(mt))
}

const transitionDuration = 5 * time.Second

// durations returns the autonomous and driver phase durations for the match type.
// A zero duration means that phase is skipped.
func durations(mt MatchType) (auto, driver time.Duration) {
	switch mt {
	case Alliance:
		return 15 * time.Second, 105 * time.Second
	case SoloDriving:
		return 0, 60 * time.Second
	case SoloCoding:
		return 60 * time.Second, 0
	}
	return 0, 0
}

// MatchID uniquely identifies a match (e.g. "Q-001").
type MatchID string

// State holds all mutable match state. It is safe to snapshot and restore.
type State struct {
	ID        MatchID
	Type      MatchType
	Phase     Phase
	PhaseEnd  time.Time // when the current timed phase ends (zero if untimed)
	EStopAt   time.Time // when estop was triggered (zero if not estopped)
	StartedAt time.Time // when the match was started (zero if not started)
}

// Output derives the current robot enable output from state.
func (s *State) Output() Output {
	switch s.Phase {
	case PhaseAutonomous:
		return Output{Enabled: true, Mode: ModeAutonomous}
	case PhaseDriver:
		return Output{Enabled: true, Mode: ModeDriver}
	default:
		return Output{Enabled: false, Mode: ModeDisabled}
	}
}

// Machine is the authoritative match state machine.
// All methods are safe to call from a single goroutine; callers must
// synchronise if they share a Machine across goroutines.
type Machine struct {
	clock   Clock
	state   State
	storage Storer
}

// New creates a Machine in PhaseIdle.
func New(clock Clock, storage Storer) *Machine {
	return &Machine{clock: clock, storage: storage}
}

var (
	ErrWrongPhase  = errors.New("operation not valid in current phase")
	ErrNoMatch     = errors.New("no match loaded")
	ErrAlreadyStop = errors.New("estop already active")
)

// Load prepares a new match for the given ID and type, moving to PhasePreMatch.
//
// Load is only valid from a clean state (Idle, a re-load in PreMatch, or after a
// completed match in Ended). A latched EStop or a recovered Fault must be cleared
// with Reset first — Load never clears them (DESIGN §5, §10).
func (m *Machine) Load(id MatchID, mt MatchType) error {
	switch m.state.Phase {
	case PhaseIdle, PhasePreMatch, PhaseEnded:
		// allowed
	default:
		return fmt.Errorf("%w: cannot load in %s (Reset first)", ErrWrongPhase, m.state.Phase)
	}
	m.state = State{
		ID:    id,
		Type:  mt,
		Phase: PhasePreMatch,
	}
	return nil
}

// Start begins the match from PhasePreMatch.
// It writes a failsafe snapshot before enabling robots.
func (m *Machine) Start() error {
	if m.state.Phase != PhasePreMatch {
		return fmt.Errorf("%w: cannot start in %s", ErrWrongPhase, m.state.Phase)
	}

	now := m.clock.Now()
	m.state.StartedAt = now

	auto, driver := durations(m.state.Type)

	if auto > 0 {
		m.state.Phase = PhaseAutonomous
		m.state.PhaseEnd = now.Add(auto)
	} else if driver > 0 {
		m.state.Phase = PhaseDriver
		m.state.PhaseEnd = now.Add(driver)
	} else {
		m.state.Phase = PhaseEnded
		m.state.PhaseEnd = time.Time{}
	}

	// Persist failsafe snapshot before robots go live.
	if m.storage != nil {
		snap := m.state
		snap.Phase = PhaseFault // reboot during match → fault
		if err := m.storage.Save(snap); err != nil {
			return fmt.Errorf("failsafe snapshot: %w", err)
		}
	}

	return nil
}

// Tick advances phase transitions based on the current clock time.
// Call this periodically (e.g. every 100 ms) from the match loop.
func (m *Machine) Tick() {
	if m.state.PhaseEnd.IsZero() {
		return
	}
	now := m.clock.Now()
	if now.Before(m.state.PhaseEnd) {
		return
	}
	m.advance()
}

// advance moves to the next logical phase when a timed phase expires.
func (m *Machine) advance() {
	_, driver := durations(m.state.Type)
	now := m.clock.Now()

	switch m.state.Phase {
	case PhaseAutonomous:
		if driver > 0 {
			m.state.Phase = PhaseTransition
			m.state.PhaseEnd = m.state.PhaseEnd.Add(transitionDuration)
		} else {
			m.endMatch()
		}
	case PhaseTransition:
		m.state.Phase = PhaseDriver
		m.state.PhaseEnd = now.Add(driver)
	case PhaseDriver:
		m.endMatch()
	}
}

func (m *Machine) endMatch() {
	m.state.Phase = PhaseEnded
	m.state.PhaseEnd = time.Time{}
	if m.storage != nil {
		_ = m.storage.Clear()
	}
}

// EStop triggers an emergency stop. Robots are immediately disabled and the
// estop latches — it is cleared only by Reset (DESIGN §5, §10).
func (m *Machine) EStop() error {
	if m.state.Phase == PhaseEStop {
		return ErrAlreadyStop
	}
	if m.state.Phase == PhaseIdle {
		return fmt.Errorf("%w: no match to estop", ErrNoMatch)
	}
	m.state.EStopAt = m.clock.Now()
	m.state.Phase = PhaseEStop
	m.state.PhaseEnd = time.Time{}
	return nil
}

// Abort ends the current match immediately (e.g. field fault, replay needed).
// Returns to PhaseEnded; the operator can then Load a new match.
func (m *Machine) Abort() error {
	switch m.state.Phase {
	case PhaseIdle, PhaseEnded:
		return fmt.Errorf("%w: nothing to abort", ErrWrongPhase)
	}
	m.endMatch()
	return nil
}

// Reset returns the machine to Idle from any state. It is the only way to clear
// a latched EStop or a recovered Fault (DESIGN §5, §10), and it discards any
// failsafe snapshot. After Reset the operator may Load a fresh match (or replay).
func (m *Machine) Reset() {
	m.state = State{}
	if m.storage != nil {
		_ = m.storage.Clear()
	}
}

// Remaining reports how much time is left in the current timed phase, or zero if
// the current phase is untimed (idle, pre_match, transition-less ends, estop, fault,
// ended). It is derived purely from the local clock so it is unaffected by network
// state, and is the value displays interpolate from (DESIGN §7).
func (m *Machine) Remaining() time.Duration {
	if m.state.PhaseEnd.IsZero() {
		return 0
	}
	d := m.state.PhaseEnd.Sub(m.clock.Now())
	if d < 0 {
		return 0
	}
	return d
}

// State returns a copy of the current machine state.
func (m *Machine) State() State {
	return m.state
}

// Output returns the current derived robot enable output.
func (m *Machine) Output() Output {
	return m.state.Output()
}

// RestoreFailsafe loads a previously saved failsafe snapshot.
// The machine comes up in PhaseFault with robots disabled.
// Returns false if no snapshot exists.
func (m *Machine) RestoreFailsafe() (bool, error) {
	if m.storage == nil {
		return false, nil
	}
	snap, ok, err := m.storage.Load()
	if err != nil {
		return false, fmt.Errorf("failsafe restore: %w", err)
	}
	if !ok {
		return false, nil
	}
	m.state = snap
	m.state.Phase = PhaseFault
	m.state.PhaseEnd = time.Time{}
	return true, nil
}
