package match

import (
	"testing"
	"time"
)

type manualClock struct{ t time.Time }

func (c *manualClock) Now() time.Time          { return c.t }
func (c *manualClock) advance(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *manualClock                   { return &manualClock{t: time.Unix(1_700_000_000, 0)} }

func assertPhase(t *testing.T, got State, phase Phase, enabled bool, mode Mode) {
	t.Helper()
	if got.Phase != phase {
		t.Errorf("phase = %v, want %v", got.Phase, phase)
	}
	if got.Enabled != enabled {
		t.Errorf("enabled = %v, want %v", got.Enabled, enabled)
	}
	if got.Mode != mode {
		t.Errorf("mode = %v, want %v", got.Mode, mode)
	}
}

// --- Alliance ---

func TestAllianceCountdownThenAuton(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-1"))
	m.Start()

	// t=0: countdown_auton, disabled
	assertPhase(t, m.State(), PhaseCountdownAuton, false, ModeNone)

	// countdown remaining should be ~3s
	if got := m.State().CountdownRemaining; got == 0 {
		t.Errorf("CountdownRemaining = 0 during countdown_auton")
	}

	// t=2s: still counting down
	clk.advance(2 * time.Second)
	assertPhase(t, m.Tick(), PhaseCountdownAuton, false, ModeNone)

	// t=3s: autonomous starts, enabled
	clk.advance(time.Second)
	assertPhase(t, m.Tick(), PhaseAutonomous, true, ModeAutonomous)
}

func TestAllianceFullProgression(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-2"))
	m.Start()

	// through countdown_auton (3s)
	clk.advance(3 * time.Second)
	assertPhase(t, m.Tick(), PhaseAutonomous, true, ModeAutonomous)

	// through autonomous (15s)
	clk.advance(15 * time.Second)
	assertPhase(t, m.Tick(), PhaseCountdownDriver, false, ModeNone)

	// countdown_driver remaining should be ~3s
	if got := m.Tick().CountdownRemaining; got == 0 {
		t.Errorf("CountdownRemaining = 0 during countdown_driver")
	}

	// through countdown_driver (3s)
	clk.advance(3 * time.Second)
	assertPhase(t, m.Tick(), PhaseDriver, true, ModeDriver)

	// through driver (105s)
	clk.advance(105 * time.Second)
	assertPhase(t, m.Tick(), PhaseEnded, false, ModeNone)
}

func TestAllianceTransitionPhase(t *testing.T) {
	clk := newClock()
	m := New(clk)
	cfg := AllianceConfig("Q-3")
	cfg.Transition = 2 * time.Second
	m.Load(cfg)
	m.Start()

	clk.advance(3 * time.Second)  // end of countdown_auton
	clk.advance(15 * time.Second) // end of autonomous → transition
	assertPhase(t, m.Tick(), PhaseTransition, false, ModeNone)

	clk.advance(2 * time.Second) // end of transition → countdown_driver
	assertPhase(t, m.Tick(), PhaseCountdownDriver, false, ModeNone)

	clk.advance(3 * time.Second) // end of countdown_driver → driver
	assertPhase(t, m.Tick(), PhaseDriver, true, ModeDriver)
}

// --- SoloDriving ---

func TestSoloDrivingCountdownThenDriver(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(SoloDrivingConfig("D-1"))
	m.Start()

	// countdown_auton first (reused as pre-run countdown)
	assertPhase(t, m.State(), PhaseCountdownAuton, false, ModeNone)

	clk.advance(3 * time.Second)
	assertPhase(t, m.Tick(), PhaseDriver, true, ModeDriver)

	clk.advance(59 * time.Second)
	assertPhase(t, m.Tick(), PhaseDriver, true, ModeDriver)

	clk.advance(2 * time.Second)
	assertPhase(t, m.Tick(), PhaseEnded, false, ModeNone)
}

// --- SoloCoding ---

func TestSoloCodingCountdownThenAuton(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(SoloCodingConfig("C-1"))
	m.Start()

	assertPhase(t, m.State(), PhaseCountdownAuton, false, ModeNone)

	clk.advance(3 * time.Second)
	assertPhase(t, m.Tick(), PhaseAutonomous, true, ModeAutonomous)

	clk.advance(61 * time.Second)
	assertPhase(t, m.Tick(), PhaseEnded, false, ModeNone)
}

func TestSoloCodingNoDriverCountdown(t *testing.T) {
	// SoloCoding has no driver period so countdown_driver must never appear
	clk := newClock()
	m := New(clk)
	m.Load(SoloCodingConfig("C-2"))
	m.Start()
	clk.advance(3 * time.Second)  // skip countdown
	clk.advance(60 * time.Second) // skip autonomous
	st := m.Tick()
	if st.Phase == PhaseCountdownDriver {
		t.Errorf("SoloCoding should never enter countdown_driver")
	}
	assertPhase(t, st, PhaseEnded, false, ModeNone)
}

// --- Estop ---

func TestEstopDuringCountdown(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-4"))
	m.Start()
	clk.advance(time.Second) // still in countdown
	assertPhase(t, m.Tick(), PhaseCountdownAuton, false, ModeNone)
	m.Estop()
	assertPhase(t, m.State(), PhaseEstop, false, ModeNone)
	// advancing time must not re-enable
	clk.advance(60 * time.Second)
	assertPhase(t, m.Tick(), PhaseEstop, false, ModeNone)
}

func TestEstopDuringAuton(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-5"))
	m.Start()
	clk.advance(5 * time.Second) // into autonomous
	m.Tick()
	m.Estop()
	assertPhase(t, m.State(), PhaseEstop, false, ModeNone)
	clk.advance(20 * time.Second)
	assertPhase(t, m.Tick(), PhaseEstop, false, ModeNone)
}

func TestEstopClearedByReset(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-6"))
	m.Start()
	m.Estop()
	if err := m.Start(); err == nil {
		t.Errorf("Start after estop should fail")
	}
	m.Reset()
	assertPhase(t, m.State(), PhaseIdle, false, ModeNone)
}

// --- Load / Start guards ---

func TestLoadRejectedWhenLive(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-7"))
	m.Start()
	if err := m.Load(AllianceConfig("Q-8")); err != ErrMatchLive {
		t.Errorf("Load while live = %v, want ErrMatchLive", err)
	}
}

func TestStartRequiresPreMatch(t *testing.T) {
	clk := newClock()
	m := New(clk)
	if err := m.Start(); err != ErrNoMatch {
		t.Errorf("Start with no match = %v, want ErrNoMatch", err)
	}
	m.Load(AllianceConfig("Q-9"))
	if err := m.Start(); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := m.Start(); err != ErrNotPreMatch {
		t.Errorf("double Start = %v, want ErrNotPreMatch", err)
	}
}

// --- Abort ---

func TestAbortDuringDriver(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-10"))
	m.Start()
	clk.advance(25 * time.Second) // into driver
	assertPhase(t, m.Tick(), PhaseDriver, true, ModeDriver)
	m.Abort()
	assertPhase(t, m.State(), PhaseEnded, false, ModeNone)
}

// --- Remaining countdown ---

func TestCountdownRemainingCountsDown(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-11"))
	m.Start()
	if got := m.State().CountdownRemaining; got != 3*time.Second {
		t.Errorf("CountdownRemaining at t=0 = %v, want 3s", got)
	}
	clk.advance(time.Second)
	if got := m.Tick().CountdownRemaining; got != 2*time.Second {
		t.Errorf("CountdownRemaining at t=1 = %v, want 2s", got)
	}
}

func TestDriverCountdownRemaining(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-12"))
	m.Start()
	clk.advance(3 * time.Second)  // skip countdown_auton
	clk.advance(15 * time.Second) // skip autonomous → countdown_driver
	assertPhase(t, m.Tick(), PhaseCountdownDriver, false, ModeNone)
	if got := m.State().CountdownRemaining; got != 3*time.Second {
		t.Errorf("driver CountdownRemaining = %v, want 3s", got)
	}
}

// --- Failsafe restore ---

func TestRestoreFromLiveMatch(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-13"))
	m.Start()
	clk.advance(10 * time.Second)
	m.Tick()
	snap := m.Snapshot()
	recovered := RestoreFailsafe(newClock(), snap)
	st := recovered.State()
	if st.Phase != PhaseFault {
		t.Errorf("recovered phase = %v, want PhaseFault", st.Phase)
	}
	if st.Enabled {
		t.Errorf("recovered match must not be enabled")
	}
	if st.MatchID != "Q-13" {
		t.Errorf("recovered MatchID = %q, want Q-13", st.MatchID)
	}
}

func TestRestoreFromEnded(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(SoloDrivingConfig("D-2"))
	m.Start()
	clk.advance(70 * time.Second)
	m.Tick()
	snap := m.Snapshot()
	recovered := RestoreFailsafe(newClock(), snap)
	if recovered.State().Phase != PhaseEnded {
		t.Errorf("recovered phase = %v, want PhaseEnded", recovered.State().Phase)
	}
}

// --- Network drop simulation ---

func TestNetworkDropDoesNotAffectLiveMatch(t *testing.T) {
	clk := newClock()
	m := New(clk)
	m.Load(AllianceConfig("Q-14"))
	m.Start()
	// Simulate portal gone: only local tick runs for the entire match duration
	// Alliance total = 3 + 15 + 3 + 105 = 126s
	for i := 0; i < 130; i++ {
		clk.advance(time.Second)
		m.Tick()
	}
	assertPhase(t, m.State(), PhaseEnded, false, ModeNone)
}
