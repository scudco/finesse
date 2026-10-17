# Finesse: Deployment Quality-of-Life Options

Date: 2026-10-17. Status: design research, no code written.

## Today's friction map

| Step | Where | Pain |
|---|---|---|
| Start the server | `exe/finesse` | Separate process to remember; Procfile.dev line in dev, supervisor config in prod. |
| DB path | `exe/finesse` → `Platforms.cable_db_path` | Solved for the common case (parses `config/database.yml`); silently no-ops if parsing fails. |
| Signing key | `exe/finesse` → `bundle exec rails runner "print Finesse.signing_key"` | Boots the entire Rails app (seconds to tens of seconds) on every finesse start. Fails silently (stderr discarded, env var left unset) → Go server dies with `FINESSE_SIGNING_KEY environment variable is required`. |
| CORS origins | `--allow-origin` flag | Manual; must be repeated per origin; no default. |
| Supervision | operator | Restart-on-crash, log capture, shutdown ordering all left to the operator. |
| Deploys | operator | No documented path for zero-downtime replacement of the Go binary; shutdown timeout is 2 s, and catch-up after reconnect only consults the in-memory ring buffer, so a reconnecting client landing on a fresh process loses messages sent during the handoff. |

The shared surface today is exactly two things: the SolidCable SQLite table schema and the HMAC signing key. Every option below is judged partly on whether it keeps that surface small.

---

## Ranked options

### 1. Puma plugin — run finesse inside the web process (S) — BUILD THIS FIRST

**Design sketch.** Ship `lib/puma/plugin/finesse.rb` modeled directly on Solid Queue's plugin: `plugin :finesse` in `config/puma.rb` spawns the Go binary when Puma boots, sends it SIGINT when Puma stops or restarts, and monitors both directions (if Puma dies, stop finesse; if finesse dies, log loudly — optionally stop Puma). Because the plugin runs inside the already-booted Rails process, `Finesse.signing_key` is a direct method call — no `rails runner` subprocess at all. Derive `--db-path` and `--allow-origin` from Rails config (`Rails.application.config.action_cable`, `Rails.configuration.hosts`) with env/flag overrides.

```ruby
# config/puma.rb
plugin :finesse if ENV["FINESSE_IN_PUMA"] || Rails.env.development?
```

**Friction removed.** The entire dev story collapses to zero new steps beyond `gem "finesse"` and one puma.rb line — no Procfile entry, no second terminal, no `bin/dev` change. Boot-time key derivation disappears (Rails is already booted). Supervision for dev and single-host prod is inherited from Puma. This is the same trade Solid Queue made: run in-Puma for simple deployments, standalone for serious ones ([Solid Queue README, "Use Solid Queue in Development"](https://github.com/rails/solid_queue#use-solid-queue-in-development)).

**What it buys concretely.** Dev onboarding goes from ~4 README steps to 1. Production single-host (the PoC's actual target) gets restart coupling for free.

**Risks.**
- Spawning a Go binary from a Puma plugin on a phased restart needs care; Solid Queue's plugin shows the full lifecycle handling including Puma 6 vs 7 event-name differences (`on_booted` vs `after_booted`) and `exit!` in the forked child to avoid `at_exit` deadlocks ([solid_queue/lib/puma/plugin/solid_queue.rb](https://github.com/rails/solid_queue/blob/main/lib/puma/plugin/solid_queue.rb)). Copy that structure rather than inventing.
- Puma clustered mode forks workers; the plugin must spawn finesse once, from the launcher, not per worker (Solid Queue handles this the same way — fork from the launcher's boot hook).
- A dev who doesn't want the plugin still has the current Procfile path, so this is additive, not a cutover.

**Prior art.** Solid Queue's Puma plugin ([source](https://github.com/rails/solid_queue/blob/main/lib/puma/plugin/solid_queue.rb), [README section](https://github.com/rails/solid_queue#puma-plugin)); Thruster's gem-ships-a-Go-binary-and-wraps-Puma packaging is the same distribution model finesse already uses ([basecamp/thruster README](https://github.com/basecamp/thruster/blob/main/README.md)).

---

### 2. Rolling-deploy safety: DB-backed catch-up + longer drain (S–M)

**Design sketch.** Two small Go changes plus one doc page.

a) **DB fallback for catch-up.** Today `subscribe()` replays only from the in-memory ring buffer (`broadcaster.go`). On reconnect with `Last-Event-ID`, if the ID isn't in the buffer (or the broadcaster was just created by a new process), fall back to `SELECT id, payload FROM solid_cable_messages WHERE channel = ? AND id > ?` directly. The data is already in SQLite; the ring buffer stays as the fast path. This makes handoff between two Go processes lossless, which is the actual blocker for rolling deploys.

b) **Drain window.** Raise the shutdown timeout from the current hardcoded 2 s to a flag (`--drain-timeout`, default ~25 s, under common 30 s LB/kamal-proxy timeouts). On SIGTERM: stop accepting new `/events` connections immediately (return 503 so the proxy routes to the new process), keep existing SSE connections open until the timeout, then close. Browser `EventSource` reconnects automatically after a server close and resends `Last-Event-ID` ([MDN: Server-sent events / EventSource](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events)), so with (a) in place the drain can even be short — clients reconnect and catch up.

c) **Version header.** Add `--version-string` (or build-stamped) returned by `/up` and as `X-Finesse-Version` on `/events`, so an operator can tell which of two live binaries a client hit. Routing itself (path/header/cookie-based A/B) should stay in the proxy layer — kamal-proxy already does host/path routing and healthcheck-gated cutover ([Kamal proxy configuration](https://kamal-deploy.org/docs/configuration/proxy/)); duplicating traffic-splitting inside finesse would grow the Go server for no PoC-stage benefit.

**Friction removed.** Zero-downtime replacement of the Go binary becomes documented and lossless instead of "hope the 2 s window is quiet."

**Risks.** The DB fallback query must share the broadcaster's row decoding with the poll loop (it already decodes SolidCable payloads; reuse that). Catch-up replay before subscription registration has a tiny race (message arrives between the SELECT and live fan-out) — the existing buffer-scan-then-register ordering shows the pattern to copy.

**Prior art.** EventSource auto-reconnect with `Last-Event-ID` is built into the HTML standard ([HTML Standard, § Server-sent events](https://html.spec.whatwg.org/multipage/server-sent-events.html)); Action Cable's standalone mode documents the "second process behind the same proxy" deployment shape this enables ([Rails Guides: Running Standalone Cable Servers](https://guides.rubyonrails.org/action_cable_overview.html#running-standalone-cable-servers)).

---

### 3. Kamal recipe: published Docker image as an accessory (S–M)

**Design sketch.** The README's current Kamal section runs finesse as a Kamal *role* reusing the app image — that works, but it boots the Rails image and pays the `rails runner` key derivation unless the operator pre-sets `FINESSE_SIGNING_KEY`. The upgrade path: publish a minimal `finesse` Docker image (scratch + the Go binary, a few MB, same pattern as Thruster's gem-published binaries) that deploys as a Kamal **accessory** — Kamal's first-class mechanism for sidecar services with their own image, env, and ports ([Kamal accessories docs](https://kamal-deploy.org/docs/configuration/accessories/)).

```yaml
accessories:
  sse:
    image: ghcr.io/<org>/finesse:latest
    host: <server-ip>
    env:
      secret: [FINESSE_SIGNING_KEY]
      clear:
        BINDING: 0.0.0.0
    cmd: --allow-origin https://yourapp.com --db-path /cable/production_cable.sqlite3
    volumes:
      - /var/lib/app:/cable
```

`FINESSE_SIGNING_KEY` moves to `.kamal/secrets` (generated once per app by a `finesse:keygen`-style rake task that boots Rails once at setup time, not at every boot). kamal-proxy already fronts accessories-adjacent traffic and, per the current README's verified behavior, disables response buffering for `Content-Type: text/event-stream` (the historical buffering bug [kamal-proxy#137](https://github.com/basecamp/kamal-proxy/issues/137) predates this detection).

**Friction removed.** No Rails boot anywhere in the SSE lifecycle; the SSE process becomes infrastructure, updated and rolled independently of app deploys.

**Risks.** Accessory containers don't share the app image's SQLite file by default — the cable DB volume must be explicitly shared (as above) or finesse pointed at a shared path. Keeping the binary version in lockstep with the gem is a release-process concern (tag image and gem together).

**Prior art.** [Kamal accessories](https://kamal-deploy.org/docs/configuration/accessories/) and the `kamal accessory` command family ([docs](https://kamal-deploy.org/docs/commands/accessory)); Thruster proves the "tiny Go binary distributed to Rails users" model ([README](https://github.com/basecamp/thruster/blob/main/README.md)).

---

### 4. Rails engine bootstrap backchannel (M) — deferred; take the cheap 80% now

**Design sketch (the full version).** `mount Finesse::Engine => "/finesse"` exposes two endpoints: `GET /finesse/config` returning `{ signing_key: ..., db_path: ..., origins: [...] }` behind a bootstrap shared secret (`FINESSE_BOOTSTRAP_TOKEN` on both sides), and `GET /finesse/health` for the operator. The Go server, started with only `--bootstrap-url` + `--bootstrap-token`, fetches config at boot and re-fetches on SIGHUP. Key rotation then works: rotate `secret_key_base` via Rails' built-in rotator support ([Rails Guides: key rotation / `rotate`](https://guides.rubyonrails.org/security.html#custom-key-rotation-and-message-encryptors)), Rails serves both old and new verifier keys during the window, and finesse picks them up without a restart.

**The honest verdict.** This couples every finesse boot to the Rails app being up and reachable, adds a second secret to manage, and the "engine" is really a two-route controller wearing a hat. The actual problem it solves — `rails runner` boot cost and key rotation — has a cheaper 80% fix that keeps today's surface:

- **Cheap 80% (S): accept multiple signing keys.** `FINESSE_SIGNING_KEYS` = comma-separated hex keys; Go verifies a token against each (constant-time compare per key, stop on match). Ruby wrapper emits current + previous key. Rotation = set both, restart once, drop the old one. No new endpoint, no backchannel, no runtime dependency on Rails.

Build the cheap version now; revisit the engine only if finesse later needs *dynamic* config (per-tenant origins, runtime feature flags) — nothing in the PoC does.

**Prior art.** Action Cable's standalone mode keeps the cable process key-coupled to Rails config the same way ([Rails Guides](https://guides.rubyonrails.org/action_cable_overview.html#running-standalone-cable-servers)); Rails' `ActiveSupport::MessageVerifier#rotate`/`MessageRotator` is the rotation primitive on the Ruby side ([API docs](https://api.rubyonrails.org/classes/ActiveSupport/MessageVerifier.html)).

---

### 5. systemd unit generator (S) — only if non-Kamal deploys materialize

**Design sketch.** `rails g finesse:systemd` emits `finesse.service` (ExecStart the exe wrapper so key derivation still works, `Restart=always`, `ExecReload=/bin/kill -HUP $MAINPID` once multi-key support lands) plus a `finesse.socket` is *not* needed.

**Friction removed.** Supervision for operators not on Kamal/Docker.

**Verdict.** Low effort but speculative: the PoC's documented targets are Procfile.dev (dev) and Kamal (prod), and systemd users can write a 10-line unit faster than we can test a generator. Build only after a real user asks.

---

### 6. Feature flags and tenant-ization — do not build

**Per-tenant channel namespace in one process:** already exists for free — tenant scoping is a channel-naming concern (`chat:account_42`), the signed token already binds a client to exactly one channel, and broadcasters are per-channel. Nothing to build.

**Per-tenant processes:** doubles down on every friction above (N boot-time key derivations, N ports, N supervisor entries) to solve an isolation problem the PoC doesn't have.

**Feature-flagged rollout of finesse itself:** already solved at the client layer — Turbo's `<turbo-stream-source>` connects to whatever URL the page renders, so a flag flipping the rendered SSE URL (or the proxy route) is a Rails-level concern, not a finesse feature.

Revisit only when a deployment with real tenant isolation requirements shows up.

---

## Summary

| # | Option | Effort | Verdict |
|---|---|---|---|
| 1 | Puma plugin | S | **Build first** — removes the dev-story friction entirely, inherits supervision and key derivation from an already-booted Rails. |
| 2 | DB catch-up fallback + drain flag + version header | S–M | Build second — makes rolling deploys lossless; prerequisites are small Go changes. |
| 4 | Multi-key env var for rotation (cheap 80% of the engine backchannel) | S | Build with #2. Engine deferred until dynamic config is a real need. |
| 3 | Published Docker image + Kamal accessory recipe | S–M | Build when production Kamal users appear; depends on #4's no-boot key flow. |
| 5 | systemd generator | S | On request only. |
| 6 | Tenant-ization / flags | — | Rejected at this stage; channel naming and Rails-side flags already cover it. |

Guiding constraint held: options 1, 2, and 4-cheap keep the shared surface at today's SQLite schema + HMAC key(s); only the deferred engine option adds an HTTP surface, which is why it's deferred.
