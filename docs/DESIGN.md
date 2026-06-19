# e6events — System Design

> Project renamed **odm3 → e6events**. The field-controller module is
> `github.com/odm3/e6events/fieldcontrol`. The component repo names below (`odm3-*`)
> are retained as the architecture's logical component names; the field controller
> additionally carries a minimal portal slice for the Phase 1 vertical slice.

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

## 4. Match lifecycle entity (the spine)

One entity every client reads, in different projections:

- schedule slot, field-set assignment
- queue status: `queued → on_deck → on_field → scoring → complete`
- per-slot **check-in readiness** flags (queuer presence; see §6)
- run `Config` (for the field Pi)
- result (score)

Projections: the field Pi reads the run config; the queue reads queue status + field +
readiness; displays read current match + score; rankings read results.

---

## 5. Control model

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
| `autonomous` | **yes** | autonomous | autonomous period |
| `transition` | no | none | disabled pause between auton and driver |
| `driver` | **yes** | driver | driver control |
| `ended` | no | none | completed normally |
| `estop` | no | none | latched; cleared only by Reset |
| `fault` | no | none | recovered from interruption; awaits operator |

Implemented in `match/`.

---

## 6. Two planes + queue/check-in

The portal exposes two WebSocket surfaces:

- **Control plane (south, portal ↔ field Pis):** `Load / Start / Estop / Abort / Reset`
  down, `State` up. After `Start`, nothing flowing down is required — a live match runs
  on the Pi's own clock.
- **View plane (north, portal → all clients):** a published read-model stream — schedule,
  queue status, current match, readiness, scores, rankings. The queue, displays, and
  team views all consume this.

Transport for the view plane is an **in-process Go pub/sub hub** (local-first; no Redis
dependency on a single box). The pub/sub *abstraction* stays clean so Redis can slot in
for large multi-division events. (This is where the VEX Queue work lands — its function,
not its transport.)

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

## 7. Displays

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

## 8. Notifications

A **separate service** (`odm3-notify`) that subscribes to the view plane. It's the one
piece that may want internet (push to teams off the venue Wi-Fi) and has a different
reliability profile than match control — so it's isolated: it can fail, restart, or go
offline without touching anything that runs a match. Consumer of the match stream, never
a dependency of it.

---

## 9. Staff app

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

## 10. Fault model & concurrency

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

## 11. Open questions

- **Offline-sync model for the staff app** (the hard one): last-write-wins with a sync
  queue, or real conflict handling for scoring (two refs on one match, or a stale offline
  tablet)? Same decision native or RN.
- **Check-in granularity:** per-slot boolean, or `absent/present/no_show` enum?
- **Slot assignment:** does the portal push slot→controller mapping, or does the Pi
  autodiscover and report up for confirmation?
- V5 competition-port GPIO pin map / RJ45 breakout wiring.
- View-plane message envelope/versioning.

---

## 12. Tech stack

- **Pi + portal:** Go. One language across the control plane; the WebSocket contract is
  the *same Go types* on both ends via `odm3-contract`.
- **Types:** Go is the source of truth; TypeScript is **generated, never hand-written**
  (e.g. `tygo`), so the frontends can't drift from the server.
- **Portal DB:** SQLite (file-based, zero-config, copy-the-file backups; WAL mode).
- **Web:** TypeScript PWA (`odm3-portal-web`), service-worker offline.
- **Staff app:** React Native + Expo, shares the TS core.
- **Pi images:** Pi OS Lite, process as a `systemd` service on boot; configured portal
  address + hardware-serial device ID; identical image per role-class.
- **Testing:** Go `testing` with an injectable `Clock` and mock drivers — full lifecycle
  testable with no hardware.

---

## 13. Roadmap

**Phase 0 — Match core. [DONE]**
`match/` state machine: alliance / solo driving / solo coding, estop, abort, snapshot +
failsafe restore. Fully unit-tested.

**Phase 1 — Field-control vertical slice. [IN PROGRESS]**
`contract` (shared Go WS types) · run loop + driver registry interface · V5 GPIO
driver (legacy competition port) · portal device registry + registration handshake
(configured portal address, hardware-serial ID) · minimal portal able to Load/Start/Estop
one field.
*Done when:* a real V5 robot runs a full match on real hardware, driven end to end.
*Built so far:* all of the above as testable Go packages (`contract`, `transport`,
`driver`, `field`, `portal`) with an injectable clock and mock drivers; `cmd/demo`
runs the full slice in-process. *Remaining:* a WebSocket adapter for `transport.Conn`,
the real Pi GPIO wrapper behind `driver.Pin`, and the V5 competition-port pin map (§11).

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
cloud publish of results.
*Done when:* a complete event runs start to finish, including elims and skills, and
publishes when online.
