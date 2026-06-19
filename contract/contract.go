// Package contract defines the cross-boundary shapes exchanged over the portal's
// two WebSocket planes (DESIGN §6): the control plane (portal ↔ field Pis) and the
// device-registration handshake (DESIGN §3). Go is the source of truth for these
// types; TypeScript is generated from them so frontends cannot drift (DESIGN §12).
//
// Everything here is JSON-serialisable. Transport is abstracted (see package
// transport) so the same envelopes can move over an in-process pipe in tests or a
// WebSocket on the wire in production.
package contract

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/odm3/e6events/fieldcontrol/match"
)

// Role is what a registered device is configured to be (DESIGN §3, §7).
type Role string

const (
	RoleUnassigned Role = "unassigned" // known device, no role yet (awaiting admin)
	RoleField      Role = "field"      // drives robots on a field
	RoleDisplay    Role = "display"    // kiosk display (type assigned later)
)

// Register is sent by a Pi to the portal on boot (DESIGN §3). Identity is the
// hardware-serial-derived device ID, never the address — the address is just where
// the device currently happens to be reachable.
type Register struct {
	DeviceID string `json:"device_id"` // e.g. "e6-<serial>"
	HWSerial string `json:"hw_serial"`
	Address  string `json:"address"` // current DHCP-assigned address
	Role     Role   `json:"role"`    // last-known role the device claims, if any
}

// RegisterAck is the portal's reply. For an unknown device the role is Unassigned
// until an admin assigns one; for a known device it is the stored role and field
// binding (self-restore across lease changes, DESIGN §3).
type RegisterAck struct {
	DeviceID string `json:"device_id"`
	Role     Role   `json:"role"`
	Field    string `json:"field"` // field binding, if any
}

// CommandType is a control-plane command sent down from the portal to a field Pi
// (DESIGN §6). After Start, nothing more is required from the portal — the match
// runs on the Pi's own clock.
type CommandType string

const (
	CmdLoad  CommandType = "load"  // stage a match (carries Config)
	CmdStart CommandType = "start" // begin the staged match
	CmdEstop CommandType = "estop" // emergency stop; latches
	CmdAbort CommandType = "abort" // end the current match (replay needed)
	CmdReset CommandType = "reset" // clear estop/fault back to idle
)

// Command is a single control-plane instruction.
type Command struct {
	Type   CommandType `json:"type"`
	Config *RunConfig  `json:"config,omitempty"` // set only for CmdLoad
}

// RunConfig is the run projection of the match entity that the field Pi consumes
// (DESIGN §4). The portal only pushes this once readiness gating upstream of Load
// has been satisfied (DESIGN §6); the Pi never sees readiness itself.
type RunConfig struct {
	MatchID string          `json:"match_id"`
	Type    match.MatchType `json:"type"`
}

// StateMsg is published up from a field Pi (DESIGN §6). Displays interpolate the
// countdown from Remaining, never their own clock (DESIGN §7).
type StateMsg struct {
	DeviceID  string        `json:"device_id"`
	MatchID   string        `json:"match_id"`
	Phase     match.Phase   `json:"phase"`
	Mode      match.Mode    `json:"mode"`
	Enabled   bool          `json:"enabled"`
	Remaining time.Duration `json:"remaining"` // nanoseconds; remaining in current timed phase
}

// Kind tags an Envelope so the receiver knows how to decode Data.
type Kind string

const (
	KindCommand     Kind = "command"
	KindState       Kind = "state"
	KindRegister    Kind = "register"
	KindRegisterAck Kind = "register_ack"
)

// Envelope is the framed unit moved by any transport. Data is the JSON encoding of
// the payload identified by Kind. This is exactly what a WebSocket text frame would
// carry, so the wire format is transport-independent.
type Envelope struct {
	Kind Kind            `json:"kind"`
	Data json.RawMessage `json:"data"`
}

func encode(kind Kind, v any) (Envelope, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{Kind: kind, Data: b}, nil
}

func decode(e Envelope, want Kind, v any) error {
	if e.Kind != want {
		return fmt.Errorf("contract: expected %q envelope, got %q", want, e.Kind)
	}
	return json.Unmarshal(e.Data, v)
}

// EncodeCommand wraps a Command in an Envelope.
func EncodeCommand(c Command) (Envelope, error) { return encode(KindCommand, c) }

// DecodeCommand extracts a Command from an Envelope.
func DecodeCommand(e Envelope) (Command, error) {
	var c Command
	return c, decode(e, KindCommand, &c)
}

// EncodeState wraps a StateMsg in an Envelope.
func EncodeState(s StateMsg) (Envelope, error) { return encode(KindState, s) }

// DecodeState extracts a StateMsg from an Envelope.
func DecodeState(e Envelope) (StateMsg, error) {
	var s StateMsg
	return s, decode(e, KindState, &s)
}

// EncodeRegister wraps a Register in an Envelope.
func EncodeRegister(r Register) (Envelope, error) { return encode(KindRegister, r) }

// DecodeRegister extracts a Register from an Envelope.
func DecodeRegister(e Envelope) (Register, error) {
	var r Register
	return r, decode(e, KindRegister, &r)
}

// EncodeRegisterAck wraps a RegisterAck in an Envelope.
func EncodeRegisterAck(a RegisterAck) (Envelope, error) { return encode(KindRegisterAck, a) }

// DecodeRegisterAck extracts a RegisterAck from an Envelope.
func DecodeRegisterAck(e Envelope) (RegisterAck, error) {
	var a RegisterAck
	return a, decode(e, KindRegisterAck, &a)
}
