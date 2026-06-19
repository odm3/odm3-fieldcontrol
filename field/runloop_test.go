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
func (c *manualClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

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
	m := match.New(clk, &match.MemStorer{})
	mock := driver.NewMock()
	portalEnd, fieldEnd := transport.Pipe()
	rl := field.New("e6-test", m, mock, fieldEnd)
	return &harness{clk: clk, mock: mock, rl: rl, portal: portalEnd, fconn: fieldEnd}
}

func load(t *testing.T, h *harness, id string, mt match.MatchType) {
	t.Helper()
	if err := h.rl.HandleCommand(contract.Command{
		Type:   contract.CmdLoad,
		Config: &contract.RunConfig{MatchID: id, Type: mt},
	}); err != nil {
		t.Fatalf("load: %v", err)
	}
}

func start(t *testing.T, h *harness) {
	t.Helper()
	if err := h.rl.HandleCommand(contract.Command{Type: contract.CmdStart}); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func step(t *testing.T, h *harness) contract.StateMsg {
	t.Helper()
	msg, err := h.rl.Step()
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	return msg
}

// TestFullMatchThroughControlPlane drives an alliance match via commands and the
// local clock, asserting the driver sees the right enable/mode at each phase.
func TestFullMatchThroughControlPlane(t *testing.T) {
	h := newHarness(t)
	load(t, h, "Q-1", match.Alliance)
	start(t, h)

	msg := step(t, h)
	if msg.Phase != match.PhaseAutonomous || !msg.Enabled || msg.Mode != match.ModeAutonomous {
		t.Fatalf("autonomous: %+v", msg)
	}
	if last := h.mock.Last(); !last.Enabled || last.Mode != match.ModeAutonomous {
		t.Fatalf("driver in autonomous: %+v", last)
	}

	// Autonomous → transition (disabled).
	h.clk.Advance(15*time.Second + time.Millisecond)
	msg = step(t, h)
	if msg.Phase != match.PhaseTransition || msg.Enabled {
		t.Fatalf("transition: %+v", msg)
	}
	if h.mock.Last().Enabled {
		t.Fatalf("driver must be disabled in transition: %+v", h.mock.Last())
	}

	// Transition → driver.
	h.clk.Advance(5 * time.Second)
	msg = step(t, h)
	if msg.Phase != match.PhaseDriver || !msg.Enabled || msg.Mode != match.ModeDriver {
		t.Fatalf("driver: %+v", msg)
	}

	// Driver → ended.
	h.clk.Advance(105*time.Second + time.Millisecond)
	msg = step(t, h)
	if msg.Phase != match.PhaseEnded || msg.Enabled {
		t.Fatalf("ended: %+v", msg)
	}
}

// TestEstopThroughRunLoop verifies estop latches the driver disabled through the loop.
func TestEstopThroughRunLoop(t *testing.T) {
	h := newHarness(t)
	load(t, h, "Q-2", match.Alliance)
	start(t, h)
	step(t, h)

	if err := h.rl.HandleCommand(contract.Command{Type: contract.CmdEstop}); err != nil {
		t.Fatal(err)
	}
	msg := step(t, h)
	if msg.Phase != match.PhaseEStop || msg.Enabled {
		t.Fatalf("estop: %+v", msg)
	}

	// Advancing the clock past autonomous must not re-enable.
	h.clk.Advance(30 * time.Second)
	msg = step(t, h)
	if msg.Phase != match.PhaseEStop || h.mock.Last().Enabled {
		t.Fatalf("estop must stay latched disabled: msg=%+v driver=%+v", msg, h.mock.Last())
	}

	// Reset clears, then a fresh load works.
	if err := h.rl.HandleCommand(contract.Command{Type: contract.CmdReset}); err != nil {
		t.Fatal(err)
	}
	load(t, h, "Q-2R", match.Alliance)
}

// TestRefuseStartWhenLinkDown is the "WS drop between matches" case (DESIGN §10):
// the Pi refuses to start a match it cannot record.
func TestRefuseStartWhenLinkDown(t *testing.T) {
	h := newHarness(t)
	load(t, h, "Q-3", match.Alliance)

	h.fconn.Drop() // control link goes down before start

	err := h.rl.HandleCommand(contract.Command{Type: contract.CmdStart})
	if !errors.Is(err, field.ErrCannotRecord) {
		t.Fatalf("start with link down: got %v want ErrCannotRecord", err)
	}
	// Still in pre_match, robots never enabled.
	if msg := step(t, h); msg.Phase != match.PhasePreMatch || msg.Enabled {
		t.Fatalf("must remain pre_match disabled: %+v", msg)
	}
}

// TestNetworkDropMidMatch is the critical case: a live match completes on the local
// clock even after the control link drops (DESIGN §6, §10). No portal interaction
// happens after the drop.
func TestNetworkDropMidMatch(t *testing.T) {
	h := newHarness(t)
	load(t, h, "Q-4", match.SoloDriving) // 60s driver only
	start(t, h)

	msg := step(t, h)
	if msg.Phase != match.PhaseDriver || !msg.Enabled {
		t.Fatalf("driver start: %+v", msg)
	}

	// Portal connection drops mid-match.
	h.portal.Drop()
	if h.fconn.Connected() {
		t.Fatal("field link should report disconnected after portal drop")
	}

	// Match continues purely on the local clock; Step does no network I/O.
	h.clk.Advance(30 * time.Second)
	if msg := step(t, h); msg.Phase != match.PhaseDriver {
		t.Fatalf("should still be in driver mid-match: %+v", msg)
	}
	h.clk.Advance(30*time.Second + time.Millisecond)
	if msg := step(t, h); msg.Phase != match.PhaseEnded {
		t.Fatalf("match should complete on local clock despite link drop: %+v", msg)
	}
}

// TestSoloCodingThroughRunLoop covers the autonomous-only match end to end.
func TestSoloCodingThroughRunLoop(t *testing.T) {
	h := newHarness(t)
	load(t, h, "SC-1", match.SoloCoding)
	start(t, h)

	if msg := step(t, h); msg.Phase != match.PhaseAutonomous || msg.Mode != match.ModeAutonomous {
		t.Fatalf("autonomous: %+v", msg)
	}
	h.clk.Advance(60*time.Second + time.Millisecond)
	if msg := step(t, h); msg.Phase != match.PhaseEnded {
		t.Fatalf("ended: %+v", msg)
	}
}

func TestDeviceID(t *testing.T) {
	if got := field.DeviceID("  100000003a1b2c3d  "); got != "e6-100000003a1b2c3d" {
		t.Fatalf("device id: got %q", got)
	}
}
