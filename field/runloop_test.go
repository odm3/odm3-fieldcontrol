package field_test

import (
	"errors"
	"testing"
	"time"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/driver"
	"github.com/odm3/e6events/fieldcontrol/field"
	"github.com/odm3/e6events/fieldcontrol/match"
	"github.com/odm3/e6events/fieldcontrol/transport"
)

type manualClock struct{ t time.Time }

func (c *manualClock) Now() time.Time          { return c.t }
func (c *manualClock) advance(d time.Duration) { c.t = c.t.Add(d) }

var epoch = time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

// harness wires a field RunLoop with a manual clock, a mock driver, and one end of
// an in-process pipe. The other end stands in for the portal.
type harness struct {
	clk    *manualClock
	mock   *driver.Mock
	rl     *field.RunLoop
	portal *transport.ChanConn
	fconn  *transport.ChanConn
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	clk := &manualClock{t: epoch}
	m := match.New(clk)
	mock := driver.NewMock()
	portalEnd, fieldEnd := transport.Pipe()
	rl := field.New("e6-test", m, mock, fieldEnd)
	return &harness{clk: clk, mock: mock, rl: rl, portal: portalEnd, fconn: fieldEnd}
}

func loadCmd(id string, mt match.Type) contract.Command {
	return contract.Command{
		Type:   contract.CmdLoad,
		Config: &contract.RunConfig{MatchID: id, Type: mt},
	}
}

func mustStep(t *testing.T, h *harness) contract.StateMsg {
	t.Helper()
	msg, err := h.rl.Step()
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	return msg
}

// TestFullAllianceMatchThroughControlPlane drives an alliance match (with countdown
// phases) via commands and the local clock, asserting the driver sees the right
// enable/mode at each phase.
func TestFullAllianceMatchThroughControlPlane(t *testing.T) {
	h := newHarness(t)

	if err := h.rl.HandleCommand(loadCmd("Q-1", match.Alliance)); err != nil {
		t.Fatal(err)
	}
	if err := h.rl.HandleCommand(contract.Command{Type: contract.CmdStart}); err != nil {
		t.Fatal(err)
	}

	// t=0: countdown_auton, disabled.
	msg := mustStep(t, h)
	if msg.Phase != match.PhaseCountdownAuton || msg.Enabled {
		t.Fatalf("countdown_auton: %+v", msg)
	}
	if h.mock.Last().Enabled {
		t.Fatalf("driver must be disabled in countdown: %+v", h.mock.Last())
	}

	// t=3s: autonomous, enabled.
	h.clk.advance(3 * time.Second)
	msg = mustStep(t, h)
	if msg.Phase != match.PhaseAutonomous || !msg.Enabled || msg.Mode != match.ModeAutonomous {
		t.Fatalf("autonomous: %+v", msg)
	}
	if last := h.mock.Last(); !last.Enabled || last.Mode != match.ModeAutonomous {
		t.Fatalf("driver in autonomous: %+v", last)
	}

	// t=18s: countdown_driver, disabled (no transition configured).
	h.clk.advance(15 * time.Second)
	msg = mustStep(t, h)
	if msg.Phase != match.PhaseCountdownDriver || msg.Enabled {
		t.Fatalf("countdown_driver: %+v", msg)
	}

	// t=21s: driver, enabled.
	h.clk.advance(3 * time.Second)
	msg = mustStep(t, h)
	if msg.Phase != match.PhaseDriver || !msg.Enabled || msg.Mode != match.ModeDriver {
		t.Fatalf("driver: %+v", msg)
	}

	// t=126s: ended.
	h.clk.advance(105 * time.Second)
	msg = mustStep(t, h)
	if msg.Phase != match.PhaseEnded || msg.Enabled {
		t.Fatalf("ended: %+v", msg)
	}
}

// TestEstopThroughRunLoop verifies Estop latches disabled and Reset clears it.
func TestEstopThroughRunLoop(t *testing.T) {
	h := newHarness(t)

	h.rl.HandleCommand(loadCmd("Q-2", match.Alliance))
	h.rl.HandleCommand(contract.Command{Type: contract.CmdStart})
	h.clk.advance(5 * time.Second) // into autonomous
	mustStep(t, h)

	if err := h.rl.HandleCommand(contract.Command{Type: contract.CmdEstop}); err != nil {
		t.Fatal(err)
	}
	msg := mustStep(t, h)
	if msg.Phase != match.PhaseEstop || msg.Enabled {
		t.Fatalf("estop: %+v", msg)
	}

	// Advancing the clock past autonomous must not re-enable.
	h.clk.advance(30 * time.Second)
	msg = mustStep(t, h)
	if msg.Phase != match.PhaseEstop || h.mock.Last().Enabled {
		t.Fatalf("estop must stay latched: msg=%+v driver=%+v", msg, h.mock.Last())
	}

	// Reset clears latch; load then works.
	if err := h.rl.HandleCommand(contract.Command{Type: contract.CmdReset}); err != nil {
		t.Fatal(err)
	}
	if err := h.rl.HandleCommand(loadCmd("Q-2R", match.Alliance)); err != nil {
		t.Fatalf("load after reset: %v", err)
	}
}

// TestRefuseStartWhenLinkDown: Pi refuses to start a match it cannot record.
func TestRefuseStartWhenLinkDown(t *testing.T) {
	h := newHarness(t)
	h.rl.HandleCommand(loadCmd("Q-3", match.Alliance))

	h.fconn.Drop() // control link goes down before Start

	err := h.rl.HandleCommand(contract.Command{Type: contract.CmdStart})
	if !errors.Is(err, field.ErrCannotRecord) {
		t.Fatalf("start with link down: got %v want ErrCannotRecord", err)
	}
	// Still in pre_match, robots never enabled.
	if msg := mustStep(t, h); msg.Phase != match.PhasePreMatch || msg.Enabled {
		t.Fatalf("must remain pre_match disabled: %+v", msg)
	}
}

// TestNetworkDropMidMatch: live match completes on local clock after portal drop.
func TestNetworkDropMidMatch(t *testing.T) {
	h := newHarness(t)
	h.rl.HandleCommand(loadCmd("Q-4", match.SoloDriving))
	h.rl.HandleCommand(contract.Command{Type: contract.CmdStart})

	// Skip countdown (3s), verify driver phase starts.
	h.clk.advance(3 * time.Second)
	msg := mustStep(t, h)
	if msg.Phase != match.PhaseDriver || !msg.Enabled {
		t.Fatalf("driver start: %+v", msg)
	}

	// Portal connection drops mid-match.
	h.portal.Drop()
	if h.fconn.Connected() {
		t.Fatal("field link should report disconnected after portal drop")
	}

	// Match continues purely on the local clock; Step does no network I/O.
	h.clk.advance(30 * time.Second)
	if msg := mustStep(t, h); msg.Phase != match.PhaseDriver {
		t.Fatalf("should still be driver mid-match: %+v", msg)
	}
	h.clk.advance(30*time.Second + time.Millisecond)
	if msg := mustStep(t, h); msg.Phase != match.PhaseEnded {
		t.Fatalf("match should complete on local clock despite drop: %+v", msg)
	}
}

// TestSoloCodingThroughRunLoop: autonomous-only match end to end.
func TestSoloCodingThroughRunLoop(t *testing.T) {
	h := newHarness(t)
	h.rl.HandleCommand(loadCmd("SC-1", match.SoloCoding))
	h.rl.HandleCommand(contract.Command{Type: contract.CmdStart})

	// Skip countdown.
	h.clk.advance(3 * time.Second)
	if msg := mustStep(t, h); msg.Phase != match.PhaseAutonomous || msg.Mode != match.ModeAutonomous {
		t.Fatalf("autonomous: %+v", msg)
	}
	h.clk.advance(60*time.Second + time.Millisecond)
	if msg := mustStep(t, h); msg.Phase != match.PhaseEnded {
		t.Fatalf("ended: %+v", msg)
	}
}

// TestCountdownRemainingSentUpstream verifies CountdownRemaining is in the StateMsg.
func TestCountdownRemainingSentUpstream(t *testing.T) {
	h := newHarness(t)
	h.rl.HandleCommand(loadCmd("Q-5", match.Alliance))
	h.rl.HandleCommand(contract.Command{Type: contract.CmdStart})

	msg := mustStep(t, h)
	if msg.Phase != match.PhaseCountdownAuton {
		t.Fatalf("expected countdown_auton, got %s", msg.Phase)
	}
	if msg.CountdownRemaining == 0 {
		t.Fatal("CountdownRemaining must be non-zero during countdown")
	}
}

func TestDeviceID(t *testing.T) {
	if got := field.DeviceID("  100000003a1b2c3d  "); got != "e6-100000003a1b2c3d" {
		t.Fatalf("device id: got %q", got)
	}
}
