package match_test

import (
	"testing"
	"time"

	"github.com/odm3/odm3-fieldcontrol/match"
)

// manualClock is a Clock that advances only when told to, for deterministic tests.
type manualClock struct{ t time.Time }

func newClock(t time.Time) *manualClock     { return &manualClock{t: t} }
func (c *manualClock) Now() time.Time       { return c.t }
func (c *manualClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

var epoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

func newMachine(c *manualClock) (*match.Machine, *match.MemStorer) {
	store := &match.MemStorer{}
	m := match.New(c, store)
	return m, store
}

func assertPhase(t *testing.T, m *match.Machine, want match.Phase) {
	t.Helper()
	if got := m.State().Phase; got != want {
		t.Fatalf("phase: got %s, want %s", got, want)
	}
}

func assertOutput(t *testing.T, m *match.Machine, enabled bool, mode match.Mode) {
	t.Helper()
	out := m.Output()
	if out.Enabled != enabled || out.Mode != mode {
		t.Fatalf("output: got {enabled:%v mode:%s}, want {enabled:%v mode:%s}",
			out.Enabled, out.Mode, enabled, mode)
	}
}

// TestAllianceFullFlow exercises the complete Alliance match flow.
func TestAllianceFullFlow(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	// Idle → PreMatch
	assertPhase(t, m, match.PhaseIdle)
	assertOutput(t, m, false, match.ModeDisabled)

	if err := m.Load("Q-001", match.Alliance); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhasePreMatch)
	assertOutput(t, m, false, match.ModeDisabled)

	// PreMatch → Autonomous
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhaseAutonomous)
	assertOutput(t, m, true, match.ModeAutonomous)

	// Mid-autonomous tick — should stay autonomous
	c.Advance(14 * time.Second)
	m.Tick()
	assertPhase(t, m, match.PhaseAutonomous)

	// Autonomous expires → Transition
	c.Advance(1*time.Second + 1*time.Millisecond)
	m.Tick()
	assertPhase(t, m, match.PhaseTransition)
	assertOutput(t, m, false, match.ModeDisabled)

	// Transition expires → Driver
	c.Advance(transitionDur)
	m.Tick()
	assertPhase(t, m, match.PhaseDriver)
	assertOutput(t, m, true, match.ModeDriver)

	// Mid-driver — stays driver
	c.Advance(50 * time.Second)
	m.Tick()
	assertPhase(t, m, match.PhaseDriver)

	// Driver expires → Ended
	c.Advance(55*time.Second + 1*time.Millisecond)
	m.Tick()
	assertPhase(t, m, match.PhaseEnded)
	assertOutput(t, m, false, match.ModeDisabled)
}

// TestSoloDrivingFullFlow: no autonomous phase.
func TestSoloDrivingFullFlow(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	if err := m.Load("SD-001", match.SoloDriving); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhaseDriver)
	assertOutput(t, m, true, match.ModeDriver)

	c.Advance(60*time.Second + 1*time.Millisecond)
	m.Tick()
	assertPhase(t, m, match.PhaseEnded)
	assertOutput(t, m, false, match.ModeDisabled)
}

// TestSoloCodingFullFlow: no driver phase.
func TestSoloCodingFullFlow(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	if err := m.Load("SC-001", match.SoloCoding); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhaseAutonomous)
	assertOutput(t, m, true, match.ModeAutonomous)

	c.Advance(60*time.Second + 1*time.Millisecond)
	m.Tick()
	assertPhase(t, m, match.PhaseEnded)
	assertOutput(t, m, false, match.ModeDisabled)
}

// TestEStopDuringAutonomous verifies estop latches and subsequent ticks don't re-enable.
func TestEStopDuringAutonomous(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	m.Load("Q-002", match.Alliance)
	m.Start()
	assertPhase(t, m, match.PhaseAutonomous)

	if err := m.EStop(); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhaseEStop)
	assertOutput(t, m, false, match.ModeDisabled)

	// Even if clock advances past autonomous expiry, estop holds.
	c.Advance(20 * time.Second)
	m.Tick()
	assertPhase(t, m, match.PhaseEStop)
	assertOutput(t, m, false, match.ModeDisabled)

	// Second estop returns error.
	if err := m.EStop(); err != match.ErrAlreadyStop {
		t.Fatalf("expected ErrAlreadyStop, got %v", err)
	}

	// Load new match clears estop.
	if err := m.Load("Q-002R", match.Alliance); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhasePreMatch)
}

// TestEStopDuringDriver verifies estop latches during driver phase.
func TestEStopDuringDriver(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	m.Load("SD-002", match.SoloDriving)
	m.Start()
	assertPhase(t, m, match.PhaseDriver)

	c.Advance(30 * time.Second)
	m.Tick()
	assertPhase(t, m, match.PhaseDriver) // still running

	m.EStop()
	assertPhase(t, m, match.PhaseEStop)
	assertOutput(t, m, false, match.ModeDisabled)

	// Clock advancing does not change phase.
	c.Advance(40 * time.Second)
	m.Tick()
	assertPhase(t, m, match.PhaseEStop)
}

// TestAbort verifies mid-match abort goes to Ended.
func TestAbort(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	m.Load("Q-003", match.Alliance)
	m.Start()
	assertPhase(t, m, match.PhaseAutonomous)

	if err := m.Abort(); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhaseEnded)
	assertOutput(t, m, false, match.ModeDisabled)

	// Abort from idle/ended is an error.
	if err := m.Abort(); err == nil {
		t.Fatal("expected error aborting from ended")
	}
}

// TestAbortFromPreMatch verifies abort works before match starts.
func TestAbortFromPreMatch(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	m.Load("Q-004", match.Alliance)
	assertPhase(t, m, match.PhasePreMatch)

	if err := m.Abort(); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhaseEnded)
}

// TestFailsafeRestore verifies snapshot is written on Start and restored on reboot.
func TestFailsafeRestore(t *testing.T) {
	c := newClock(epoch)
	store := &match.MemStorer{}
	m := match.New(c, store)

	m.Load("Q-005", match.Alliance)
	m.Start()

	// Simulate reboot: create a new machine with the same store.
	m2 := match.New(c, store)
	restored, err := m2.RestoreFailsafe()
	if err != nil {
		t.Fatal(err)
	}
	if !restored {
		t.Fatal("expected failsafe to be restored")
	}

	s := m2.State()
	if s.Phase != match.PhaseFault {
		t.Fatalf("expected fault phase, got %s", s.Phase)
	}
	if s.ID != "Q-005" {
		t.Fatalf("expected match ID Q-005, got %s", s.ID)
	}
	assertOutput(t, m2, false, match.ModeDisabled)
}

// TestFailsafeRestoreNoSnapshot verifies that with no snapshot, RestoreFailsafe returns false.
func TestFailsafeRestoreNoSnapshot(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	restored, err := m.RestoreFailsafe()
	if err != nil {
		t.Fatal(err)
	}
	if restored {
		t.Fatal("expected no restore with empty store")
	}
	assertPhase(t, m, match.PhaseIdle)
}

// TestFailsafeClearedOnMatchEnd verifies snapshot is cleared when match ends normally.
func TestFailsafeClearedOnMatchEnd(t *testing.T) {
	c := newClock(epoch)
	store := &match.MemStorer{}
	m := match.New(c, store)

	m.Load("Q-006", match.SoloDriving)
	m.Start()

	c.Advance(60*time.Second + 1*time.Millisecond)
	m.Tick()
	assertPhase(t, m, match.PhaseEnded)

	// New machine should find no snapshot.
	m2 := match.New(c, store)
	restored, err := m2.RestoreFailsafe()
	if err != nil {
		t.Fatal(err)
	}
	if restored {
		t.Fatal("snapshot should have been cleared after normal match end")
	}
}

// TestNetworkDropCase verifies the match progresses on the local clock alone,
// without any external portal calls. Advances the clock past all phases and
// verifies transitions happen on Tick() only.
func TestNetworkDropCase(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	m.Load("Q-007", match.Alliance)
	m.Start()
	assertPhase(t, m, match.PhaseAutonomous)

	// Advance past auto expiry — no portal interaction, just clock + Tick.
	c.Advance(15*time.Second + 1*time.Millisecond)
	m.Tick()
	assertPhase(t, m, match.PhaseTransition)

	c.Advance(transitionDur)
	m.Tick()
	assertPhase(t, m, match.PhaseDriver)

	c.Advance(105*time.Second + 1*time.Millisecond)
	m.Tick()
	assertPhase(t, m, match.PhaseEnded)
}

// TestLoadClearsEStop verifies that loading a new match after estop resets state.
func TestLoadClearsEStop(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	m.Load("Q-008", match.Alliance)
	m.Start()
	m.EStop()
	assertPhase(t, m, match.PhaseEStop)

	if err := m.Load("Q-008R", match.SoloDriving); err != nil {
		t.Fatal(err)
	}
	assertPhase(t, m, match.PhasePreMatch)
	if s := m.State(); s.ID != "Q-008R" {
		t.Fatalf("expected new match ID, got %s", s.ID)
	}
}

// TestEStopFromIdle errors gracefully.
func TestEStopFromIdle(t *testing.T) {
	c := newClock(epoch)
	m, _ := newMachine(c)

	if err := m.EStop(); err == nil {
		t.Fatal("expected error estopping from idle")
	}
	assertPhase(t, m, match.PhaseIdle)
}

// transitionDur is the exported constant used in tests.
const transitionDur = 5 * time.Second
