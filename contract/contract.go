// Package contract defines the cross-boundary shapes exchanged over the portal's
// two WebSocket planes (DESIGN §7): the control plane (portal ↔ field Pis) and the
// device-registration handshake (DESIGN §3). Go is the source of truth for these
// types; TypeScript is generated from them so frontends cannot drift (DESIGN §14).
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

// Role is what a registered device is configured to be (DESIGN §3, §8).
type Role string

const (
	RoleUnassigned Role = "unassigned" // known device, no role yet (awaiting admin)
	RoleField      Role = "field"      // drives robots on a field
	RoleDisplay    Role = "display"    // kiosk display (type assigned later)
)

// Register is sent by a Pi to the portal on boot (DESIGN §3). Identity is the
// hardware-serial-derived device ID, never the address.
type Register struct {
	DeviceID string `json:"device_id"` // e.g. "e6-<serial>"
	HWSerial string `json:"hw_serial"`
	Address  string `json:"address"` // current DHCP-assigned address
	Role     Role   `json:"role"`    // last-known role the device claims, if any
}

// RegisterAck is the portal's reply. For an unknown device the role is Unassigned;
// for a known device it is the stored role and field binding (self-restore).
type RegisterAck struct {
	DeviceID string `json:"device_id"`
	Role     Role   `json:"role"`
	Field    string `json:"field"` // field binding, if any
}

// CommandType is a control-plane command sent down from the portal to a field Pi
// (DESIGN §7). After Start, nothing more is required — the match runs on the Pi's own clock.
type CommandType string

const (
	CmdLoad  CommandType = "load"  // stage a match (carries RunConfig)
	CmdStart CommandType = "start" // begin the staged match
	CmdEstop CommandType = "estop" // emergency stop; latches
	CmdAbort CommandType = "abort" // end the current match
	CmdReset CommandType = "reset" // clear estop/fault back to idle
)

// Command is a single control-plane instruction.
type Command struct {
	Type   CommandType `json:"type"`
	Config *RunConfig  `json:"config,omitempty"` // set only for CmdLoad
}

// RunConfig is the run projection of the match entity pushed to the field Pi at
// load time. Standard durations are used if CountdownAuton etc. are zero (DESIGN §5).
type RunConfig struct {
	MatchID         string        `json:"match_id"`
	Type            match.Type    `json:"type"`
	CountdownAuton  time.Duration `json:"countdown_auton,omitempty"`
	Autonomous      time.Duration `json:"autonomous,omitempty"`
	Transition      time.Duration `json:"transition,omitempty"`
	CountdownDriver time.Duration `json:"countdown_driver,omitempty"`
	Driver          time.Duration `json:"driver,omitempty"`
}

// MatchConfig converts a RunConfig to a match.Config, substituting standard
// Pinnacle durations for any zero-value duration fields.
func (rc RunConfig) MatchConfig() match.Config {
	var std match.Config
	switch rc.Type {
	case match.Alliance:
		std = match.AllianceConfig(rc.MatchID)
	case match.SoloDriving:
		std = match.SoloDrivingConfig(rc.MatchID)
	case match.SoloCoding:
		std = match.SoloCodingConfig(rc.MatchID)
	default:
		std = match.Config{MatchID: rc.MatchID, Type: rc.Type}
	}
	// Override with explicit values where set.
	if rc.CountdownAuton != 0 {
		std.CountdownAuton = rc.CountdownAuton
	}
	if rc.Autonomous != 0 {
		std.Autonomous = rc.Autonomous
	}
	if rc.Transition != 0 {
		std.Transition = rc.Transition
	}
	if rc.CountdownDriver != 0 {
		std.CountdownDriver = rc.CountdownDriver
	}
	if rc.Driver != 0 {
		std.Driver = rc.Driver
	}
	return std
}

// StateMsg is published up from a field Pi (DESIGN §7). Displays interpolate the
// countdown from Remaining, never their own clock (DESIGN §8).
type StateMsg struct {
	DeviceID           string        `json:"device_id"`
	MatchID            string        `json:"match_id"`
	Phase              match.Phase   `json:"phase"`
	Mode               match.Mode    `json:"mode"`
	Enabled            bool          `json:"enabled"`
	Remaining          time.Duration `json:"remaining"`           // time left in current enabled phase
	CountdownRemaining time.Duration `json:"countdown_remaining"` // time left in countdown
}

// Kind tags an Envelope so the receiver knows how to decode Data.
type Kind string

const (
	KindCommand     Kind = "command"
	KindState       Kind = "state"
	KindRegister    Kind = "register"
	KindRegisterAck Kind = "register_ack"
)

// Envelope is the framed unit moved by any transport.
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
