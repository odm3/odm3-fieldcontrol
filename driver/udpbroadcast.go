package driver

import (
	"context"
	"encoding/binary"
	"net"
	"time"

	"github.com/odm3/e6events/fieldcontrol/match"
)

// Open field control protocol — UDP broadcast (DESIGN §12).
//
// The field Pi broadcasts an 8-byte packet every 50ms on port 9800 of the field
// subnet. Any controller platform (REV Control Hub, custom electronics) that can
// open a UDP socket participates without proprietary hardware.
//
//	Offset  Size  Description
//	0-3     4B    Magic: "ODM3" (0x4F 0x44 0x4D 0x33)
//	4       1B    Version: 0x01
//	5       1B    Flags: bit0=enabled, bit1=mode(1=driver,0=auton), bit2=estop
//	6-7     2B    Time remaining (ms), big-endian uint16, 0 when disabled
const (
	UDPPort     = 9800
	udpInterval = 50 * time.Millisecond
)

var udpMagic = [4]byte{0x4F, 0x44, 0x4D, 0x33}

// Broadcaster sends the ODM3 open protocol UDP packets on the field subnet.
// It runs as a background goroutine and is driven by the run loop via Update.
type Broadcaster struct {
	conn    *net.UDPConn
	stateCh chan broadcastState
}

type broadcastState struct {
	enabled   bool
	mode      match.Mode
	estopped  bool
	remaining time.Duration
}

// NewBroadcaster creates a broadcaster that sends to the given broadcast address
// (e.g. "10.20.0.255:9800"). The caller must call Run to start broadcasting.
func NewBroadcaster(addr string) (*Broadcaster, error) {
	raddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp4", nil, raddr)
	if err != nil {
		return nil, err
	}
	return &Broadcaster{conn: conn, stateCh: make(chan broadcastState, 1)}, nil
}

// Update delivers a new state for the broadcaster to send. It is safe to call
// from the run loop goroutine. Non-blocking: drops the update if the channel is full.
func (b *Broadcaster) Update(s broadcastState) {
	select {
	case b.stateCh <- s:
	default:
		// Replace stale state.
		select {
		case <-b.stateCh:
		default:
		}
		b.stateCh <- s
	}
}

// UpdateFromMatchState is a convenience wrapper that converts a match.State to a
// broadcastState. estopped is inferred from the PhaseEstop phase.
func (b *Broadcaster) UpdateFromMatchState(s match.State) {
	b.Update(broadcastState{
		enabled:   s.Enabled,
		mode:      s.Mode,
		estopped:  s.Phase == match.PhaseEstop,
		remaining: s.Remaining,
	})
}

// Run broadcasts state every 50ms until ctx is cancelled. Silence means disabled,
// so if this goroutine exits, robots conservatively treat themselves as disabled
// after 500ms (DESIGN §12).
func (b *Broadcaster) Run(ctx context.Context) error {
	defer b.conn.Close()
	ticker := time.NewTicker(udpInterval)
	defer ticker.Stop()

	var cur broadcastState
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case cur = <-b.stateCh:
		case <-ticker.C:
			if err := b.send(cur); err != nil {
				// Best effort; a transient send failure should not crash the loop.
				continue
			}
		}
	}
}

func (b *Broadcaster) send(s broadcastState) error {
	var pkt [8]byte
	copy(pkt[:4], udpMagic[:])
	pkt[4] = 0x01 // version

	var flags byte
	if s.enabled {
		flags |= 0x01
	}
	if s.mode == match.ModeDriver {
		flags |= 0x02
	}
	if s.estopped {
		flags |= 0x04
	}
	pkt[5] = flags

	var ms uint16
	if s.enabled && s.remaining > 0 {
		ms = uint16(s.remaining.Milliseconds())
	}
	binary.BigEndian.PutUint16(pkt[6:], ms)

	_, err := b.conn.Write(pkt[:])
	return err
}
