package contract_test

import (
	"testing"
	"time"

	"github.com/odm3/e6events/fieldcontrol/contract"
	"github.com/odm3/e6events/fieldcontrol/match"
)

func TestCommandRoundTrip(t *testing.T) {
	cases := []contract.Command{
		{Type: contract.CmdLoad, Config: &contract.RunConfig{MatchID: "Q-1", Type: match.Alliance}},
		{Type: contract.CmdStart},
		{Type: contract.CmdEstop},
		{Type: contract.CmdAbort},
		{Type: contract.CmdReset},
	}
	for _, want := range cases {
		env, err := contract.EncodeCommand(want)
		if err != nil {
			t.Fatalf("encode %v: %v", want.Type, err)
		}
		if env.Kind != contract.KindCommand {
			t.Fatalf("kind: got %q", env.Kind)
		}
		got, err := contract.DecodeCommand(env)
		if err != nil {
			t.Fatalf("decode %v: %v", want.Type, err)
		}
		if got.Type != want.Type {
			t.Fatalf("type: got %q want %q", got.Type, want.Type)
		}
		if want.Config != nil {
			if got.Config == nil || *got.Config != *want.Config {
				t.Fatalf("config: got %+v want %+v", got.Config, want.Config)
			}
		}
	}
}

func TestStateRoundTrip(t *testing.T) {
	want := contract.StateMsg{
		DeviceID:  "e6-abc123",
		MatchID:   "Q-1",
		Phase:     match.PhaseDriver,
		Mode:      match.ModeDriver,
		Enabled:   true,
		Remaining: 42 * time.Second,
	}
	env, err := contract.EncodeState(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := contract.DecodeState(env)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestRegisterRoundTrip(t *testing.T) {
	want := contract.Register{DeviceID: "e6-abc", HWSerial: "abc", Address: "10.0.0.5", Role: RoleOf()}
	env, err := contract.EncodeRegister(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := contract.DecodeRegister(env)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func RoleOf() contract.Role { return contract.RoleField }

func TestDecodeWrongKind(t *testing.T) {
	env, _ := contract.EncodeState(contract.StateMsg{})
	if _, err := contract.DecodeCommand(env); err == nil {
		t.Fatal("expected error decoding state envelope as command")
	}
}
