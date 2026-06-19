package driver_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/odm3/e6events/fieldcontrol/driver"
	"github.com/odm3/e6events/fieldcontrol/match"
)

func TestRegistryOpen(t *testing.T) {
	r := driver.NewRegistry()
	r.Register("mock", func() (driver.Driver, error) { return driver.NewMock(), nil })

	d, err := r.Open("mock")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name() != "mock" {
		t.Fatalf("name: got %q", d.Name())
	}
	if _, err := r.Open("nope"); err == nil {
		t.Fatal("expected error opening unregistered driver")
	}
	if got := r.Names(); len(got) != 1 || got[0] != "mock" {
		t.Fatalf("names: got %v", got)
	}
}

func TestMockRecords(t *testing.T) {
	m := driver.NewMock()
	_ = m.Apply(true, match.ModeDriver)
	_ = m.Apply(false, match.ModeNone)
	if len(m.Applied) != 2 {
		t.Fatalf("applied: got %d want 2", len(m.Applied))
	}
	if last := m.Last(); last.Enabled {
		t.Fatalf("last should be disabled, got %+v", last)
	}
	_ = m.Close()
	if !m.Closed {
		t.Fatal("expected Closed")
	}
}

// bufPort is an in-memory Port for testing V5USB without real hardware.
type bufPort struct{ bytes.Buffer }

func (p *bufPort) Write(b []byte) (int, error) { return p.Buffer.Write(b) }

func TestV5USBPacketDisabled(t *testing.T) {
	p := &bufPort{}
	if _, err := driver.NewV5USB(p); err != nil {
		t.Fatal(err)
	}
	// NewV5USB sends a disabled packet on construction.
	checkPacket(t, p.Bytes(), false, match.ModeNone)
}

func TestV5USBPacketAutonomous(t *testing.T) {
	p := &bufPort{}
	d, err := driver.NewV5USB(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Reset()
	if err := d.Apply(true, match.ModeAutonomous); err != nil {
		t.Fatal(err)
	}
	checkPacket(t, p.Bytes(), true, match.ModeAutonomous)
}

func TestV5USBPacketDriver(t *testing.T) {
	p := &bufPort{}
	d, err := driver.NewV5USB(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Reset()
	if err := d.Apply(true, match.ModeDriver); err != nil {
		t.Fatal(err)
	}
	checkPacket(t, p.Bytes(), true, match.ModeDriver)
}

// checkPacket verifies a 14-byte V5 USB serial packet.
func checkPacket(t *testing.T, pkt []byte, enabled bool, mode match.Mode) {
	t.Helper()
	if len(pkt) != 14 {
		t.Fatalf("packet length: got %d want 14", len(pkt))
	}
	// Magic header.
	want := []byte{0xC9, 0x36, 0xB8, 0x47, 0x58, 0xC1, 0x05}
	for i, b := range want {
		if pkt[i] != b {
			t.Fatalf("header byte %d: got 0x%02X want 0x%02X", i, pkt[i], b)
		}
	}
	// State byte.
	var wantState byte
	switch {
	case !enabled:
		wantState = 0x0B
	case mode == match.ModeAutonomous:
		wantState = 0x0A
	default:
		wantState = 0x08
	}
	if pkt[7] != wantState {
		t.Fatalf("state byte: got 0x%02X want 0x%02X", pkt[7], wantState)
	}
	// Zero padding.
	for i := 8; i < 12; i++ {
		if pkt[i] != 0 {
			t.Fatalf("padding byte %d: got 0x%02X", i, pkt[i])
		}
	}
	// CRC16/CCITT-FALSE over first 12 bytes — re-verify independently.
	crc := crc16CCITT(pkt[:12])
	got := binary.BigEndian.Uint16(pkt[12:])
	if got != crc {
		t.Fatalf("CRC: got 0x%04X want 0x%04X", got, crc)
	}
}

func crc16CCITT(data []byte) uint16 {
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

func TestV5USBRequiresPort(t *testing.T) {
	if _, err := driver.NewV5USB(nil); err == nil {
		t.Fatal("expected error with nil port")
	}
}
