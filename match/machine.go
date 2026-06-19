package match

import (
	"errors"
	"sync"
	"time"
)

var (
	ErrMatchLive   = errors.New("a match is currently live")
	ErrNoMatch     = errors.New("no match loaded")
	ErrEstopped    = errors.New("field is emergency-stopped")
	ErrNotPreMatch = errors.New("match is not in pre-match phase")
)

// Clock abstracts time so the machine can be driven deterministically in tests.
type Clock interface {
	Now() time.Time
}

// RealClock is the production clock.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// Machine runs a single match authoritatively. Once Start is called it advances
// purely on its own clock, independent of the portal.
//
// Full phase sequence:
//
//	Alliance:    pre_match → countdown_auton → autonomous → [transition] → countdown_driver → driver → ended
//	SoloDriving: pre_match → countdown_auton → driver → ended
//	SoloCoding:  pre_match → countdown_auton → autonomous → ended
//
// Countdown phases are disabled. Robots see no signal difference between
// pre_match and a countdown — the countdown exists for displays and driver awareness.
type Machine struct {
	mu       sync.Mutex
	clock    Clock
	cfg      Config
	phase    Phase
	loaded   bool
	estopped bool
	startAt  time.Time
}

func New(clk Clock) *Machine {
	if clk == nil {
		clk = RealClock{}
	}
	return &Machine{clock: clk, phase: PhaseIdle}
}

// Load installs a match config and moves to PreMatch. Rejected if a match is live.
func (m *Machine) Load(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.isLiveLocked() {
		return ErrMatchLive
	}
	m.cfg = cfg
	m.loaded = true
	m.estopped = false
	m.startAt = time.Time{}
	m.phase = PhasePreMatch
	return nil
}

// Start begins the match, entering the autonomous countdown. Must be called from PreMatch.
func (m *Machine) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.loaded {
		return ErrNoMatch
	}
	if m.estopped {
		return ErrEstopped
	}
	if m.phase != PhasePreMatch {
		return ErrNotPreMatch
	}
	m.startAt = m.clock.Now()
	m.recomputeLocked()
	return nil
}

// Estop latches the field disabled. Only Reset clears it.
func (m *Machine) Estop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.estopped = true
	m.startAt = time.Time{}
	m.phase = PhaseEstop
}

// Abort ends the current match early without latching.
func (m *Machine) Abort() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.loaded && !m.estopped {
		m.startAt = time.Time{}
		m.phase = PhaseEnded
	}
}

// Reset clears back to idle, releasing any estop latch.
func (m *Machine) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = Config{}
	m.loaded = false
	m.estopped = false
	m.startAt = time.Time{}
	m.phase = PhaseIdle
}

// Tick recomputes phase against the current clock and returns derived state.
// The run loop calls this at ~50-100 Hz.
func (m *Machine) Tick() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recomputeLocked()
	return m.stateLocked()
}

// State returns the current derived state without advancing transitions.
func (m *Machine) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stateLocked()
}

// offsets computes the cumulative time boundaries for every phase.
// All durations are measured from startAt.
type offsets struct {
	ca time.Duration // end of countdown_auton
	a  time.Duration // end of autonomous
	t  time.Duration // end of transition
	cd time.Duration // end of countdown_driver
	d  time.Duration // end of driver (= total match duration)
}

func (m *Machine) computeOffsets() offsets {
	c := m.cfg
	var o offsets
	o.ca = c.CountdownAuton
	o.a = o.ca + c.Autonomous
	o.t = o.a + c.Transition
	o.cd = o.t + c.CountdownDriver
	o.d = o.cd + c.Driver
	return o
}

func (m *Machine) recomputeLocked() {
	if m.estopped {
		m.phase = PhaseEstop
		return
	}
	if m.startAt.IsZero() {
		return
	}
	elapsed := m.clock.Now().Sub(m.startAt)
	o := m.computeOffsets()

	switch m.cfg.Type {
	case Alliance:
		switch {
		case elapsed < o.ca:
			m.phase = PhaseCountdownAuton
		case elapsed < o.a:
			m.phase = PhaseAutonomous
		case elapsed < o.t:
			m.phase = PhaseTransition
		case elapsed < o.cd:
			m.phase = PhaseCountdownDriver
		case elapsed < o.d:
			m.phase = PhaseDriver
		default:
			m.endLocked()
		}

	case SoloDriving:
		// countdown_auton → driver (no autonomous period)
		switch {
		case elapsed < o.ca:
			m.phase = PhaseCountdownAuton
		case elapsed < o.ca+m.cfg.Driver:
			m.phase = PhaseDriver
		default:
			m.endLocked()
		}

	case SoloCoding:
		// countdown_auton → autonomous (no driver period)
		switch {
		case elapsed < o.ca:
			m.phase = PhaseCountdownAuton
		case elapsed < o.ca+m.cfg.Autonomous:
			m.phase = PhaseAutonomous
		default:
			m.endLocked()
		}

	default:
		m.endLocked()
	}
}

func (m *Machine) endLocked() {
	m.startAt = time.Time{}
	m.phase = PhaseEnded
}

func (m *Machine) stateLocked() State {
	s := State{MatchID: m.cfg.MatchID, Phase: m.phase}
	if m.startAt.IsZero() {
		return s
	}
	elapsed := m.clock.Now().Sub(m.startAt)
	o := m.computeOffsets()

	switch m.phase {
	case PhaseCountdownAuton:
		s.Enabled = false
		s.Mode = ModeNone
		s.CountdownRemaining = clamp(o.ca - elapsed)

	case PhaseAutonomous:
		s.Enabled = true
		s.Mode = ModeAutonomous
		switch m.cfg.Type {
		case Alliance:
			s.Remaining = clamp(o.a - elapsed)
		case SoloCoding:
			s.Remaining = clamp(o.ca + m.cfg.Autonomous - elapsed)
		}

	case PhaseTransition:
		// disabled, no remaining

	case PhaseCountdownDriver:
		s.Enabled = false
		s.Mode = ModeNone
		s.CountdownRemaining = clamp(o.cd - elapsed)

	case PhaseDriver:
		s.Enabled = true
		s.Mode = ModeDriver
		switch m.cfg.Type {
		case Alliance:
			s.Remaining = clamp(o.d - elapsed)
		case SoloDriving:
			s.Remaining = clamp(o.ca + m.cfg.Driver - elapsed)
		}
	}
	return s
}

func (m *Machine) isLiveLocked() bool {
	switch m.phase {
	case PhaseCountdownAuton, PhaseAutonomous, PhaseTransition,
		PhaseCountdownDriver, PhaseDriver:
		return true
	}
	return false
}

func clamp(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// Snapshot is the persistable state written to local disk so an unexpected
// Pi reboot never leaves robots enabled.
type Snapshot struct {
	Config          Config
	Phase           Phase
	StartAtUnixNano int64
	Estopped        bool
	Loaded          bool
}

func (m *Machine) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := Snapshot{
		Config:   m.cfg,
		Phase:    m.phase,
		Estopped: m.estopped,
		Loaded:   m.loaded,
	}
	if !m.startAt.IsZero() {
		snap.StartAtUnixNano = m.startAt.UnixNano()
	}
	return snap
}

// RestoreFailsafe rebuilds a Machine from a snapshot after a restart.
// It never re-enables robots: any live or estopped snapshot comes up
// disabled in PhaseFault with the match ID preserved for a replay decision.
func RestoreFailsafe(clk Clock, snap Snapshot) *Machine {
	m := New(clk)
	m.cfg = snap.Config
	m.loaded = snap.Loaded
	wasLive := snap.StartAtUnixNano != 0 || snap.Estopped
	switch {
	case wasLive:
		m.phase = PhaseFault
	case snap.Phase == PhaseEnded:
		m.phase = PhaseEnded
	case snap.Loaded:
		m.phase = PhasePreMatch
	default:
		m.phase = PhaseIdle
	}
	return m
}
