package transport_test

import (
	"errors"
	"testing"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/transport"
)

func TestPipeRoundTrip(t *testing.T) {
	a, b := transport.Pipe()

	want, _ := contract.EncodeCommand(contract.Command{Type: contract.CmdStart})
	if err := a.Send(want); err != nil {
		t.Fatal(err)
	}
	got, err := b.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != contract.KindCommand {
		t.Fatalf("kind: got %q", got.Kind)
	}
}

func TestPipeDropDisconnectsBothEnds(t *testing.T) {
	a, b := transport.Pipe()
	if !a.Connected() || !b.Connected() {
		t.Fatal("expected both ends connected initially")
	}

	a.Drop()

	if a.Connected() || b.Connected() {
		t.Fatal("expected both ends disconnected after drop")
	}
	if err := a.Send(contract.Envelope{}); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("send after drop: got %v want ErrClosed", err)
	}
	if _, err := b.Recv(); !errors.Is(err, transport.ErrClosed) {
		t.Fatalf("recv after drop: got %v want ErrClosed", err)
	}
}
