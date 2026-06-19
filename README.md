# e6events — field control

Open, **local-first** tournament stack for RECF Achieve Pinnacle (2026–2027). This
repository is the Pi field controller (Go) plus the minimal portal slice needed to
drive a field end to end. See [`docs/DESIGN.md`](docs/DESIGN.md) for the full system
architecture and roadmap.

Module path: `github.com/odm3/e6events/fieldcontrol`

## Status

- **Phase 0 — Match core. [DONE]** Authoritative single-match state machine.
- **Phase 1 — Field-control vertical slice. [IN PROGRESS]** Contract types, the
  control plane, the driver registry, the V5 GPIO driver, the run loop, and the
  portal device registry / registration handshake — all unit-tested with an
  injectable clock and mock drivers (no hardware required).

Remaining for Phase 1 to be "done" (a real V5 robot running a full match on real
hardware): a WebSocket adapter implementing `transport.Conn`, the real Pi GPIO pin
wrapper behind `driver.Pin`, and the V5 competition-port pin map (DESIGN §11).

## Packages

| Package      | Responsibility |
|--------------|----------------|
| `match`      | Authoritative single-match state machine: phases, the two derived signals (enabled/disabled, autonomous/driver), estop latching, snapshot + failsafe restore. Runs entirely on an injected `Clock`. |
| `contract`   | Cross-boundary shapes for the control plane and registration handshake (`Command`, `StateMsg`, `Register`/`RegisterAck`, `RunConfig`) and the transport `Envelope`. Go is the source of truth; TS is generated. |
| `transport`  | The small `Conn` abstraction plus an in-process pipe (with drop simulation) used by tests and the demo. The production WebSocket adapter implements the same interface. |
| `driver`     | The hardware boundary: `Driver` interface + registry, a `Mock` for tests, and `V5GPIO` (legacy competition port over two configurable GPIO lines). |
| `field`      | The Pi run loop: binds the machine to a driver and the control plane, advances on the local clock, refuses to start a match it can't record, and survives a mid-match link drop. |
| `portal`     | Minimal tournament slice: device registry + registration handshake (stable hardware-serial IDs, self-restore across DHCP leases) and a controller that drives one field. |

## Key invariants (from the design)

- The field asserts only two signals — **enabled/disabled** and **autonomous/driver**.
- The live match is **Pi-authoritative**: it runs on the local clock, so a dropped
  control connection mid-match does not affect it.
- **EStop** latches disabled, overrides everything, and is cleared only by **Reset**.
- A mid-match reboot recovers into **fault**, disabled, with the match ID preserved
  for the operator's replay decision — never a silent re-enable.
- The Pi **refuses to start a match it cannot record** (link down between matches).
- Device **identity is the hardware serial, never the address**.

## Run it

```sh
go test ./...          # full lifecycle, all match types, estop, abort, failsafe, network drop
go test -race ./...    # the run loop and demo are concurrent
go run ./cmd/demo      # end-to-end: registration, an alliance match, and a mid-match network drop
```
