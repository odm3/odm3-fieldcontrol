// Package field is the Pi-side run loop. It binds the authoritative match state
// machine to a hardware driver and the portal's control plane. The live match is
// Pi-authoritative: it advances on the local clock, so a dropped control-plane
// connection mid-match does not affect it (DESIGN §7, §11).
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
// cannot record (DESIGN §11).
var ErrCannotRecord = errors.New("field: control link down, refusing to start an unrecordable match")

// Snapshotter persists failsafe snapshots before robots go live. An unexpected Pi
// reboot reads this snapshot and calls match.RestoreFailsafe (DESIGN §11).
type Snapshotter interface {
	Save(match.Snapshot) error
	Clear() error
}

// RunLoop drives one field.
type RunLoop struct {
	deviceID    string
	machine     *match.Machine
	drv         driver.Driver
	conn        transport.Conn
	snapshotter Snapshotter

	mu     sync.Mutex
	lastSt match.State
}

// New builds a RunLoop. snapshotter may be nil (no failsafe persistence).
func New(deviceID string, m *match.Machine, drv driver.Driver, conn transport.Conn) *RunLoop {
	return &RunLoop{deviceID: deviceID, machine: m, drv: drv, conn: conn}
}

// WithSnapshotter attaches failsafe persistence to the run loop.
func (r *RunLoop) WithSnapshotter(s Snapshotter) *RunLoop {
	r.snapshotter = s
	return r
}

// HandleCommand applies a single control-plane command to the machine. It is
// the only place commands mutate the machine; Run funnels every received command
// through here on the run-loop goroutine.
func (r *RunLoop) HandleCommand(cmd contract.Command) error {
	switch cmd.Type {
	case contract.CmdLoad:
		if cmd.Config == nil {
			return errors.New("field: load command missing config")
		}
		return r.machine.Load(cmd.Config.MatchConfig())
	case contract.CmdStart:
		// Refuse to start a match we cannot record (DESIGN §11). A live match,
		// once started, keeps running even if the link later drops.
		if !r.conn.Connected() {
			return ErrCannotRecord
		}
		// Persist failsafe snapshot before robots go live.
		if r.snapshotter != nil {
			snap := r.machine.Snapshot()
			// Mark as live so a reboot recovers to fault.
			snap.StartAtUnixNano = 1 // sentinel; real value written by machine.Start
			if err := r.snapshotter.Save(snap); err != nil {
				return err
			}
		}
		return r.machine.Start()
	case contract.CmdEstop:
		r.machine.Estop()
		return nil
	case contract.CmdAbort:
		r.machine.Abort()
		return nil
	case contract.CmdReset:
		r.machine.Reset()
		if r.snapshotter != nil {
			_ = r.snapshotter.Clear()
		}
		return nil
	default:
		return errors.New("field: unknown command " + string(cmd.Type))
	}
}

// Step advances the match by one tick, reasserts the derived signals to the
// driver as a watchdog heartbeat, and returns the state to publish upstream.
// It performs no network I/O, so it keeps working through a control-link drop.
func (r *RunLoop) Step() (contract.StateMsg, error) {
	st := r.machine.Tick()

	// Reassert every tick (idempotent watchdog).
	if err := r.drv.Apply(st.Enabled, st.Mode); err != nil {
		return contract.StateMsg{}, err
	}

	// Clear failsafe snapshot when the match ends normally.
	if st.Phase == match.PhaseEnded && r.snapshotter != nil {
		_ = r.snapshotter.Clear()
	}

	r.mu.Lock()
	r.lastSt = st
	r.mu.Unlock()

	return contract.StateMsg{
		DeviceID:           r.deviceID,
		MatchID:            st.MatchID,
		Phase:              st.Phase,
		Mode:               st.Mode,
		Enabled:            st.Enabled,
		Remaining:          st.Remaining,
		CountdownRemaining: st.CountdownRemaining,
	}, nil
}

// LastState returns the most recently computed match state.
func (r *RunLoop) LastState() match.State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastSt
}

// Run executes the loop until ctx is cancelled. Commands arriving on the control
// link are funnelled onto the loop goroutine; a tick fires every `tick` to advance
// the match and publish state. A control-link drop ends command reception and
// best-effort publishing, but the tick — and therefore the live match — keeps
// running on the local clock (DESIGN §7, §11).
func (r *RunLoop) Run(ctx context.Context, tick time.Duration) error {
	cmds := make(chan contract.Command, 8)
	go func() {
		for {
			env, err := r.conn.Recv()
			if err != nil {
				return
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
			_ = r.HandleCommand(cmd)
		case <-t.C:
			msg, err := r.Step()
			if err != nil {
				continue
			}
			if env, encErr := contract.EncodeState(msg); encErr == nil {
				_ = r.conn.Send(env)
			}
		}
	}
}
