package driver

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/odm3/e6events/fieldcontrol/match"
)

// V5 USB serial protocol constants (DESIGN §12, from Jerrylum/better-field-control).
// The Pi sends a 14-byte packet to each V5 Controller over USB serial at 115200 baud.
//
//	C9 36 B8 47 58 C1 05 <state> 00 00 00 00 <crc_hi> <crc_lo>
//
// State byte: 0x0A = autonomous, 0x08 = driver, 0x0B = disabled.
// Checksum: CRC16/CCITT over the first 12 bytes.
const (
	v5StateAutonomous = 0x0A
	v5StateDriver     = 0x08
	v5StateDisabled   = 0x0B
)

var v5Header = [7]byte{0xC9, 0x36, 0xB8, 0x47, 0x58, 0xC1, 0x05}

// Port abstracts the serial port so the driver is testable without real hardware.
type Port interface {
	io.Writer
}

// V5USB drives one V5 Controller over USB serial (DESIGN §12). Four slots per
// field map to four USB connections into a powered hub on the Pi. The same packet
// is sent repeatedly; the V5 Controller relays enable/disable and auton/driver to
// the Brain over VEXnet/BT.
type V5USB struct {
	port Port
}

// NewV5USB builds a driver connected to the given serial port. It immediately
// sends a disabled packet so the controller starts in a safe state.
func NewV5USB(port Port) (*V5USB, error) {
	if port == nil {
		return nil, fmt.Errorf("v5usb: port is required")
	}
	d := &V5USB{port: port}
	if err := d.Apply(false, match.ModeNone); err != nil {
		return nil, err
	}
	return d, nil
}

// Name implements Driver.
func (d *V5USB) Name() string { return "v5-usb" }

// Apply implements Driver. Sends a 14-byte packet encoding the two field signals.
func (d *V5USB) Apply(enabled bool, mode match.Mode) error {
	var state byte
	switch {
	case !enabled:
		state = v5StateDisabled
	case mode == match.ModeAutonomous:
		state = v5StateAutonomous
	default:
		state = v5StateDriver
	}

	var pkt [14]byte
	copy(pkt[:7], v5Header[:])
	pkt[7] = state
	// bytes 8-11 are zero (already zero-valued)
	crc := crc16(pkt[:12])
	binary.BigEndian.PutUint16(pkt[12:], crc)

	if _, err := d.port.Write(pkt[:]); err != nil {
		return fmt.Errorf("v5usb: write: %w", err)
	}
	return nil
}

// Close sends a disabled packet and releases the port.
func (d *V5USB) Close() error {
	return d.Apply(false, match.ModeNone)
}

// crc16 computes CRC16/CCITT-FALSE (poly 0x1021, init 0xFFFF, no reflect).
// This is the checksum used in the VEX USB serial protocol.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
