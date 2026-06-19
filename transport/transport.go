// Package transport moves contract.Envelopes between nodes. The Conn abstraction
// is deliberately small so the production transport (a WebSocket text-frame
// adapter) and the deterministic in-process transport used in tests and the demo
// implement the same interface — mirroring the "keep the abstraction clean so the
// transport can be swapped" principle the design applies to the view-plane pub/sub
// (DESIGN §6).
package transport

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/odm3/e6events/fieldcontrol/contract"
)

// ErrClosed is returned by Send/Recv once a connection has dropped or closed.
var ErrClosed = errors.New("transport: connection closed")

// Conn is one end of a bidirectional, ordered, message-framed link.
type Conn interface {
	// Send delivers an envelope to the peer. It returns ErrClosed if the link is down.
	Send(contract.Envelope) error
	// Recv blocks until an envelope arrives or the link drops (ErrClosed).
	Recv() (contract.Envelope, error)
	// Connected reports whether the link is currently usable. The field run loop
	// uses this to refuse to start a match it cannot record (DESIGN §10).
	Connected() bool
	// Close drops this connection (and, for the in-process pipe, its peer).
	Close() error
}

// closeState is shared by both ends of a pipe so dropping either end tears down both.
type closeState struct {
	once sync.Once
	done chan struct{}
	flag atomic.Bool
}

func (s *closeState) close() {
	s.once.Do(func() {
		s.flag.Store(true)
		close(s.done)
	})
}

// ChanConn is an in-process Conn backed by channels. It is safe for one sender and
// one receiver goroutine per end.
type ChanConn struct {
	send  chan contract.Envelope
	recv  chan contract.Envelope
	state *closeState
}

// Pipe returns the two connected ends of an in-process link. Dropping or closing
// either end disconnects both, simulating a network drop.
func Pipe() (*ChanConn, *ChanConn) {
	ab := make(chan contract.Envelope, 32)
	ba := make(chan contract.Envelope, 32)
	st := &closeState{done: make(chan struct{})}
	a := &ChanConn{send: ab, recv: ba, state: st}
	b := &ChanConn{send: ba, recv: ab, state: st}
	return a, b
}

// Send implements Conn.
func (c *ChanConn) Send(e contract.Envelope) error {
	if c.state.flag.Load() {
		return ErrClosed
	}
	select {
	case c.send <- e:
		return nil
	case <-c.state.done:
		return ErrClosed
	}
}

// Recv implements Conn.
func (c *ChanConn) Recv() (contract.Envelope, error) {
	select {
	case e := <-c.recv:
		return e, nil
	case <-c.state.done:
		return contract.Envelope{}, ErrClosed
	}
}

// Connected implements Conn.
func (c *ChanConn) Connected() bool { return !c.state.flag.Load() }

// Close implements Conn.
func (c *ChanConn) Close() error {
	c.state.close()
	return nil
}

// Drop is an alias for Close that reads as a simulated network failure in tests.
func (c *ChanConn) Drop() { c.state.close() }
