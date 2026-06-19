# e6events — System Design

> Project renamed **odm3 → e6events**. Field-controller module: `github.com/odm3/e6events/fieldcontrol`.
> Component repo names (`odm3-*`) are retained as logical architecture labels.

Design notes for an open, **local-first** tournament stack for RECF Achieve Pinnacle
(2026–2027): a tournament portal, a field controller, a staff app, displays, and a
notification service — none of it tied to RECFEvents or VEX-proprietary tooling.

The field controller's match state machine is built (`match/`). Everything else here is
the agreed architecture and the roadmap to it.

---

## 1. Vision

A self-contained competition system that runs an event on a gym LAN with no internet
dependency. RECFEvents is cloud and needs reliable Wi-Fi; ours runs on the local
network with a local database, and only reaches the internet to *publish* results when
it can. That single decision — local-first, offline-capable — is the product identity.

Core idea everything derives from: **the field only ever asserts two signals,
enabled/disabled and autonomous/driver.** What a controller does with its robot,
including the wireless link, is the controller's concern and opaque to the field.

---

## 2. The whole system

There are only three kinds of node:

1. **Portal server** — one per event, the authority, Go + SQLite. Owns all tournament
   state, the network's truth, and is by design the single source of truth *and* the
   single point of failure. If it dies, the event pauses (operational recovery).
2. **Field-control Pis** — Go, one per field, drive robots. State machine built.
3. **Everything else is a client of the portal** — the same TypeScript core in
   different roles. Some clients run on personal devices (staff app, team phones),
   some on kiosk Pis (displays).

### Components / repos
| Repo                | What it is                                                        |
|---------------------|-------------------------------------------------------------------|
| `odm3-contract`     | Shared Go types (WS messages, match entity) + TS codegen. Source of truth for every cross-boundary shape. |
| `odm3-fieldcontrol` | Pi field controller (Go). **Match state machine built.**          |
| `odm3-portal`       | Go tournament server: SQLite, REST, two WebSocket planes, device registry. |
| `odm3-portal-web`   | TypeScript PWA: admin/control surface + display routes.           |
| `odm3-staff`        | React Native + Expo app: queuer check-in, inspection, scoring (one role-gated app). Shares the TS core. |
| `odm3-display`      | Identical kiosk Pi image (Chromium → a portal display route).     |
| `odm3-notify`       | Separate notification service (team-facing, may use internet).    |
| `odm3-fieldcontrol-client` | Java library for FTC/REV teams. Implements the ODM3 UDP open protocol inside a single FTC OpMode. |

---

## 3. Device identity & provisioning

DHCP (router-assigned addresses) is a maintained dependency carried from VEX setups.
**Addresses are not identity.** Decided model:

- Every Pi has a **stable device ID derived from its hardware serial** — identical
  flashed image, unique stable ID, no per-unit flashing.
- Each Pi image carries **one configured value: the portal address.** (Discovery is a
  configured portal address, *not* mDNS — deliberate, since we control the network.)
- DHCP assigns the Pi whatever IP it wants. On boot the Pi contacts the portal at the
  configured address and **registers**: "I am device `odm3-<serial>`, reachable at
  `<current IP>`."
- The portal keeps a **device registry keyed by device ID**, storing role, field
  binding, and last-seen address. A Pi returning on a new lease is recognized instantly
  as the same node it was before — surviving lease changes, switch swaps, even a router
  replacement, because identity never lived in the address. (This is VEX TM's
  "remember the Pi across restarts" behavior, minus the fragile "if the network didn't
  change" condition.)

The device registry doubles as the **event-day health view**: which Pis are online,
stale, what each claims to be, last check-in. First-time setup (unknown ID → assign
role) and recovery (known ID → self-restore) flow through the same mechanism.

---

## 4. Network topology

The tournament LAN runs on existing UniFi infrastructure (AP, switch, gateway)
carried from VEX tournament setups. ODM3 adds one dedicated VLAN/SSID for field
control, isolated from the tournament management and team/guest networks.

```
UniFi Gateway
├── VLAN 10 — Tournament Management
│     Portal server, scoring laptops, admin tablets
├── VLAN 20 — Field Control  (hidden SSID)
│     Field Pis (ethernet), REV Control Hubs (Wi-Fi), display Pis
│     Portal server has a leg here to reach field Pis via WebSocket
└── VLAN 30 — Team / Guest
      Team laptops, phones, pit devices
```

**V5 teams** connect via **long USB cables** from each driver station position back
to the field Pi (or a powered USB hub). No Wi-Fi involvement for V5 field control —
purely wired, operationally identical to plugging into a VEX field controller today.
USB active extension cables cover up to 5m passively; active repeaters reach 10–15m.
Four USB connections per field (one per alliance station).

**REV teams** connect their Control Hub to the hidden field SSID (VLAN 20) instead
of using Wi-Fi Direct to their Driver Hub. One-time per-device configuration.

The hidden field SSID keeps team devices off the field network. Client isolation,
MAC whitelisting, and per-SSID bandwidth controls via UniFi keep the field network
quiet for the latency-sensitive 50ms ODM3 UDP broadcasts.

---

## 5. Match lifecycle entity (the spine)

One entity every client reads, in different projections:

- schedule slot, field-set assignment
- queue status: `queued → on_deck → on_field → scoring → complete`
- per-slot **check-in readiness** flags (queuer presence; see §6)
- run `Config` (for the field Pi)
- result (score)

Projections: the field Pi reads the run config; the queue reads queue status + field +
readiness; displays read current match + score; rankings read results.

---

## 6. Control model

Two orthogonal signals derived from a single `Phase`.

### Match types & durations (Pinnacle)
| Type           | Structure                          |
|----------------|------------------------------------|
| `alliance`     | 15s autonomous → 105s driver (120s)|
| `solo_driving` | 60s driver only                    |
| `solo_coding`  | 60s autonomous only                |

### Phases
| Phase | Enabled | Mode | Notes |
|---|---|---|---|
| `idle` | no | none | no match loaded |
| `pre_match` | no | none | loaded, waiting for start |
| `countdown_auton` | no | none | 3..2..1 before autonomous — display/driver awareness only |
| `autonomous` | **yes** | autonomous | autonomous period |
| `transition` | no | none | disabled pause between auton and driver countdown |
| `countdown_driver` | no | none | 3..2..1 before driver control — display/driver awareness only |
| `driver` | **yes** | driver | driver control |
| `ended` | no | none | completed normally |
| `estop` | no | none | latched; cleared only by Reset |
| `fault` | no | none | recovered from interruption; awaits operator |

Countdown phases are disabled. Robots see no signal difference between `pre_match`
and a countdown — the countdown exists purely for displays and driver awareness.
`State.CountdownRemaining` carries the countdown timer; `State.Remaining` carries the
enabled-phase timer. Display Pis render "AUTONOMOUS IN 3..2..1" vs "DRIVER CONTROL IN
3..2..1" from the phase name.

Full phase sequences:
- **Alliance:** `pre_match → countdown_auton(3s) → autonomous(15s) → [transition] → countdown_driver(3s) → driver(105s) → ended`
- **SoloDriving:** `pre_match → countdown_auton(3s) → driver(60s) → ended`
- **SoloCoding:** `pre_match → countdown_auton(3s) → autonomous(60s) → ended`

Implemented in `match/`.

---

## 7. Two planes + queue/check-in

The portal exposes two WebSocket surfaces:

- **Control plane (south, portal ↔ field Pis):** `Load / Start / Estop / Abort / Reset`
  down, `State` up. After `Start`, nothing flowing down is required — a live match runs
  on the Pi's own clock.
- **View plane (north, portal → all clients):** a published read-model stream — schedule,
  queue status, current match, readiness, scores, rankings. The queue, displays, and
  team views all consume this.

Transport for the view plane is an **in-process Go pub/sub hub** (local-first; no Redis
dependency on a single box). The pub/sub *abstraction* stays clean so Redis can slot in
for large multi-division events. (The queue/check-in subsystem is the queue function built natively into the portal,
not a separate named product.)

### Queue & check-in (the Worlds model, improved)
Queue is presence/readiness, not one-way notification:

- **Queuer check-in:** a staff role (in the staff app) marks per-slot presence on the
  on-deck match — a box per team. When all four are checked, the match shows 4/4 ready
  (green). A slot can be a small enum (`absent / present / no_show`) if the ref needs to
  distinguish "3 ready, 1 no-show" — TBD (§11).
- Readiness is a **portal-side gate upstream of `Load`.** The field Pi never sees it;
  only once 4/4 (and a human commits) does the portal push `Config` to the Pi. Readiness
  flow and robot-enable flow are cleanly separated.
- **Team / pit views** are read-only view-plane clients (a phone filtered to its team
  number shows next match, field, live queue status) — pushed, no polling, no TM API.

---

## 8. Displays

Identical flashed kiosk image; **type assigned from the portal after it's on the
network** (nothing display-specific baked in). Boots → Chromium kiosk → registers as an
unconfigured display → admin assigns role → loads the matching display route. Reassign
remotely without reflashing.

| Display type        | Binding              | Chrome             |
|---------------------|----------------------|--------------------|
| Field Queue Display | a field's timer / readiness (separate Pi OK) | tied to one field |
| Audience Display    | unbound (big screens/projectors) | overlays allowed (branding, intros) |
| Pit Display         | unbound              | no overlays — clean schedule/rankings/queue |

Display countdown renders from the Pi's reported `Remaining` relayed through the portal,
interpolated locally — never the display's own clock — so it matches the field's
authoritative timer.

---

## 9. Notifications

A **separate service** (`odm3-notify`) that subscribes to the view plane. It's the one
piece that may want internet (push to teams off the venue Wi-Fi) and has a different
reliability profile than match control — so it's isolated: it can fail, restart, or go
offline without touching anything that runs a match. Consumer of the match stream, never
a dependency of it.

---

## 10. Staff app

One React Native + Expo app, one codebase, multiple roles: **queuer check-in,
inspection, scoring.** Role-gated UI — a volunteer is handed the role they're working.
Shares the TypeScript core (match entity, generated contract types, view/control
clients, offline sync) with `odm3-portal-web`; native only where needed (camera/QR for
team check-in, secure storage, background sync).

Why RN/Expo over native Swift/Kotlin: the expensive part of this app is offline-sync
correctness and the portal contract, not the UI. Native would reimplement that 2–3×.
RN keeps it one TS codebase consuming the same Go-generated types, with native-feeling
UI; true native is reserved for modules RN can't express.

---

## 11. Fault model & concurrency

- **Live match is Pi-authoritative.** WS drop mid-match → match completes on local clock.
  WS drop between matches → Pi refuses to start a match it can't record.
- **Estop** latches disabled, overrides everything, cleared only by Reset.
- **Pi reboot mid-match** → read persisted `Snapshot` → come up disabled in `fault`,
  match ID preserved → operator replay decision. Never silently re-enable.
- **Portal dies** → event pauses (single point of failure by design).
- **Field sets:** within a set, staggered, one live match at a time; across sets, fully
  concurrent and independent. **No timing coordination anywhere** (no synchronized
  starts), so no cross-Pi clock-sync problem. One-live-match-per-set enforced portal-side.

---

## 12. Driver layer & open protocol

Pluggable driver modules implement a common Go interface. The state machine calls
`SetState(enabled, mode)` on each slot and knows nothing about the protocol behind it.

### Open field control protocol (UDP broadcast)

The field Pi broadcasts an 8-byte UDP packet every **50ms** on port **9800** of the
field subnet. Every controller platform that can open a UDP socket and parse this packet
participates in field control — no proprietary hardware, no modified apps.

```
Offset  Size  Description
0-3     4B    Magic: 0x4F 0x44 0x4D 0x33  ("ODM3")
4       1B    Version: 0x01
5       1B    Flags:
                bit 0 — enabled  (1 = enabled,    0 = disabled)
                bit 1 — mode     (1 = driver ctrl, 0 = autonomous)
                bit 2 — estop    (1 = e-stop latched)
6-7     2B    Time remaining in current phase, unsigned short, milliseconds (0 when disabled)
```

Silence = disabled. If a robot stops receiving packets for 500ms it must treat itself
as disabled — the same conservative failsafe the Pi applies on its own side.

### V5 driver (Y1 MVP)

The V5 controller (handheld gamepad) is the field control endpoint — not the brain.
The Pi sends the 14-byte USB serial packet decoded from Jerrylum/better-field-control
over a USB connection to each V5 controller. The controller relays enable/disable and
autonomous/driver to the brain over VEXnet/BT. Four robots = four USB connections into
a powered hub on the Pi. No FC brain, no smart cable required.

USB serial protocol (115200 baud, VEX USB VID 0x288):
```
C9 36 B8 47 58 C1 05 <state> 00 00 00 00 <crc_hi> <crc_lo>

State byte:  0x0A = autonomous,  0x08 = driver,  0x0B = disabled
Checksum:    CRC16 over first 12 bytes
```

### REV Control Hub driver (post-MVP, open electronics)

FTC's current architecture is point-to-point Wi-Fi Direct between the Driver Hub
(Android, driver station) and the Control Hub (Android, on robot) with no field
control hook. Teams press Init and Start manually; there is no external kill signal.

The ODM3 solution for RECF teams using REV hardware is the **FieldControlClient**
library (`odm3-fieldcontrol-client` Java repo). Teams include it in their FTC Android
Studio project. It runs a background UDP listener on port 9800, receives the ODM3
open protocol packet, and exposes `isAutonomous()` / `isDriverControl()` / `isEnabled()`
to the OpMode.

**RecfOpMode base class — mirrors VEXcode/PROS three-callback model:**

```java
// Teams extend this, override three methods. Mirrors VEXcode pre_auton /
// autonomous / usercontrol and PROS competition_initialize / autonomous / opcontrol.
public abstract class RecfOpMode extends LinearOpMode {
    private final FieldControlClient field = new FieldControlClient();

    public void preAuton() {}          // hardware init, sensor reset, display
    public abstract void autonomous(); // autonomous period
    public abstract void driverControl(); // driver control period

    @Override
    public final void runOpMode() throws InterruptedException {
        field.start();
        preAuton();                      // runs while disabled / during countdown
        field.waitForAutonomous();       // blocks until field enables auto
        autonomous();
        field.waitForDriverControl();    // blocks until field transitions to driver
        driverControl();
        field.stop();
    }
}

// Team's entire robot program:
@TeleOp(name = "RECF Match", group = "RECF")
public class MyRobot extends RecfOpMode {
    @Override public void preAuton()      { /* init hardware */ }
    @Override public void autonomous()    { /* auto code */     }
    @Override public void driverControl() { /* driver code */   }
}
```

**Rules requirement for REV teams:**
- Write a single `@TeleOp` OpMode (not separate Autonomous + TeleOp programs)
- Use `FieldControlClient` to delineate autonomous and driver control periods
- Select the OpMode, hit **Init** when signaled by the queuer; the field Pi drives
  everything after that — autonomous start, transition to driver, match end
- Do not hit Start or Stop manually

This eliminates the FTC manual Init/Start/Stop flow, the autonomous-to-TeleOp gap
where teams lose seconds, and the DS preselection complexity. The robot's Wi-Fi
joins the field subnet (not Wi-Fi Direct to the Control Hub), which is the one
network configuration change teams make once.

The Pi-side REV driver module broadcasts the same open protocol UDP packet as every
other controller type — no special handling. The library on the robot side is the
entire REV integration.

### Future drivers

When RECF defines its Y2+ open electronics standard, drivers slot in against the same
Go interface without touching the state machine. The open protocol UDP packet is already
the universal field signal for any controller with a network stack.

---

## 13. Open questions

- **Offline-sync model for the staff app:** last-write-wins with a sync queue, or real
  conflict handling for scoring (two refs on one match, or a stale offline tablet)?
- **Check-in granularity:** per-slot boolean, or `absent / present / no_show` enum?
- **Slot assignment:** portal pushes slot→controller mapping in the match config, or
  Pi autodiscovers and reports up for confirmation?
- View-plane message envelope/versioning strategy.

---

## 14. Tech stack

- **Pi + portal:** Go. One language across the control plane; the WebSocket contract is
  the *same Go types* on both ends via `odm3-contract`.
- **Types:** Go is the source of truth; TypeScript is **generated, never hand-written**
  (e.g. `tygo`), so the frontends can't drift from the server.
- **Portal DB:** SQLite (file-based, zero-config, copy-the-file backups; WAL mode).
- **Web:** TypeScript PWA (`odm3-portal-web`), service-worker offline.
- **Staff app:** React Native + Expo, shares the TS core.
- **Pi images:** Pi OS Lite, process as a `systemd` service on boot; configured portal
  address + hardware-serial device ID; identical image per role-class.
- **FieldControlClient:** Java library (`odm3-fieldcontrol-client`) for FTC/REV teams.
  Published as an open source Android library. Implements the ODM3 UDP open protocol.
- **Testing:** Go `testing` with an injectable `Clock` and mock drivers — full lifecycle
  testable with no hardware.

---

## 15. Roadmap

**Phase 0 — Match core. [DONE]**
`match/` state machine: alliance / solo driving / solo coding, estop, abort, snapshot +
failsafe restore. Fully unit-tested.

**Phase 1 — Field-control vertical slice. [IN PROGRESS]**
`contract` (shared Go WS types) · run loop + driver registry interface · V5 USB serial
driver (14-byte packet, CRC16/CCITT) · portal device registry + registration handshake
(hardware-serial ID, self-restore across DHCP leases) · minimal portal able to
Load/Start/Estop/Reset one field · open protocol UDP broadcaster (8-byte, port 9800).
*Built:* all of the above as fully tested Go packages with injectable clock and mock
drivers. *Remaining for hardware-done:* WebSocket adapter for `transport.Conn`, real
Pi USB serial port wrapper behind `driver.Port`, and the VID 0x288 enumeration on-Pi.
*Done when:* a real V5 robot runs a full match on real hardware, driven end to end.

**Phase 2 — Tournament spine.**
Portal data model (events, teams, match lifecycle entity) on SQLite · match conductor ·
pluggable per-season scoring (Pinnacle) · rankings · web admin/control surface.
*Done when:* an event's qualification matches run with scoring and live rankings.

**Phase 3 — Queue & staff app.**
View-plane pub/sub hub · queuer check-in / readiness flags · `odm3-staff` (RN+Expo:
queuer + inspection + scoring, role-gated, offline sync) · read-only team/pit queue views.
*Done when:* queuers check teams in from tablets and teams see their status live.

**Phase 4 — Displays.**
`odm3-display` identical kiosk image · self-register + portal-assigned type · Field Queue
/ Audience / Pit routes · device-registry health view.
*Done when:* plug a Pi into any screen, assign its role from the portal, it shows the right view.

**Phase 5 — Full event + reach.**
`odm3-notify` service · scheduling algorithms · skills · alliance selection & elims ·
cloud publish of results · `odm3-fieldcontrol-client` REV Java library published.
*Done when:* a complete event runs start to finish, including elims and skills, and
publishes when online.
