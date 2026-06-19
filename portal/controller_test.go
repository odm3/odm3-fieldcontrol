package portal_test

import (
	"testing"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/driver"
	"github.com/odm3/e6events/fieldcontrol/field"
	"github.com/odm3/e6events/fieldcontrol/match"
	"github.com/odm3/e6events/fieldcontrol/portal"
	"github.com/odm3/e6events/fieldcontrol/transport"
)

// drainCommands pulls n commands off the field link and applies them to the loop.
func drainCommands(t *testing.T, fieldEnd *transport.ChanConn, rl *field.RunLoop, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		env, err := fieldEnd.Recv()
		if err != nil {
			t.Fatalf("recv command %d: %v", i, err)
		}
		cmd, err := contract.DecodeCommand(env)
		if err != nil {
			t.Fatalf("decode command %d: %v", i, err)
		}
		if err := rl.HandleCommand(cmd); err != nil {
			t.Fatalf("handle command %d: %v", i, err)
		}
	}
}

// TestControllerDrivesField wires the portal's FieldController to a field RunLoop
// over the in-process control plane and verifies a Load+Start command sequence
// reaches the machine and the resulting state publishes back up (DESIGN §6).
func TestControllerDrivesField(t *testing.T) {
	clk := fixedClock{epoch}
	m := match.New(clk, &match.MemStorer{})
	portalEnd, fieldEnd := transport.Pipe()

	fc := portal.NewFieldController(portalEnd)
	rl := field.New("e6-x", m, driver.NewMock(), fieldEnd)

	if err := fc.Load(contract.RunConfig{MatchID: "Q-1", Type: match.Alliance}); err != nil {
		t.Fatal(err)
	}
	if err := fc.Start(); err != nil {
		t.Fatal(err)
	}
	drainCommands(t, fieldEnd, rl, 2)

	// Field advances and publishes state upstream.
	msg, err := rl.Step()
	if err != nil {
		t.Fatal(err)
	}
	env, err := contract.EncodeState(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := fieldEnd.Send(env); err != nil {
		t.Fatal(err)
	}

	got, err := fc.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != match.PhaseAutonomous || !got.Enabled || got.MatchID != "Q-1" {
		t.Fatalf("portal observed wrong state: %+v", got)
	}
	if last, seen := fc.Last(); !seen || last.Phase != match.PhaseAutonomous {
		t.Fatalf("Last() should cache observed state: %+v seen=%v", last, seen)
	}
}
