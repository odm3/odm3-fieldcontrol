package portal

import (
	"sync"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/transport"
)

// FieldController is the portal's handle on a single field's control plane. It
// sends Load/Start/Estop/Abort/Reset down and tracks the latest state published up
// (DESIGN §6). One live match per field set is enforced portal-side; with a single
// field that invariant is trivial (DESIGN §10).
type FieldController struct {
	conn transport.Conn

	mu   sync.Mutex
	last contract.StateMsg
	seen bool
}

// NewFieldController binds a controller to one end of a control-plane link.
func NewFieldController(conn transport.Conn) *FieldController {
	return &FieldController{conn: conn}
}

func (c *FieldController) send(cmd contract.Command) error {
	env, err := contract.EncodeCommand(cmd)
	if err != nil {
		return err
	}
	return c.conn.Send(env)
}

// Load stages a match on the field. The portal only calls this once readiness
// gating has been satisfied upstream (DESIGN §6).
func (c *FieldController) Load(cfg contract.RunConfig) error {
	return c.send(contract.Command{Type: contract.CmdLoad, Config: &cfg})
}

// Start begins the staged match.
func (c *FieldController) Start() error { return c.send(contract.Command{Type: contract.CmdStart}) }

// Estop triggers an emergency stop.
func (c *FieldController) Estop() error { return c.send(contract.Command{Type: contract.CmdEstop}) }

// Abort ends the current match.
func (c *FieldController) Abort() error { return c.send(contract.Command{Type: contract.CmdAbort}) }

// Reset clears a latched estop or fault.
func (c *FieldController) Reset() error { return c.send(contract.Command{Type: contract.CmdReset}) }

// ReadState blocks for the next state published by the field and records it as the
// latest. Callers typically run this in a goroutine.
func (c *FieldController) ReadState() (contract.StateMsg, error) {
	env, err := c.conn.Recv()
	if err != nil {
		return contract.StateMsg{}, err
	}
	msg, err := contract.DecodeState(env)
	if err != nil {
		return contract.StateMsg{}, err
	}
	c.mu.Lock()
	c.last = msg
	c.seen = true
	c.mu.Unlock()
	return msg, nil
}

// Last returns the most recent state published by the field, and whether any has
// been seen yet.
func (c *FieldController) Last() (contract.StateMsg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last, c.seen
}
