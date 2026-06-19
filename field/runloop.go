// Package field is the Pi-side run loop. It binds the authoritative match state
// machine to a hardware driver and the portal's control plane. The live match is
// Pi-authoritative: it advances on the local clock, so a dropped control-plane
// connection mid-match does not affect it (DESIGN §6, §10).
package field

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/driver"
	"github.com/odm3/e6events/fieldcontrol/match"
	"github.com/odm3/e6events/fieldcontrol/transport"
)

// ErrCannotRecord is returned when a Start is requested but the control link is
// down, so the match could not be recorded. The Pi refuses to start a match it
// cannot record (DESIGN §10).
var ErrCannotRecord = errors.New("field: control link down, refusing to start an unrecordable match")

// RunLoop drives one field.
type RunLoop struct {
	deviceID string
	machine  *match.Machine
	drv      driver.Driver
	conn     transport.Conn

	mu      sync.Mutex
	lastOut match.Output
}

// New builds a RunLoop. The machine should already be constructed with the same
// Clock used everywhere on this Pi (RealClock in production, a manual clock in
// tests) and a failsafe Storer so a reboot recovers into fault (DESIGN §10).
func New(deviceID string, m *match.Machine, drv driver.Driver, conn transport.Conn) *RunLoop {
	return &RunLoop{deviceID: deviceID, machine: m, drv: drv, conn: conn}
}

// HandleCommand applies a single control-plane command to the machine. It is the
// only place commands mutate the machine; Run funnels every command through here on
// the run-loop goroutine so the machine is never touched concurrently.
func (r *RunLoop) HandleCommand(cmd contract.Command) error {
	switch cmd.Type {
	case contract.CmdLoad:
		if cmd.Config == nil {
			return errors.New("field: load command missing config")
		}
		return r.machine.Load(match.MatchID(cmd.Config.MatchID), cmd.Config.Type)
	case contract.CmdStart:
		// Refuse to start a match we cannot record (DESIGN §10). A live match,
		// once started, keeps running even if the link later drops.
		if !r.conn.Connected() {
			return ErrCannotRecord
		}
		return r.machine.Start()
	case contract.CmdEstop:
		return r.machine.EStop()
	case contract.CmdAbort:
		return r.machine.Abort()
	case contract.CmdReset:
		r.machine.Reset()
		return nil
	default:
		return errors.New("field: unknown command " + string(cmd.Type))
	}
}

// Step advances the local-clock match by one tick, reasserts the derived output to
// the driver as a watchdog heartbeat, and returns the state to publish upstream.
// It performs no network I/O, so it keeps working through a control-link drop.
func (r *RunLoop) Step() (contract.StateMsg, error) {
	r.machine.Tick()
	out := r.machine.Output()

	// Reassert every tick (idempotent) so the hardware always reflects the
	// authoritative state, even after a transient driver hiccup.
	if err := r.drv.Apply(out); err != nil {
		return contract.StateMsg{}, err
	}
	r.mu.Lock()
	r.lastOut = out
	r.mu.Unlock()

	st := r.machine.State()
	return contract.StateMsg{
		DeviceID:  r.deviceID,
		MatchID:   string(st.ID),
		Phase:     st.Phase,
		Mode:      out.Mode,
		Enabled:   out.Enabled,
		Remaining: r.machine.Remaining(),
	}, nil
}

// LastOutput returns the most recently applied output (for observability/tests).
func (r *RunLoop) LastOutput() match.Output {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastOut
}

// Run executes the loop until ctx is cancelled. Commands arriving on the control
// link are funnelled onto the loop goroutine; a tick fires every `tick` to advance
// the match and publish state. A control-link drop ends command reception and best-
// effort publishing, but the tick — and therefore the live match — keeps running on
// the local clock (DESIGN §6, §10).
func (r *RunLoop) Run(ctx context.Context, tick time.Duration) error {
	cmds := make(chan contract.Command, 8)
	go func() {
		for {
			env, err := r.conn.Recv()
			if err != nil {
				return // link dropped; loop keeps advancing locally
			}
			cmd, derr := contract.DecodeCommand(env)
			if derr != nil {
				continue
			}
			select {
			case cmds <- cmd:
			case <-ctx.Done():
				return
			}
		}
	}()

	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case cmd := <-cmds:
			_ = r.HandleCommand(cmd) // command errors are surfaced via published state
		case <-t.C:
			msg, err := r.Step()
			if err != nil {
				// A driver fault is serious but must not crash the loop; the
				// machine stays authoritative and we keep trying.
				continue
			}
			if env, encErr := contract.EncodeState(msg); encErr == nil {
				_ = r.conn.Send(env) // best effort; drops are tolerated
			}
		}
	}
}
