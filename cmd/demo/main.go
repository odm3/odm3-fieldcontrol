// Command demo runs the Phase 1 field-control vertical slice end to end in a single
// process: portal device registry, registration handshake, and a field run loop
// driven over the in-process control plane with a mock V5 driver. A scaled clock
// compresses the 2-minute alliance match into a couple of seconds.
//
//	go run ./cmd/demo
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/driver"
	"github.com/odm3/e6events/fieldcontrol/field"
	"github.com/odm3/e6events/fieldcontrol/match"
	"github.com/odm3/e6events/fieldcontrol/portal"
	"github.com/odm3/e6events/fieldcontrol/transport"
)

// scaledClock advances match-time faster than wall-time so a full match runs quickly.
type scaledClock struct {
	base   time.Time
	start  time.Time
	factor float64
}

func newScaledClock(factor float64) *scaledClock {
	now := time.Now()
	return &scaledClock{base: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), start: now, factor: factor}
}

func (c *scaledClock) Now() time.Time {
	return c.base.Add(time.Duration(float64(time.Since(c.start)) * c.factor))
}

func main() {
	const factor = 60 // 126s alliance match (3+15+3+105) runs in ~2.1s

	// --- Registration handshake (DESIGN §3) -----------------------------------
	reg := portal.NewRegistry(match.RealClock{})
	serial := "100000003a1b2c3d"
	devID := field.DeviceID(serial)

	ack := reg.Register(contract.Register{DeviceID: devID, HWSerial: serial, Address: "10.20.0.20"})
	fmt.Printf("register %s @10.20.0.20 -> role=%q (first boot)\n", devID, ack.Role)
	_ = reg.Assign(devID, contract.RoleField, "field-1")
	ack = reg.Register(contract.Register{DeviceID: devID, HWSerial: serial, Address: "10.20.0.99"})
	fmt.Printf("reboot on new lease 10.20.0.99 -> role=%q field=%q (self-restored)\n\n", ack.Role, ack.Field)

	// --- Control plane: portal <-> field --------------------------------------
	clk := newScaledClock(factor)
	m := match.New(clk)
	portalEnd, fieldEnd := transport.Pipe()

	rl := field.New(devID, m, driver.NewMock(), fieldEnd)
	fc := portal.NewFieldController(portalEnd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = rl.Run(ctx, 20*time.Millisecond) }()

	// Portal observes published state and prints each phase change.
	dropped := make(chan struct{})
	go func() {
		var last match.Phase = -1
		for {
			msg, err := fc.ReadState()
			if err != nil {
				close(dropped)
				return
			}
			if msg.Phase != last {
				rem := msg.Remaining
				if msg.CountdownRemaining > 0 {
					rem = msg.CountdownRemaining
				}
				fmt.Printf("portal <- phase=%-16s enabled=%-5v mode=%-10s remaining=%s\n",
					msg.Phase, msg.Enabled, msg.Mode, rem.Round(time.Second))
				last = msg.Phase
			}
		}
	}()

	// Run an alliance match (3s countdown → 15s auto → 3s countdown → 105s driver).
	fmt.Println("loading + starting an alliance match...")
	_ = fc.Load(contract.RunConfig{MatchID: "Q-001", Type: match.Alliance})
	_ = fc.Start()

	// Mid-match, simulate a portal/network drop. The live match must keep running.
	time.Sleep(900 * time.Millisecond)
	fmt.Println("\n*** network drop: portal connection lost mid-match ***")
	portalEnd.Drop()
	<-dropped
	fmt.Println("portal stopped receiving — field keeps running locally...")

	// Wait for the match to complete on the field's local clock.
	time.Sleep(1500 * time.Millisecond)
	st := rl.LastState()
	fmt.Printf("\nfield-local final state: phase=%s enabled=%v\n", st.Phase, st.Enabled)
	if st.Phase == match.PhaseEnded {
		fmt.Println("match completed on the local clock despite the dropped connection. ✓")
	}
}
